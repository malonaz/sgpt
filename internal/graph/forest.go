package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

// externalPrefix marks a selector as targeting an imported graph:
// "@{import name}//{selector}".
const externalPrefix = "@"

// IgnoreLoader returns the extra ignore patterns of the repo rooted at root
// (typically its configuration's top-level `ignore`). Imported graphs are
// scanned with *their own* patterns, never the primary repo's.
type IgnoreLoader func(root string) []string

// Forest is the primary graph plus the configuration's imported graphs,
// loaded lazily: an import is only scanned when a selector targets it (or
// when completion lists everything).
type Forest struct {
	// Primary is the enclosing graph's tree.
	Primary *Tree
	// loadIgnore provides an import's own ignore patterns; may be nil.
	loadIgnore       IgnoreLoader
	importNameToPath map[string]string
	importNameToTree map[string]*Tree
}

// NewForest wraps the primary tree with the configured repo imports.
func NewForest(primary *Tree, imports []*sgptpb.Import, loadIgnore IgnoreLoader) *Forest {
	forest := &Forest{
		Primary:          primary,
		loadIgnore:       loadIgnore,
		importNameToPath: map[string]string{},
		importNameToTree: map[string]*Tree{},
	}
	for _, repoImport := range imports {
		forest.importNameToPath[repoImport.GetName()] = repoImport.GetPath()
	}
	return forest
}

// splitExternal splits "@name//rest" selectors; ok is false for local ones.
// The returned rest keeps its "//" prefix.
func splitExternal(selector string) (importName, rest string, ok bool) {
	if !strings.HasPrefix(selector, externalPrefix) {
		return "", "", false
	}
	importName, rest, found := strings.Cut(selector[len(externalPrefix):], localPrefix)
	if !found {
		return "", "", false
	}
	return importName, localPrefix + rest, true
}

// tree returns the tree a selector targets, loading imports on first use,
// along with the selector stripped of its import prefix.
func (f *Forest) tree(selector string) (*Tree, string, error) {
	importName, rest, ok := splitExternal(selector)
	if !ok {
		return f.Primary, selector, nil
	}
	tree, err := f.importTree(importName)
	if err != nil {
		return nil, "", err
	}
	return tree, rest, nil
}

