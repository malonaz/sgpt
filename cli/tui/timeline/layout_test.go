package timeline

import "testing"

func TestRebackground(t *testing.T) {
	const band = "48;2;1;2;3"
	for sequence, want := range map[string]string{
		"\x1b[38;5;252m":              "\x1b[38;5;252m",
		"\x1b[m":                      "\x1b[0;48;2;1;2;3m",
		"\x1b[0m":                     "\x1b[0;48;2;1;2;3m",
		"\x1b[49m":                    "\x1b[48;2;1;2;3m",
		"\x1b[48;5;238m":              "\x1b[48;2;1;2;3m",
		"\x1b[38;2;9;9;9;48;2;7;7;7m": "\x1b[38;2;9;9;9;48;2;1;2;3m",
		"\x1b[1;44m":                  "\x1b[1;48;2;1;2;3m",
		"\x1b[0;1;38;5;141m":          "\x1b[0;1;38;5;141;48;2;1;2;3m",
	} {
		if got := rebackground(sequence, band); got != want {
			t.Errorf("rebackground(%q) = %q, want %q", sequence, got, want)
		}
	}
}
