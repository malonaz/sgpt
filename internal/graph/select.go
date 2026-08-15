package graph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

// localPrefix roots a selector at the graph root, please-style.
const localPrefix = "//"

// recursiveSuffix on a selector also selects the subtree's nodes.
const recursiveSuffix = "/..."

// Injection is one rendered context item ready to inject into a chat: Path
// is the virtual label ("//dir:title"), Content the rendered node.
type Injection struct {
	Path    string
	Content string
}

// parseSelector normalizes a local selector into its parts:
//   - "//dir:title" / "//:title" (root) — one node; bare "title" is accepted
//     as shorthand for a root node.
//   - "//dir" / "//" — a directory.
//   - "//dir/..." — a directory and its subtree.
func parseSelector(selector string) (dirPath, title string, hasTitle, recursive bool, err error) {
	if !strings.HasPrefix(selector, localPrefix) {
		// Bare shorthand for a root node title.
		if strings.ContainsAny(selector, "/:") {
			return "", "", false, false, fmt.Errorf("invalid selector %q: want \"//dir:title\", \"//dir\" or \"//dir/...\"", selector)
		}
		return ".", selector, true, false, nil
	}
	selector = strings.TrimPrefix(selector, localPrefix)
	recursive = strings.HasSuffix(selector, recursiveSuffix)
	selector = strings.TrimSuffix(selector, recursiveSuffix)
	dirPath, title, hasTitle = strings.Cut(selector, ":")
	if hasTitle && recursive {
		return "", "", false, false, fmt.Errorf("selector cannot combine :%s and %s", title, recursiveSuffix)
	}
	return filepath.Clean(dirPath), title, hasTitle, recursive, nil
}

// Select resolves selectors into ordered, deduplicated injections:
//   - "//dir" selects all of dir's nodes.
//   - "//dir:title" selects that one node ("//:title" or bare "title" at the root).
//   - "//dir/..." additionally selects the subtree's nodes, BFS top-down.
//
// Each node renders with its parent/child node names, so the model can pull
// more via the read_nodes tool.
func (t *Tree) Select(selectors []string) ([]Injection, error) {
	var selected []*NodeFile
	for _, selector := range selectors {
		dirPath, title, hasTitle, recursive, err := parseSelector(selector)
		if err != nil {
			return nil, err
		}
		target, ok := t.PathToDir[dirPath]
		// "//x" may name a directory or a root artifact titled x; matching
		// both is ambiguous and refused rather than silently picking one.
		if !hasTitle && !recursive && !strings.Contains(dirPath, "/") && dirPath != "." {
			rootNode := t.node(t.PathToDir["."], dirPath)
			if ok && rootNode != nil {
				return nil, fmt.Errorf(`%q is ambiguous: both a directory and a root node — use "//%s:..." or rename one`, selector, dirPath)
			}
			if !ok && rootNode != nil {
				selected = append(selected, rootNode)
				continue
			}
		}
		if !ok {
			return nil, fmt.Errorf("directory %q is not part of the graph", dirPath)
		}

		if hasTitle {
			nodeFile := t.node(target, title)
			if nodeFile == nil {
				return nil, fmt.Errorf("directory %q has no node %q (available: %s)",
					dirPath, title, strings.Join(t.titles(target), ", "))
			}
			selected = append(selected, nodeFile)
			continue
		}

		selected = append(selected, target.Nodes...)
		if !recursive {
			continue
		}
		queue := append([]*Dir(nil), target.Children...)
		for len(queue) > 0 {
			dir := queue[0]
			queue = queue[1:]
			selected = append(selected, dir.Nodes...)
			queue = append(queue, dir.Children...)
		}
	}

	var injections []Injection
	seen := map[string]bool{}
	for _, nodeFile := range selected {
		if seen[nodeFile.Selector()] {
			continue
		}
		seen[nodeFile.Selector()] = true
		injections = append(injections, Injection{
			Path:    t.Label(nodeFile),
			Content: t.Render(nodeFile),
		})
	}
	return injections, nil
}

