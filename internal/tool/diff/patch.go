package diff

import (
	"fmt"
	"strings"
)

const diffContextLines = 3

// Patch is a single exact search/replace edit.
type Patch struct {
	Search     string `json:"search"`
	Replace    string `json:"replace"`
	ReplaceAll bool   `json:"replace_all"`
}

// ApplyPatches applies search/replace patches sequentially and builds a
// unified-style diff. Patches whose search text has no exact match are healed
// against the content with whitespace-lenient matching. The same code path
// serves review (dry-run) and execution, so the diff the user approves is
// exactly what gets applied.
func ApplyPatches(path, content string, patches []Patch) (string, string, error) {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))
	// Line drift from earlier patches, so later hunk headers stay accurate.
	lineDelta := 0

	for i, p := range patches {
		if p.Search == "" {
			return "", "", fmt.Errorf("patch %d: empty search text", i+1)
		}
		// Heal against the current content so hunks with stale whitespace or
		// indentation still land, and later hunks see earlier hunks' edits.
		p = healPatch(content, p)
		count := strings.Count(content, p.Search)
		if count == 0 {
			return "", "", fmt.Errorf("patch %d: search text not found in %s", i+1, path)
		}
		if count > 1 && !p.ReplaceAll {
			return "", "", fmt.Errorf("patch %d: search text matches %d locations in %s; add more context or set replace_all", i+1, count, path)
		}
		occurrences := 1
		if p.ReplaceAll {
			occurrences = count
		}

		searchFrom := 0
		for range occurrences {
			index := strings.Index(content[searchFrom:], p.Search) + searchFrom
			// Expand the match to full lines so the diff shows complete lines.
			lineStart := strings.LastIndex(content[:index], "\n") + 1
			lineEnd := index + len(p.Search)
			if newline := strings.Index(content[lineEnd:], "\n"); newline != -1 {
				lineEnd += newline
			} else {
				lineEnd = len(content)
			}

			removed := strings.Split(content[lineStart:lineEnd], "\n")
			// Index-based replacement: the expanded region may contain other
			// occurrences of the search text.
			added := strings.Split(content[lineStart:index]+p.Replace+content[index+len(p.Search):lineEnd], "\n")

			allLines := strings.Split(content, "\n")
			startLine := strings.Count(content[:lineStart], "\n") // 0-based
			contextStart := max(0, startLine-diffContextLines)
			afterRemoved := startLine + len(removed)
			contextEnd := min(len(allLines), afterRemoved+diffContextLines)

			oldCount := (startLine - contextStart) + len(removed) + (contextEnd - afterRemoved)
			newCount := (startLine - contextStart) + len(added) + (contextEnd - afterRemoved)
			diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", contextStart+1, oldCount, contextStart+1+lineDelta, newCount))
			for _, line := range allLines[contextStart:startLine] {
				diff.WriteString(" " + line + "\n")
			}
			for _, line := range removed {
				diff.WriteString("-" + line + "\n")
			}
			for _, line := range added {
				diff.WriteString("+" + line + "\n")
			}
			for _, line := range allLines[afterRemoved:contextEnd] {
				diff.WriteString(" " + line + "\n")
			}

			lineDelta += len(added) - len(removed)
			content = content[:index] + p.Replace + content[index+len(p.Search):]
			// Resume past the replacement so a replace containing the search
			// text cannot loop forever.
			searchFrom = index + len(p.Replace)
		}
	}
	return content, diff.String(), nil
}