func (f *Forest) importTree(importName string) (*Tree, error) {
	if tree, ok := f.importNameToTree[importName]; ok {
		return tree, nil
	}
	path, ok := f.importNameToPath[importName]
	if !ok {
		return nil, fmt.Errorf("repo %q is not imported in the configuration", importName)
	}
	root := expandHome(path)
	if _, err := os.Stat(filepath.Join(root, RootFileName)); err != nil {
		return nil, fmt.Errorf("import %q has no %s at %s", importName, RootFileName, root)
	}
	// The import is scanned under its own rules: its .gitignore files and
	// its own configuration's ignore patterns.
	var ignorePatterns []string
	if f.loadIgnore != nil {
		ignorePatterns = append(ignorePatterns, f.loadIgnore(root)...)
	}
	tree, err := Scan(root, ignorePatterns)
	if err != nil {
		return nil, fmt.Errorf("scanning imported graph %q: %w", importName, err)
	}
	tree.Prefix = externalPrefix + importName
	f.importNameToTree[importName] = tree
	return tree, nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// ResolveRole maps a (possibly import-prefixed) role selector to its role,
// qualified for use by the importer: name/includes/tools carry
// the import prefix and files are made absolute against the role's own repo.
func (f *Forest) ResolveRole(name string) (*sgptpb.Role, error) {
	tree, localName, err := f.tree(name)
	if err != nil {
		return nil, err
	}
	roleFile, err := tree.ResolveRole(localName)
	if err != nil {
		return nil, err
	}
	return qualifyRole(tree, roleFile), nil
}

// Roles returns every role across the forest, qualified. Imports that fail
// to load are skipped silently (completion must never error).
// Nil-safe: a nil forest (failed construction) has no roles.
func (f *Forest) Roles() []*sgptpb.Role {
	return rolesOf(f.trees())
}

// PrimaryRoles returns the enclosing repo's roles only — completion offers
// imports only once the user reaches for them ("@...").
func (f *Forest) PrimaryRoles() []*sgptpb.Role {
	if f == nil {
		return nil
	}
	return rolesOf([]*Tree{f.Primary})
}

func rolesOf(trees []*Tree) []*sgptpb.Role {
	var roles []*sgptpb.Role
	for _, tree := range trees {
		for _, roleFile := range tree.RoleFiles() {
			roles = append(roles, qualifyRole(tree, roleFile))
		}
	}
	return roles
}

// ToolSets returns every tool set across the forest, qualified by selector.
func (f *Forest) ToolSets() []*sgptpb.ToolSet {
	return toolSetsOf(f.trees())
}

// PrimaryToolSets returns the enclosing repo's tool sets only.
func (f *Forest) PrimaryToolSets() []*sgptpb.ToolSet {
	if f == nil {
		return nil
	}
	return toolSetsOf([]*Tree{f.Primary})
}

func toolSetsOf(trees []*Tree) []*sgptpb.ToolSet {
	var toolSets []*sgptpb.ToolSet
	for _, tree := range trees {
		for _, toolSetFile := range tree.ToolSetFiles() {
			toolSet := proto.CloneOf(toolSetFile.Message)
			toolSet.Name = tree.Prefix + toolSetFile.Selector()
			toolSets = append(toolSets, toolSet)
		}
	}
	return toolSets
}

// trees returns the primary tree plus every loadable import, sorted.
// Nil-safe: construction failures leave callers with a nil forest.
func (f *Forest) trees() []*Tree {
	if f == nil {
		return nil
	}
	trees := []*Tree{f.Primary}
	importNames := make([]string, 0, len(f.importNameToPath))
	for importName := range f.importNameToPath {
		importNames = append(importNames, importName)
	}
	sort.Strings(importNames)
	for _, importName := range importNames {
		tree, err := f.importTree(importName)
		if err != nil {
			continue
		}
		trees = append(trees, tree)
	}
	return trees
}

// qualifyRole rewrites a role for use outside its home repo: its name and
// every selector it carries (includes, tool sets) gain the import prefix, and its files — root-relative in the file — become
// absolute paths under the repo's root.
func qualifyRole(tree *Tree, roleFile *RoleFile) *sgptpb.Role {
	role := proto.CloneOf(roleFile.Message)
	role.Name = tree.Prefix + roleFile.Selector()
	if role.Alias != "" && tree.Prefix != "" {
		// Bare aliases are reserved for the primary repo; imported aliases
		// are addressed as "@import//alias" (parsed as a root shorthand).
		role.Alias = tree.Prefix + localPrefix + role.Alias
	}
	for i, includedName := range role.GetRoles() {
		role.Roles[i] = tree.qualifySelector(includedName)
	}
	for i, toolName := range role.GetTools() {
		// Tool entries may be builtins ("diff") or tool set selectors.
		if strings.HasPrefix(toolName, localPrefix) || strings.HasPrefix(toolName, externalPrefix) {
			role.Tools[i] = tree.qualifySelector(toolName)
		}
	}
	for i, filePath := range role.GetFiles() {
		// "~" and absolute paths point outside the repo; only root-relative
		// paths are anchored to it.
		if !filepath.IsAbs(filePath) && !strings.HasPrefix(filePath, "~") {
			role.Files[i] = filepath.Join(tree.Root, filePath)
		}
	}
	return role
}

// qualifySelector prefixes a repo-local selector with the tree's import
// prefix; already-external selectors and bare root shorthands are prefixed
// canonically too.
func (t *Tree) qualifySelector(selector string) string {
	if t.Prefix == "" || strings.HasPrefix(selector, externalPrefix) {
		return selector
	}
	if !strings.HasPrefix(selector, localPrefix) {
		// Bare root shorthand ("title") canonicalizes to "//title".
		selector = localPrefix + selector
	}
	return t.Prefix + selector
}
