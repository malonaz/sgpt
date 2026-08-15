package graph

import (
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"
)

// localPrefix roots a selector at the graph root, please-style.
const localPrefix = "//"

// parseSelector normalizes a local selector into its parts:
//   - "//dir:title" / "//:title" (root) — one artifact; bare "title" is
//     accepted as shorthand for a root artifact.
//   - "//dir" / "//" — a directory.
func parseSelector(selector string) (dirPath, title string, hasTitle bool, err error) {
	if !strings.HasPrefix(selector, localPrefix) {
		// Bare shorthand for a root artifact title.
		if strings.ContainsAny(selector, "/:") {
			return "", "", false, fmt.Errorf("invalid selector %q: want \"//dir:title\" or \"//dir\"", selector)
		}
		return ".", selector, true, nil
	}
	selector = strings.TrimPrefix(selector, localPrefix)
	dirPath, title, hasTitle = strings.Cut(selector, ":")
	return filepath.Clean(dirPath), title, hasTitle, nil
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
	dirPath, title, hasTitle, err := parseSelector(name)
	if err != nil {
		return nil, err
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
