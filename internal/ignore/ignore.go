// Package ignore implements gitignore-style path matching: per-directory
// .gitignore files plus extra patterns from configuration, unioned.
package ignore

import (
	"os"
	"path/filepath"
	"strings"
)

// pattern is one parsed gitignore line, anchored to the directory of the
// file it came from ("" for configuration patterns, which apply everywhere).
type pattern struct {
	// base is the root-relative directory the pattern is anchored to.
	base string
	// segments of the glob, split on "/".
	segments []string
	// negated re-includes a previously ignored path ("!pattern").
	negated bool
	// directoryOnly matches directories only ("pattern/").
	directoryOnly bool
	// anchored patterns ("/foo", "a/b") match relative to base; unanchored
	// ones ("*.log") match at any depth below it.
	anchored bool
}

// Matcher answers "is this path ignored?" for a tree rooted at root.
// Patterns are collected lazily: a directory's .gitignore is parsed the
// first time the directory is visited.
type Matcher struct {
	root     string
	patterns []pattern
}

// NewMatcher creates a matcher rooted at root with extra configuration
// patterns (gitignore syntax, applied as if from the root).
func NewMatcher(root string, extraPatterns []string) *Matcher {
	m := &Matcher{root: root}
	for _, line := range extraPatterns {
		m.addPattern("", line)
	}
	m.LoadDirectory(".")
	return m
}

// LoadDirectory parses dir's .gitignore (root-relative dir, "." for root),
// if present. Call it for every directory before matching its children.
func (m *Matcher) LoadDirectory(dir string) {
	data, err := os.ReadFile(filepath.Join(m.root, dir, ".gitignore"))
	if err != nil {
		return
	}
	base := dir
	if base == "." {
		base = ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		m.addPattern(base, line)
	}
}

func (m *Matcher) addPattern(base, line string) {
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	p := pattern{base: base}
	if strings.HasPrefix(line, "!") {
		p.negated = true
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.directoryOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if strings.HasPrefix(line, "/") {
		p.anchored = true
		line = line[1:]
	} else if strings.Contains(line, "/") {
		p.anchored = true
	}
	p.segments = strings.Split(line, "/")
	if len(p.segments) == 0 {
		return
	}
	m.patterns = append(m.patterns, p)
}

// Ignored reports whether the root-relative path is ignored.
// isDirectory must reflect the path's type (directory-only patterns).
func (m *Matcher) Ignored(path string, isDirectory bool) bool {
	if path == "." || path == "" {
		return false
	}
	// Last matching pattern wins (gitignore semantics: negations re-include).
	ignored := false
	for _, p := range m.patterns {
		if p.matches(path, isDirectory) {
			ignored = !p.negated
		}
	}
	return ignored
}

func (p pattern) matches(path string, isDirectory bool) bool {
	// Restrict to the pattern's anchor directory.
	if p.base != "" {
		prefix := p.base + "/"
		if !strings.HasPrefix(path, prefix) {
			return false
		}
		path = strings.TrimPrefix(path, prefix)
	}
	parts := strings.Split(path, "/")
	// A directory-only pattern matching the leaf itself requires a
	// directory; matching an ancestor ignores everything below regardless.
	leafOK := func(exact bool) bool { return !exact || !p.directoryOnly || isDirectory }
	if p.anchored {
		matched, exact := matchSegments(p.segments, parts)
		return matched && leafOK(exact)
	}
	// Unanchored: the pattern may match the path's tail at any depth. A
	// match on a parent directory also ignores everything below it, so try
	// every suffix start.
	for start := 0; start < len(parts); start++ {
		if matched, exact := matchSegments(p.segments, parts[start:]); matched && leafOK(exact) {
			return true
		}
	}
	return false
}

// matchSegments matches glob segments against path parts. A full match of
// the pattern against a prefix of the path counts: ignoring a directory
// ignores its contents; exact reports whether the whole path was consumed.
// Supports "**", "*", "?" and character-free globs.
func matchSegments(segments, parts []string) (matched, exact bool) {
	if len(segments) == 0 {
		// Pattern exhausted: prefix match, contents ignored too.
		return true, len(parts) == 0
	}
	if len(parts) == 0 {
		return false, false
	}
	if segments[0] == "**" {
		// "**" matches zero or more directories.
		if matched, exact := matchSegments(segments[1:], parts); matched {
			return matched, exact
		}
		return matchSegments(segments, parts[1:])
	}
	ok, err := filepath.Match(segments[0], parts[0])
	if err != nil || !ok {
		return false, false
	}
	return matchSegments(segments[1:], parts[1:])
}