// healPatch relocates a patch whose search text has no exact match by
// comparing lines with whitespace leniency, then rebuilds the search from the
// file's actual lines. This absorbs the most common model diff mistakes:
// stale trailing whitespace and wrong indentation. Ambiguous or absent
// matches leave the patch untouched so ApplyPatches reports the error.
func healPatch(content string, p Patch) Patch {
	if strings.Contains(content, p.Search) {
		return p
	}
	searchLines := strings.Split(p.Search, "\n")
	contentLines := strings.Split(content, "\n")

	// Tier 1: ignore trailing whitespace only.
	if actual, ok := findUniqueLines(contentLines, searchLines, trimTrailingWhitespace); ok {
		p.Search = strings.Join(actual, "\n")
		return p
	}
	// Tier 2: ignore all surrounding whitespace, then re-indent the
	// replacement so inserted lines match the file's actual indentation.
	actual, ok := findUniqueLines(contentLines, searchLines, strings.TrimSpace)
	if !ok {
		return p
	}
	searchIndent := leadingWhitespace(searchLines[0])
	actualIndent := leadingWhitespace(actual[0])
	replaceLines := strings.Split(p.Replace, "\n")
	for i, line := range replaceLines {
		if strings.HasPrefix(line, searchIndent) && strings.TrimSpace(line) != "" {
			replaceLines[i] = actualIndent + line[len(searchIndent):]
		}
	}
	p.Search = strings.Join(actual, "\n")
	p.Replace = strings.Join(replaceLines, "\n")
	return p
}

// findUniqueLines slides a window over contentLines and returns the actual
// lines of the single window equal to searchLines under normalize; false if
// zero or multiple windows match.
func findUniqueLines(contentLines, searchLines []string, normalize func(string) string) ([]string, bool) {
	matchStart, matchCount := -1, 0
	for i := 0; i+len(searchLines) <= len(contentLines); i++ {
		match := true
		for j, searchLine := range searchLines {
			if normalize(contentLines[i+j]) != normalize(searchLine) {
				match = false
				break
			}
		}
		if match {
			matchStart = i
			if matchCount++; matchCount > 1 {
				return nil, false
			}
		}
	}
	if matchCount != 1 {
		return nil, false
	}
	return contentLines[matchStart : matchStart+len(searchLines)], true
}

func trimTrailingWhitespace(line string) string { return strings.TrimRight(line, " \t") }

func leadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// ParseUnifiedDiff converts a unified diff into search/replace patches, one
// per hunk. Line numbers in '@@' headers are ignored entirely — models
// miscount them constantly — so each hunk is anchored purely by its context
// and removed lines.
func ParseUnifiedDiff(diff string) ([]Patch, error) {
	var patches []Patch
	var search, replace []string
	inHunk := false
	flush := func() error {
		if !inHunk {
			return nil
		}
		// Drop trailing blank context (usually an artifact of the diff
		// string's final newline) so hunks anchor on real content.
		for len(search) > 0 && len(replace) > 0 &&
			search[len(search)-1] == "" && replace[len(replace)-1] == "" {
			search = search[:len(search)-1]
			replace = replace[:len(replace)-1]
		}
		if len(search) == 0 {
			return fmt.Errorf("hunk %d has no context or removed lines to anchor on", len(patches)+1)
		}
		patches = append(patches, Patch{
			Search:  strings.Join(search, "\n"),
			Replace: strings.Join(replace, "\n"),
		})
		search, replace = nil, nil
		return nil
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			if err := flush(); err != nil {
				return nil, err
			}
			inHunk = true
		case !inHunk:
			// Tolerate anything before the first hunk: '---'/'+++' file
			// headers, 'diff --git' lines, prose.
		case strings.HasPrefix(line, `\`):
			// "\ No newline at end of file" markers.
		case strings.HasPrefix(line, "+"):
			replace = append(replace, line[1:])
		case strings.HasPrefix(line, "-"):
			search = append(search, line[1:])
		case strings.HasPrefix(line, " "):
			search = append(search, line[1:])
			replace = append(replace, line[1:])
		default:
			// Context line missing its leading space — a common model slip,
			// notably on blank lines.
			search = append(search, line)
			replace = append(replace, line)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, fmt.Errorf("diff contains no hunks")
	}
	return patches, nil
}
