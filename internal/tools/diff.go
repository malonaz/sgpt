package tools

import (
	"fmt"
	"strings"
)

const diffContextLines = 3

type patch struct {
	Search     string `json:"search"`
	Replace    string `json:"replace"`
	ReplaceAll bool   `json:"replace_all"`
}

// applyPatches applies exact search/replace patches sequentially and builds a
// unified-style diff. The same code path serves review (dry-run) and
// execution, so the diff the user approves is exactly what gets applied.
func applyPatches(path, content string, patches []patch) (string, string, error) {
	var diff strings.Builder
	diff.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", path, path))
	// Line drift from earlier patches, so later hunk headers stay accurate.
	lineDelta := 0

	for i, p := range patches {
		if p.Search == "" {
			return "", "", fmt.Errorf("patch %d: empty search text", i+1)
		}
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
