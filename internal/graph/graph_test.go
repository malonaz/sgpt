package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeNode(t *testing.T, root, dir, title, summary string) {
	t.Helper()
	node := &sgptpb.Node{}
	node.SetInstructions("instructions")
	node.SetSummary(summary)
	node.SetContent("content of " + title)
	nodeFile := &NodeFile{Dir: dir, Title: title, Extension: NodeExtension, Message: node}
	if err := SaveNode(root, nodeFile); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (string, *Tree) {
	t.Helper()
	root := t.TempDir()
	write(t, root, "a/x.go", "x")
	write(t, root, "a/b/y.go", "y")
	write(t, root, "a/c/z.go", "z")
	write(t, root, ".sgpt.json", "{}")
	writeNode(t, root, ".", "overview", "root summary")
	writeNode(t, root, "a", "architecture", "a summary")
	writeNode(t, root, "a", "testing", "a testing summary")
	writeNode(t, root, "a/b", "architecture", "b summary")

	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, tree
}

func TestNames(t *testing.T) {
	_, tree := setup(t)
	nodeFile := tree.PathToDir["a/b"].Nodes[0]
	if got := nodeFile.Message.GetName(); got != "//a/b:architecture" {
		t.Fatalf("name = %q, want //a/b:architecture", got)
	}
	if got := nodeFile.Message.GetTitle(); got != "architecture" {
		t.Fatalf("title = %q, want architecture", got)
	}
}

func TestRelated(t *testing.T) {
	_, tree := setup(t)
	dirRoot := tree.PathToDir["."]
	if parents := tree.Parents(dirRoot); len(parents) != 0 {
		t.Fatalf("root parents = %v, want none", parents)
	}
	// Children stop at the nearest node-bearing layer: "a" has nodes, so
	// "a/b" is not a direct child of the root in the knowledge graph.
	children := tree.Children(dirRoot)
	if len(children) != 2 || children[0].Selector() != "//a:architecture" || children[1].Selector() != "//a:testing" {
		t.Fatalf("root children = %v", selectors(children))
	}

	dirB := tree.PathToDir["a/b"]
	parents := tree.Parents(dirB)
	if len(parents) != 3 || parents[0].Selector() != "//overview" {
		t.Fatalf("a/b parents = %v", selectors(parents))
	}
}

func TestRender(t *testing.T) {
	_, tree := setup(t)
	rendered := tree.Render(tree.PathToDir["a"].Nodes[0])
	for _, want := range []string{"# //a:architecture", "content of architecture", "Parent nodes:", "//overview", "Child nodes:", "//a/b:architecture"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered node lacks %q:\n%s", want, rendered)
		}
	}
}

