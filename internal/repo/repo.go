// Package repo resolves the configuration's repo imports: the single place
// that knows how an "@{import}//{rest}" selector maps to a directory on
// disk. Every kind of imported artifact — roles, tool sets, lores — is
// addressed the same way, so they all share this.
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sgptpb "github.com/malonaz/sgpt/genproto/sgpt/v1"
)

const (
	// Prefix marks a selector as targeting an imported repo.
	Prefix = "@"
	// separator ends the import name and begins the local selector.
	separator = "//"
)

// Imports maps import names to their repo roots, in configuration order.
type Imports struct {
	names []string
	// nameToRoot holds absolute, "~"-expanded roots.
	nameToRoot map[string]string
}

// NewImports indexes the configuration's imports.
func NewImports(imports []*sgptpb.Import) *Imports {
	i := &Imports{nameToRoot: map[string]string{}}
	for _, repoImport := range imports {
		name := repoImport.GetName()
		if _, ok := i.nameToRoot[name]; ok {
			continue
		}
		i.names = append(i.names, name)
		i.nameToRoot[name] = ExpandHome(repoImport.GetPath())
	}
	return i
}

// Names returns the import names, in configuration order.
func (i *Imports) Names() []string { return i.names }

// Root returns the repo root of an import.
func (i *Imports) Root(name string) (string, error) {
	root, ok := i.nameToRoot[name]
	if !ok {
		return "", fmt.Errorf("repo %q is not imported in the configuration", name)
	}
	return root, nil
}

// Split splits "@{import}//{rest}" into its parts; ok is false for local
// selectors. The returned rest keeps its "//" prefix, so callers can hand
// it straight to a local resolver.
func Split(selector string) (importName, rest string, ok bool) {
	if !strings.HasPrefix(selector, Prefix) {
		return "", "", false
	}
	importName, rest, found := strings.Cut(selector[len(Prefix):], separator)
	if !found {
		return "", "", false
	}
	return importName, separator + rest, true
}

// Qualify prefixes a local selector with an import name, addressing it from
// the importing repo's point of view ("//go" -> "@core//go").
func Qualify(importName, selector string) string {
	if importName == "" {
		return selector
	}
	return Prefix + importName + separator + strings.TrimPrefix(selector, separator)
}

// ExpandHome resolves a leading "~/" against the user's home directory:
// import paths in the configuration commonly use it.
func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
