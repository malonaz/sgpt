// Package graph implements discovery of the repository's `.sgpt/` artifacts:
// a tree rooted by a `graph.sgpt` descriptor where any directory can hold
// knowledge nodes (`{title}.node`), roles (`{title}.role`) and tool sets
// (`{title}.toolset`), all addressed please-style ("//dir:title").
package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/malonaz/core/go/pbutil"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	// RootFileName marks the root of a graph tree: the repo-local sgpt
	// configuration.
	RootFileName = ".sgpt.json"
	// ArtifactDirName is the per-directory directory holding sgpt artifacts.
	ArtifactDirName = ".sgpt"

	// NodeExtension is the extension of knowledge node files.
	NodeExtension = ".node"
	// RoleExtension is the extension of role files.
	RoleExtension = ".role"
	// ToolSetExtension is the extension of tool set files.
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

// Aliases for the three artifact kinds.
type (
	NodeFile    = File[*sgptpb.Node]
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
// given extension, in name order.
func loadFiles[T proto.Message](root, dir, extension string, factory func() T) ([]*File[T], error) {
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
		message := factory()
		if err := readMessage(filepath.Join(root, dir, ArtifactDirName, entry.Name()), message); err != nil {
			return nil, fmt.Errorf("parsing %s %s of %s: %w", extension, title, dir, err)
		}
		files = append(files, &File[T]{Dir: dir, Title: title, Extension: extension, Message: message})
	}
	return files, nil
}

// SaveNode writes a node file back, stamping the deterministic fields
// (name, title and timestamps).
func SaveNode(root string, nodeFile *NodeFile) error {
	node := nodeFile.Message
	node.SetTitle(nodeFile.Title)
	node.SetName(nodeFile.Selector())
	if node.GetCreateTime() == nil {
		node.SetCreateTime(timestamppb.Now())
	}
	node.SetUpdateTime(timestamppb.Now())
	path := nodeFile.FilePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeMessage(path, node)
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

func writeMessage(path string, message proto.Message) error {
	data, err := pbutil.JSONMarshalPretty(message)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
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
