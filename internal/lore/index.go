package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/malonaz/sgpt/internal/repo"
)

// Index is the set of reachable lore libraries — the enclosing repo's (when
// inside one) plus each configured import's — and the single place that
// resolves lore selectors to canonical names and file paths. Libraries are
// deduplicated by resolved root, so a repo reachable under several spellings
// (locally and as an import) has exactly one canonical library; every
// spelling still resolves, to the canonical name.
type Index struct {
	libraries []Library
	// prefixToLibrary maps every selector spelling ("" for the enclosing
	// repo, "@{import}" for each import) to its canonical library, as an
	// index into libraries.
	prefixToLibrary map[string]int
}

// NewIndex builds the index from the enclosing repo root (empty when outside
// a repo) and the configuration's imports.
func NewIndex(root string, imports *repo.Imports) *Index {
	index := &Index{prefixToLibrary: map[string]int{}}
	rootToIndex := map[string]int{}
	add := func(prefix, root string) {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolved = root
		}
		if i, ok := rootToIndex[resolved]; ok {
			// Alias spelling of an already-indexed repo: the first
			// (local when inside one) spelling stays canonical.
			index.prefixToLibrary[prefix] = i
			return
		}
		index.libraries = append(index.libraries, Library{Prefix: prefix, Root: root})
		rootToIndex[resolved] = len(index.libraries) - 1
		index.prefixToLibrary[prefix] = len(index.libraries) - 1
	}
	if root != "" {
		add("", root)
	}
	for _, name := range imports.Names() {
		importRoot, err := imports.Root(name)
		if err != nil {
			continue
		}
		add(repo.Prefix+name, importRoot)
	}
	return index
}

// Libraries returns the canonical libraries, enclosing repo first.
func (x *Index) Libraries() []Library {
	return x.libraries
}

// Resolve maps a lore selector to its canonical name and file path.
// Selectors mirror role/tool-set addressing: "lores/{lore}" targets the
// enclosing repo, "@{import}//lores/{lore}" an imported one. The name is
// canonical — alias spellings of the same repo resolve to one name — so it
// is safe to use as a dedupe key against search results.
func (x *Index) Resolve(selector string) (name, path string, err error) {
	prefix, local := "", selector
	if importName, rest, ok := repo.Split(selector); ok {
		prefix, local = repo.Prefix+importName, strings.TrimPrefix(rest, "//")
	}
	id := strings.TrimPrefix(local, loresDirName+"/")
	if id == local || id == "" {
		return "", "", fmt.Errorf("invalid lore selector %q: want \"lores/{lore}\" or \"@{import}//lores/{lore}\"", selector)
	}
	i, ok := x.prefixToLibrary[prefix]
	if !ok {
		return "", "", fmt.Errorf("lore %q: repo %q is not imported in the configuration", selector, strings.TrimPrefix(prefix, repo.Prefix))
	}
	library := x.libraries[i]
	path = filepath.Join(library.Dir(), filepath.FromSlash(id)+Extension)
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("lore %q not found at %s", selector, path)
	}
	return library.QualifyName(loresDirName + "/" + id), path, nil
}

// NameForPath maps a lore file path back to its canonical name; ok is false
// when the path lies outside every library. The inverse of Resolve, used to
// recognize lore files injected into a chat's context as plain files.
func (x *Index) NameForPath(path string) (string, bool) {
	resolved := resolvePath(path)
	for _, library := range x.libraries {
		relative, err := filepath.Rel(resolvePath(library.Dir()), resolved)
		if err != nil || strings.HasPrefix(relative, "..") || !strings.HasSuffix(relative, Extension) {
			continue
		}
		id := filepath.ToSlash(strings.TrimSuffix(relative, Extension))
		return library.QualifyName(loresDirName + "/" + id), true
	}
	return "", false
}

// resolvePath follows symlinks so path comparisons survive symlinked
// checkouts; unresolvable paths compare as-is.
func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
