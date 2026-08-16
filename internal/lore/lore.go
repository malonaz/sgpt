// Package lore implements lore libraries: agent-curated pieces of
// unstructured knowledge stored as markdown files (YAML front matter for
// metadata, markdown body for content) under a repo's `.sgpt/lores`
// directory — committed with the repo, so lore travels from repo to repo
// via the configuration's imports. Searched grep-style via the search_lores
// tool; each library's label vocabulary is persisted in its repo's
// `.sgpt/labels`.
package lore

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	// Extension of lore files: markdown with YAML front matter, so diffs
	// render as prose rather than as an escaped JSON blob.
	Extension = ".md"
	// loresDirName under a repo's .sgpt directory.
	loresDirName = "lores"
	// labelsFileName under a repo's .sgpt directory: the persisted label
	// vocabulary.
	labelsFileName = "labels"
)

// Library is one repo's lore collection.
type Library struct {
	// Prefix qualifies lore names for imported repos ("@{import}"); empty
	// for the enclosing repo.
	Prefix string
	// Root is the absolute path of the repo root.
	Root string
}

// Dir is the directory holding the library's lore files.
func (l Library) Dir() string {
	return filepath.Join(l.Root, ".sgpt", loresDirName)
}

// QualifyName prefixes a lore name with the library's import prefix
// ("lores/x" -> "@core//lores/x"), matching role/tool-set addressing.
func (l Library) QualifyName(name string) string {
	if l.Prefix == "" {
		return name
	}
	return l.Prefix + "//" + name
}

// Load reads every lore of the library, walking subdirectories — the agent
// is free to organize lores into folders. The resource name is stamped from
// the root-relative file location ("lores/{dir/.../stem}", qualified with
// the library's prefix) — never trusted from the file itself.
func (l Library) Load() ([]*sgptpb.Lore, error) {
	dir := l.Dir()
	var lores []*sgptpb.Lore
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A missing library is an empty library, not an error.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), Extension) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lore, err := UnmarshalMarkdown(data)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		relativePath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Subdirectories become ID segments: "go/errors.md" -> "lores/go/errors".
		loreResourceName := sgptpb.LoreResourceName{Lore: filepath.ToSlash(strings.TrimSuffix(relativePath, Extension))}
		lore.Name = l.QualifyName(loreResourceName.String())
		lores = append(lores, lore)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lores, nil
}

// SyncLabels persists the union of label keys and their distinct values
// across the library's lores to its repo's `.sgpt/labels` — a browsable
// index of the label vocabulary, so agents reuse keys instead of inventing
// near-duplicates.
func (l Library) SyncLabels(lores []*sgptpb.Lore) error {
	keyToValueSet := map[string]map[string]bool{}
	for _, lore := range lores {
		for key, value := range lore.GetLabels() {
			if keyToValueSet[key] == nil {
				keyToValueSet[key] = map[string]bool{}
			}
			keyToValueSet[key][value] = true
		}
	}
	keys := make([]string, 0, len(keyToValueSet))
	for key := range keyToValueSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		values := make([]string, 0, len(keyToValueSet[key]))
		for value := range keyToValueSet[key] {
			values = append(values, value)
		}
		sort.Strings(values)
		fmt.Fprintf(&b, "%s: %s\n", key, strings.Join(values, ", "))
	}
	sgptDir := filepath.Join(l.Root, ".sgpt")
	if err := os.MkdirAll(sgptDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sgptDir, labelsFileName), []byte(b.String()), 0o644)
}

// Match is one lore matched by a search.
type Match struct {
	Lore *sgptpb.Lore
	// Snippets are the matching content lines, grep-style.
	Snippets []string
	// MatchCount is the total number of matching content lines.
	MatchCount int
	// score orders matches: metadata hits outweigh content hits.
	score int
}

// maxSnippetsPerLore caps snippets so one giant lore doesn't flood results.
const maxSnippetsPerLore = 5

// Search runs a case-insensitive regular expression over every lore's
// title, description, labels and content, returning the topN best matches.
func Search(lores []*sgptpb.Lore, query string, topN int) ([]*Match, error) {
	pattern, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, fmt.Errorf("invalid query %q: %w", query, err)
	}
	if topN <= 0 {
		topN = 10
	}
	var matches []*Match
	for _, lore := range lores {
		match := &Match{Lore: lore}
		// Metadata hits rank a lore even without content snippets.
		if pattern.MatchString(lore.GetTitle()) || pattern.MatchString(lore.GetDescription()) {
			match.score += 10
		}
		for key, value := range lore.GetLabels() {
			if pattern.MatchString(key) || pattern.MatchString(value) {
				match.score += 5
			}
		}
		for _, line := range strings.Split(lore.GetContent(), "\n") {
			if !pattern.MatchString(line) {
				continue
			}
			match.MatchCount++
			if len(match.Snippets) < maxSnippetsPerLore {
				match.Snippets = append(match.Snippets, strings.TrimSpace(line))
			}
		}
		match.score += match.MatchCount
		if match.score > 0 {
			matches = append(matches, match)
		}
	}
	// Stable: equal scores keep name order from Load.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	if len(matches) > topN {
		matches = matches[:topN]
	}
	return matches, nil
}
