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

func TestRoleAndToolSetDiscovery(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/x.go", "x")
	write(t, root, ".sgpt.json", "{}")
	write(t, root, "a/.sgpt/reviewer.role.md", "@alias(\"rev\")\n@file(\"a/x.go\")\n@role(\"base\")\n\nreview code\n")
	write(t, root, ".sgpt/base.role.md", "base\n")
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
	// Root roles resolve by bare title.
	if _, err := forest.ResolveRole("base"); err != nil {
		t.Fatal(err)
	}

	toolSets := forest.ToolSets()
	if len(toolSets) != 1 || toolSets[0].GetName() != "//a:engine" {
		t.Fatalf("toolSets = %v", toolSets)
	}
	if _, err := tree.ResolveToolSet("//a:engine"); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.ResolveRole("//a:nope"); err == nil {
		t.Fatal("resolving an unknown role did not error")
	}
}

func TestImportedRoleQualification(t *testing.T) {
	importRoot := t.TempDir()
	write(t, importRoot, "go/x.go", "x")
	write(t, importRoot, ".sgpt.json", "{}")
	write(t, importRoot, "go/.sgpt/expert.role.md",
		"@alias(\"ex\")\n@file(\"go/x.go\")\n@role(\"base\")\n@role(\"//go:other\")\n@tool(\"diff\")\n@tool(\"//go:engine\")\n\np\n")

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
	assertEqual(t, expert.GetTools(), []string{"diff", "@malonaz/core//go:engine"})
	if expert.GetFiles()[0] != filepath.Join(importRoot, "go/x.go") {
		t.Fatalf("file = %q", expert.GetFiles()[0])
	}

	if _, err := forest.ResolveRole("@unknown//x:y"); err == nil {
		t.Fatal("resolving from an unimported repo did not error")
	}
}

func TestRootSelectorAmbiguity(t *testing.T) {
	root := t.TempDir()
	// A directory "overview" AND a root role "overview".
	write(t, root, "overview/x.go", "x")
	write(t, root, ".sgpt.json", "{}")
	write(t, root, ".sgpt/overview.role.md", "root role\n")
	write(t, root, "overview/.sgpt/architecture.role.md", "dir role\n")

	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.ResolveRole("//overview"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ResolveRole err = %v, want ambiguity error", err)
	}
	// Explicit forms stay resolvable.
	if _, err := tree.ResolveRole("//overview:architecture"); err != nil {
		t.Fatal(err)
	}
	if _, err := tree.ResolveRole("//:overview"); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactMarkdownErrors(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".sgpt.json", "{}")

	for name, content := range map[string]string{
		"unknown directive": "@bogus(\"x\")\n",
		"unterminated":      "@alias(\nnever closed\n)\n@tool(\"x\"\n",
		"alias arity":       "@alias(\"a\", \"b\")\n",
		"unquoted argument": "@file(unquoted)\n",
		"duplicate alias":   "@alias(\"a\")\n@alias(\"b\")\n",
		"removed @node":     "@node(\"//a:b\")\n",
	} {
		write(t, root, ".sgpt/bad"+RoleExtension, content)
		if _, err := Scan(root, nil); err == nil {
			t.Errorf("%s: Scan accepted invalid artifact:\n%s", name, content)
		}
	}

	// Body lines starting with "@" but not directive-shaped are fine.
	write(t, root, ".sgpt/bad"+RoleExtension, "@alias(\"ok\")\n\ntalk about @malonaz//go and a(b) things\n")
	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	role := tree.PathToDir["."].Roles[0].Message
	if role.GetAlias() != "ok" || !strings.Contains(role.GetPrompt(), "@malonaz//go") {
		t.Fatalf("parsed role = %v", role)
	}
}
