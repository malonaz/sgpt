package diff

import "testing"

func TestRenameTarget(t *testing.T) {
	for _, tc := range []struct {
		name, path, diff, want string
	}{
		{"same headers, absolute path", "/repo/x.go", "--- a/x.go\n+++ b/x.go\n@@\n-a\n+b\n", "/repo/x.go"},
		{"same headers, dotted path", "./x.go", "--- a/x.go\n+++ b/x.go\n@@\n-a\n+b\n", "./x.go"},
		{"no headers", "/repo/x.go", "@@\n-a\n+b\n", "/repo/x.go"},
		{"rename", "x.go", "--- a/x.go\n+++ b/y.go\n@@\n-a\n+b\n", "y.go"},
		{"rename without old header", "x.go", "+++ b/y.go\n@@\n-a\n+b\n", "y.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fileDiff, err := ParseUnifiedDiff(tc.diff)
			if err != nil {
				t.Fatal(err)
			}
			if got := renameTarget(tc.path, fileDiff); got != tc.want {
				t.Fatalf("renameTarget = %q, want %q", got, tc.want)
			}
		})
	}
}
