package history

import "testing"

func TestStoreSharedAndPersisted(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := Open()
	first, second := store.NewCursor(), store.NewCursor()
	first.Add("one")
	second.Add("two\nlines with a \\n literal")

	// Entries from either cursor are visible to both, newest first.
	if entry, _ := first.Previous("draft"); entry != "two\nlines with a \\n literal" {
		t.Fatalf("Previous = %q", entry)
	}
	if entry, _ := first.Previous(""); entry != "one" {
		t.Fatalf("Previous = %q", entry)
	}
	if _, ok := first.Previous(""); ok {
		t.Fatal("Previous past the oldest entry moved")
	}
	first.Next()
	if entry, _ := first.Next(); entry != "draft" {
		t.Fatalf("Next back to the draft = %q", entry)
	}

	// A reopened store reads the entries back intact.
	reopened := Open().NewCursor()
	if entry, _ := reopened.Previous(""); entry != "two\nlines with a \\n literal" {
		t.Fatalf("reloaded entry = %q", entry)
	}
}
