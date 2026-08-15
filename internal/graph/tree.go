package graph

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/malonaz/sgpt/internal/ignore"
)

// Dir is one directory of a graph tree.
type Dir struct {
	// Path is root-relative ("." for the root).
	Path string
	// Files are the non-ignored direct files (root-relative), excluding the
	// graph's own artifacts (graph.sgpt; .sgpt/ is skipped wholesale).
	Files []string
	// Children in name order.
	Children []*Dir
	// Nodes, Roles and ToolSets are the directory's artifacts, in name order.
	Nodes    []*NodeFile
	Roles    []*RoleFile
	ToolSets []*ToolSetFile
}

// Tree is a walked graph tree: every non-ignored directory with its nodes
// loaded.
type Tree struct {
	// Root is the absolute path of the graph root.
	Root string
	// Prefix qualifies artifact labels for imported graphs ("@{name}"); empty
	// for the primary graph.
	Prefix string
	// Dirs in BFS order, top-down (root first).
	Dirs []*Dir
	// PathToDir indexes Dirs by root-relative path.
	PathToDir map[string]*Dir
}

// Scan builds the tree without hashing: BFS, honoring .gitignore files and
// the extra patterns, loading every node file. Cheap — used for selection
// and completion.
func Scan(root string, extraIgnorePatterns []string) (*Tree, error) {
	matcher := ignore.NewMatcher(root, extraIgnorePatterns)
	tree := &Tree{Root: root, PathToDir: map[string]*Dir{}}

	queue := []*Dir{{Path: "."}}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		tree.Dirs = append(tree.Dirs, dir)
		tree.PathToDir[dir.Path] = dir

		entries, err := os.ReadDir(filepath.Join(root, dir.Path))
		if err != nil {
			return nil, fmt.Errorf("reading directory %s: %w", dir.Path, err)
		}
		// Parse this directory's .gitignore before judging its children.
		matcher.LoadDirectory(dir.Path)
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(dir.Path, name)
			if entry.IsDir() {
				if name == ".git" || name == ArtifactDirName || matcher.Ignored(path, true) {
					continue
				}
				child := &Dir{Path: path}
				dir.Children = append(dir.Children, child)
				queue = append(queue, child)
				continue
			}
			if name == RootFileName || matcher.Ignored(path, false) {
				continue
			}
			dir.Files = append(dir.Files, path)
		}

		if err := dir.loadArtifacts(root); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// loadArtifacts reads the directory's .sgpt files, stamping the
// deterministic identity fields — never trusted from the files themselves.
func (d *Dir) loadArtifacts(root string) error {
	var err error
	if d.Nodes, err = loadFiles(root, d.Path, NodeExtension, parseNodeMarkdown); err != nil {
		return err
	}
	for _, nodeFile := range d.Nodes {
		nodeFile.Message.SetName(nodeFile.Selector())
	}
	if d.Roles, err = loadFiles(root, d.Path, RoleExtension, parseRoleMarkdown); err != nil {
		return err
	}
	for _, roleFile := range d.Roles {
		roleFile.Message.Name = roleFile.Selector()
	}
	if d.ToolSets, err = loadFiles(root, d.Path, ToolSetExtension, parseToolSetJSON); err != nil {
		return err
	}
	for _, toolSetFile := range d.ToolSets {
		toolSetFile.Message.Name = toolSetFile.Selector()
	}
	return nil
}
