package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Parents returns the nodes of a directory's ancestors, root first.
func (t *Tree) Parents(dir *Dir) []*NodeFile {
	var parents []*NodeFile
	chain := pathChain(dir.Path)
	for _, ancestor := range chain[:len(chain)-1] {
		if ancestorDir, ok := t.PathToDir[ancestor]; ok {
			parents = append(parents, ancestorDir.Nodes...)
		}
	}
	return parents
}

// Children returns the nodes of the nearest node-bearing descendant
// directories: descent stops at the first directory holding nodes, so the
// result is the next layer of the knowledge graph, not the whole subtree.
func (t *Tree) Children(dir *Dir) []*NodeFile {
	var children []*NodeFile
	queue := append([]*Dir(nil), dir.Children...)
	for len(queue) > 0 {
		descendant := queue[0]
		queue = queue[1:]
		if len(descendant.Nodes) > 0 {
			children = append(children, descendant.Nodes...)
			continue
		}
		queue = append(queue, descendant.Children...)
	}
	return children
}

// Render is the standardized rendering of a node: its content followed by
// the names (and summaries) of its parent and child nodes, so a reader knows
// what else can be pulled in.
func (t *Tree) Render(nodeFile *NodeFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", t.Label(nodeFile))
	if summary := nodeFile.Message.GetSummary(); summary != "" {
		fmt.Fprintf(&b, "%s\n\n", summary)
	}
	b.WriteString(strings.TrimSpace(nodeFile.Message.GetContent()))
	dir := t.PathToDir[nodeFile.Dir]
	t.appendRelated(&b, "Parent nodes", t.Parents(dir))
	t.appendRelated(&b, "Child nodes", t.Children(dir))
	t.appendFiles(&b, nodeFile)
	return b.String()
}

// appendFiles inlines the node's pulled-in files: knowledge that lives in a
// well-documented file is referenced, not duplicated into content.
func (t *Tree) appendFiles(b *strings.Builder, nodeFile *NodeFile) {
	for _, path := range nodeFile.Message.GetFiles() {
		content, err := os.ReadFile(filepath.Join(t.Root, path))
		if err != nil {
			// Degrade to an inline note: a vanished file must not sink the render.
			fmt.Fprintf(b, "\n\n## File: %s\n[unreadable: %v]\n", path, err)
			continue
		}
		fmt.Fprintf(b, "\n\n## File: %s\n```\n%s\n```\n", path, strings.TrimRight(string(content), "\n"))
	}
}

// Label is the node's fully qualified name: prefixed for imported graphs.
func (t *Tree) Label(nodeFile *NodeFile) string {
	return t.Prefix + nodeFile.Selector()
}

func (t *Tree) appendRelated(b *strings.Builder, label string, nodes []*NodeFile) {
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintf(b, "\n\n%s:\n", label)
	for _, nodeFile := range nodes {
		if summary := nodeFile.Message.GetSummary(); summary != "" {
			fmt.Fprintf(b, "- %s — %s\n", t.Label(nodeFile), summary)
		} else {
			fmt.Fprintf(b, "- %s\n", t.Label(nodeFile))
		}
	}
}