func TestSelect(t *testing.T) {
	_, tree := setup(t)

	injections, err := tree.Select([]string{"//a/b:architecture"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, injectionPaths(injections), []string{"//a/b:architecture"})

	injections, err = tree.Select([]string{"//a"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, injectionPaths(injections), []string{"//a:architecture", "//a:testing"})

	// dir/... selects the subtree; duplicates collapse.
	injections, err = tree.Select([]string{"//", "//a/..."})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, injectionPaths(injections), []string{"//overview", "//a:architecture", "//a:testing", "//a/b:architecture"})

	if _, err := tree.Select([]string{"//a:nope"}); err == nil {
		t.Fatal("selecting an unknown node did not error")
	}

	// Root nodes select by bare title.
	injections, err = tree.Select([]string{"overview"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, injectionPaths(injections), []string{"//overview"})
}

func TestResolve(t *testing.T) {
	_, tree := setup(t)
	nodeFile, err := tree.Resolve("//a:testing")
	if err != nil {
		t.Fatal(err)
	}
	if nodeFile.Selector() != "//a:testing" {
		t.Fatalf("resolved %q", nodeFile.Selector())
	}
	if _, err := tree.Resolve("//a"); err == nil {
		t.Fatal("resolving a directory name did not error")
	}
	rootNode, err := tree.Resolve("overview")
	if err != nil {
		t.Fatal(err)
	}
	if rootNode.Selector() != "//overview" {
		t.Fatalf("resolved %q", rootNode.Selector())
	}
}

func TestSelectors(t *testing.T) {
	_, tree := setup(t)
	assertEqual(t, tree.Selectors(), []string{
		"//",
		"//a", "//a/b", "//a/b:architecture",
		"//a:architecture", "//a:testing",
		"//overview",
	})
}

func selectors(nodeFiles []*NodeFile) []string {
	result := make([]string, 0, len(nodeFiles))
	for _, nodeFile := range nodeFiles {
		result = append(result, nodeFile.Selector())
	}
	return result
}

func injectionPaths(injections []Injection) []string {
	paths := make([]string, 0, len(injections))
	for _, injection := range injections {
		paths = append(paths, injection.Path)
	}
	return paths
}

func assertEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestForest(t *testing.T) {
	_, primaryTree := setup(t)

	importRoot := t.TempDir()
	write(t, importRoot, "go/grpc/x.go", "x")
	write(t, importRoot, ".sgpt.json", "{}")
	writeNode(t, importRoot, "go/grpc", "architecture", "grpc summary")

	graphImport := &sgptpb.Import{}
	graphImport.SetName("github.com/malonaz/core")
	graphImport.SetPath(importRoot)
	forest := NewForest(primaryTree, []*sgptpb.Import{graphImport}, nil)

	// External selection is prefixed; local selection is untouched.
	injections, err := forest.Select([]string{"//a:testing", "@github.com/malonaz/core//go/grpc:architecture"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, injectionPaths(injections), []string{"//a:testing", "@github.com/malonaz/core//go/grpc:architecture"})

	tree, nodeFile, err := forest.Resolve("@github.com/malonaz/core//go/grpc:architecture")
	if err != nil {
		t.Fatal(err)
	}
	if got := tree.Label(nodeFile); got != "@github.com/malonaz/core//go/grpc:architecture" {
		t.Fatalf("label = %q", got)
	}

	if _, err := forest.Select([]string{"@unknown//x:y"}); err == nil {
		t.Fatal("selecting from an unimported graph did not error")
	}

	// Completion includes the import's prefixed selectors.
	found := false
	for _, selector := range forest.Selectors() {
		if selector == "@github.com/malonaz/core//go/grpc:architecture" {
			found = true
		}
	}
	if !found {
		t.Fatal("forest selectors lack the imported node")
	}
}

func TestRenderPulledFiles(t *testing.T) {
	root, tree := setup(t)
	nodeFile := tree.PathToDir["a"].Nodes[0]
	nodeFile.Message.SetFiles([]string{"a/b/y.go", "missing.go"})
	if err := SaveNode(root, nodeFile); err != nil {
		t.Fatal(err)
	}
	rendered := tree.Render(nodeFile)
	for _, want := range []string{"## File: a/b/y.go", "```\ny\n```", "## File: missing.go", "[unreadable:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered node lacks %q:\n%s", want, rendered)
		}
	}
}

func TestRoleAndToolSetDiscovery(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x.go", "x")
	write(t, root, ".sgpt.json", "{}")
	write(t, root, "a/.sgpt/reviewer.role", `{"alias": "rev", "prompt": "review code", "files": ["a/x.go"], "roles": ["base"], "graph_nodes": ["//a:architecture"]}`)
	write(t, root, ".sgpt/base.role", `{"prompt": "base"}`)
	write(t, root, "a/.sgpt/engine.toolset", `{"engine_service": "my-client"}`)

	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	forest := NewForest(tree, nil, nil)

	roles := forest.Roles()
	if len(roles) != 2 {
		t.Fatalf("roles = %d, want 2", len(roles))
	}
	reviewer, err := forest.ResolveRole("//a:reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if reviewer.GetName() != "//a:reviewer" || reviewer.GetAlias() != "rev" {
		t.Fatalf("reviewer = %q / %q", reviewer.GetName(), reviewer.GetAlias())
	}
	// Primary-repo role files resolve to absolute paths.
	if !filepath.IsAbs(reviewer.GetFiles()[0]) {
		t.Fatalf("file not absolute: %q", reviewer.GetFiles()[0])
	}

	toolSets := forest.ToolSets()
	if len(toolSets) != 1 || toolSets[0].GetName() != "//a:engine" {
		t.Fatalf("toolSets = %v", toolSets)
	}
	if _, err := tree.ResolveToolSet("//a:engine"); err != nil {
		t.Fatal(err)
	}
}

func TestImportedRoleQualification(t *testing.T) {
	importRoot := t.TempDir()
	write(t, importRoot, "go/x.go", "x")
	write(t, importRoot, ".sgpt.json", "{}")
	write(t, importRoot, "go/.sgpt/expert.role",
		`{"alias": "ex", "prompt": "p", "files": ["go/x.go"], "roles": ["base", "//go:other"], "graph_nodes": ["//go:arch"], "tools": ["diff", "//go:engine"]}`)

	primaryRoot := t.TempDir()
	write(t, primaryRoot, ".sgpt.json", "{}")
	primaryTree, err := Scan(primaryRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	repoImport := &sgptpb.Import{}
	repoImport.SetName("malonaz/core")
	repoImport.SetPath(importRoot)
	forest := NewForest(primaryTree, []*sgptpb.Import{repoImport}, nil)

	expert, err := forest.ResolveRole("@malonaz/core//go:expert")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, []string{expert.GetName(), expert.GetAlias()}, []string{"@malonaz/core//go:expert", "@malonaz/core//ex"})
	assertEqual(t, expert.GetRoles(), []string{"@malonaz/core//base", "@malonaz/core//go:other"})
	assertEqual(t, expert.GetGraphNodes(), []string{"@malonaz/core//go:arch"})
	assertEqual(t, expert.GetTools(), []string{"diff", "@malonaz/core//go:engine"})
	if expert.GetFiles()[0] != filepath.Join(importRoot, "go/x.go") {
		t.Fatalf("file = %q", expert.GetFiles()[0])
	}
}

func TestRootSelectorAmbiguity(t *testing.T) {
	root := t.TempDir()
	// A directory "overview" AND a root node "overview".
	write(t, root, "overview/x.go", "x")
	write(t, root, ".sgpt.json", "{}")
	writeNode(t, root, ".", "overview", "root summary")
	writeNode(t, root, "overview", "architecture", "dir summary")

	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Select([]string{"//overview"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Select err = %v, want ambiguity error", err)
	}
	if _, err := tree.Resolve("//overview"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Resolve err = %v, want ambiguity error", err)
	}
	// Explicit forms stay resolvable.
	if _, err := tree.Select([]string{"//overview:architecture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Resolve("//:overview"); err != nil {
		t.Fatal(err)
	}
	// Recursive form is unambiguously a directory.
	if _, err := tree.Select([]string{"//overview/..."}); err != nil {
		t.Fatal(err)
	}
}