// Resolve maps a node name ("//dir:title", or "title" for a root node) to
// its node file.
func (t *Tree) Resolve(name string) (*NodeFile, error) {
	dirPath, title, hasTitle, recursive, err := parseSelector(name)
	if err != nil {
		return nil, err
	}
	if recursive {
		return nil, fmt.Errorf(`%q does not name a single node: want "//dir:title"`, name)
	}
	if !hasTitle {
		_, isDirectory := t.PathToDir[dirPath]
		hasRootNode := !strings.Contains(dirPath, "/") && t.node(t.PathToDir["."], dirPath) != nil
		if isDirectory && hasRootNode {
			return nil, fmt.Errorf(`%q is ambiguous: both a directory and a root node — use "//%s:..." or rename one`, name, dirPath)
		}
		if !hasRootNode {
			return nil, fmt.Errorf(`%q does not name a single node: want "//dir:title"`, name)
		}
		dirPath, title = ".", dirPath
	}
	dir, exists := t.PathToDir[dirPath]
	if !exists {
		return nil, fmt.Errorf("directory %q is not part of the graph", dirPath)
	}
	nodeFile := t.node(dir, title)
	if nodeFile == nil {
		return nil, fmt.Errorf("directory %q has no node %q (available: %s)",
			dirPath, title, strings.Join(t.titles(dir), ", "))
	}
	return nodeFile, nil
}

// Selectors lists every selectable spelling, for shell completion: "//dir"
// and "//dir:title" ("//" and "//:title" at the root).
func (t *Tree) Selectors() []string {
	var selectors []string
	for _, dir := range t.Dirs {
		if len(dir.Nodes) == 0 {
			continue
		}
		dirSelector := localPrefix + dir.Path
		if dir.Path == "." {
			dirSelector = localPrefix
		}
		selectors = append(selectors, t.Prefix+dirSelector)
		for _, nodeFile := range dir.Nodes {
			selectors = append(selectors, t.Label(nodeFile))
		}
	}
	sort.Strings(selectors)
	return selectors
}

func (t *Tree) node(dir *Dir, title string) *NodeFile {
	return findByTitle(dir.Nodes, title)
}

func (t *Tree) titles(dir *Dir) []string {
	titles := make([]string, 0, len(dir.Nodes))
	for _, nodeFile := range dir.Nodes {
		titles = append(titles, nodeFile.Title)
	}
	return titles
}

func findByTitle[T proto.Message](files []*File[T], title string) *File[T] {
	for _, file := range files {
		if file.Title == title {
			return file
		}
	}
	return nil
}

// resolveFile maps a selector to a single artifact of the given kind.
func resolveFile[T proto.Message](t *Tree, name, kind string, get func(*Dir) []*File[T]) (*File[T], error) {
	dirPath, title, hasTitle, recursive, err := parseSelector(name)
	if err != nil {
		return nil, err
	}
	if recursive {
		return nil, fmt.Errorf(`%q does not name a single %s: want "//dir:title"`, name, kind)
	}
	if !hasTitle {
		_, isDirectory := t.PathToDir[dirPath]
		hasRootArtifact := !strings.Contains(dirPath, "/") && findByTitle(get(t.PathToDir["."]), dirPath) != nil
		if isDirectory && hasRootArtifact {
			return nil, fmt.Errorf(`%q is ambiguous: both a directory and a root %s — use "//%s:..." or rename one`, name, kind, dirPath)
		}
		if !hasRootArtifact {
			return nil, fmt.Errorf(`%q does not name a single %s: want "//dir:title"`, name, kind)
		}
		dirPath, title = ".", dirPath
	}
	dir, exists := t.PathToDir[dirPath]
	if !exists {
		return nil, fmt.Errorf("directory %q is not part of the graph", dirPath)
	}
	file := findByTitle(get(dir), title)
	if file == nil {
		return nil, fmt.Errorf("directory %q has no %s %q", dirPath, kind, title)
	}
	return file, nil
}

// ResolveRole maps a selector to a role file.
func (t *Tree) ResolveRole(name string) (*RoleFile, error) {
	return resolveFile(t, name, "role", func(dir *Dir) []*RoleFile { return dir.Roles })
}

// ResolveToolSet maps a selector to a tool set file.
func (t *Tree) ResolveToolSet(name string) (*ToolSetFile, error) {
	return resolveFile(t, name, "tool set", func(dir *Dir) []*ToolSetFile { return dir.ToolSets })
}

// RoleFiles returns every role of the tree, BFS order.
func (t *Tree) RoleFiles() []*RoleFile {
	var files []*RoleFile
	for _, dir := range t.Dirs {
		files = append(files, dir.Roles...)
	}
	return files
}

// ToolSetFiles returns every tool set of the tree, BFS order.
func (t *Tree) ToolSetFiles() []*ToolSetFile {
	var files []*ToolSetFile
	for _, dir := range t.Dirs {
		files = append(files, dir.ToolSets...)
	}
	return files
}
