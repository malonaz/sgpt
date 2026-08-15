// Package graph implements discovery of the repository's `.sgpt/` artifacts:
// a tree rooted by a `.sgpt.json` configuration where any directory can hold
// roles (`{title}.role.md`) and tool sets (`{title}.toolset`), all addressed
// please-style ("//dir:title").
package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	// RootFileName marks the root of a graph tree: the repo-local sgpt
	// configuration.
	RootFileName = ".sgpt.json"
	// ArtifactDirName is the per-directory directory holding sgpt artifacts.
	ArtifactDirName = ".sgpt"

	// RoleExtension is the extension of role files (markdown).
	RoleExtension = ".role.md"
	// ToolSetExtension is the extension of tool set files (JSON).
	ToolSetExtension = ".toolset"
)

// FindRoot walks up from dir looking for a .sgpt.json, returning the
// absolute path of the graph root.
func FindRoot(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, RootFileName)); err == nil {
			return current, nil
		}
		if current == filepath.Dir(current) {
			return "", fmt.Errorf("no %s found above %s", RootFileName, dir)
		}
	}
}

// File is one discovered .sgpt artifact together with its location.
type File[T proto.Message] struct {
	// Dir is the root-relative directory the artifact lives in ("." for root).
	Dir string
	// Title is the artifact file's stem.
	Title string
	// Extension is the artifact file's extension (".node", ".role", ...).
	Extension string
	// Message is the parsed payload.
	Message T
}

// Aliases for the artifact kinds.
type (
	RoleFile    = File[*sgptpb.Role]
	ToolSetFile = File[*sgptpb.ToolSet]
)

// Selector is the artifact's user-facing identifier, please-style:
// "//dir:title", or "//title" for artifacts at the graph root. Root
// selectors are resolved directory-first: a directory of the same name
// shadows the artifact.
func (f *File[T]) Selector() string {
	if f.Dir == "." {
		return "//" + f.Title
	}
	return "//" + f.Dir + ":" + f.Title
}

// FilePath is the artifact file's path under root.
func (f *File[T]) FilePath(root string) string {
	return filepath.Join(root, f.Dir, ArtifactDirName, f.Title+f.Extension)
}

// loadFiles reads every artifact of a directory (root-relative) with the
// given extension, in name order, using the kind's parser.
func loadFiles[T proto.Message](root, dir, extension string, parse func(data []byte) (T, error)) ([]*File[T], error) {
	entries, err := os.ReadDir(filepath.Join(root, dir, ArtifactDirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []*File[T]
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), extension) {
			continue
		}
		title := strings.TrimSuffix(entry.Name(), extension)
		path := filepath.Join(root, dir, ArtifactDirName, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		message, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		files = append(files, &File[T]{Dir: dir, Title: title, Extension: extension, Message: message})
	}
	return files, nil
}

// readMessage unmarshals a proto from a JSON file. Strict: unknown fields
// are errors, so a stale or mistyped artifact fails loudly instead of being
// silently half-read.
func readMessage(path string, message proto.Message) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := pbutil.JSONUnmarshalStrict(data, message); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// pathChain returns the parent chain of a root-relative directory, root
// first: "a/b/c" -> [".", "a", "a/b", "a/b/c"].
func pathChain(dir string) []string {
	chain := []string{"."}
	if dir == "." || dir == "" {
		return chain
	}
	var current string
	for _, segment := range strings.Split(dir, "/") {
		current = filepath.Join(current, segment)
		chain = append(chain, current)
	}
	return chain
}
