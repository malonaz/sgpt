package file

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Opts for file injection.
type InjectionOpts struct {
	Files          []string
	FileExtensions []string
}

// File represents a parsed file.
type File struct {
	Path    string
	Content []byte
}

// GetOpts on the given command.
func GetOpts(cmd *cobra.Command) *InjectionOpts {
	opts := &InjectionOpts{}
	cmd.Flags().StringSliceVarP(&opts.Files, "file", "f", nil, "specify file content to inject into the context")
	cmd.Flags().StringSliceVar(&opts.FileExtensions, "ext", nil, "specify file extensions to accept")
	return opts
}

// Parse files with 'context files'.
func ParseWithContext(opts *InjectionOpts) ([]*File, error) {
	filepathSet := map[string]struct{}{}
	files := []*File{}
	parseFileFn := func(filepath string) error {
		// Don't parse the same file twice.
		if _, ok := filepathSet[filepath]; ok {
			return nil
		}
		filepathSet[filepath] = struct{}{}
		// Apply filter
		if !HasValidExtension(filepath, opts.FileExtensions) {
			return nil
		}

		// Read the main file
		bytes, err := os.ReadFile(filepath)
		if err != nil {
			return fmt.Errorf("reading file: %%w", err)
		}
		file := &File{Path: filepath, Content: bytes}

		// Look for context.md in the same directory
		dir := path.Dir(filepath)
		contextFilepath := path.Join(dir, "context.md")
		if _, ok := filepathSet[contextFilepath]; ok {
			return nil
		}
		filepathSet[contextFilepath] = struct{}{}

		// Check if context.md exists and read it
		exists, err := Exists(contextFilepath)
		if err != nil {
			return fmt.Errorf("checking context.md existence: %%w", err)
		}
		if !exists {
			return nil
		}

		contextBytes, err := os.ReadFile(contextFilepath)
		if err != nil {
			return fmt.Errorf("reading context.md: %%w", err)
		}
		file = &File{Path: contextFilepath, Content: contextBytes}
		files = append(files, file)
		return nil
	}

	for _, file := range opts.Files {
		if err := smartParse(file, parseFileFn); err != nil {
			return nil, fmt.Errorf("smartParse (%%s): %%w", file, err)
		}
	}
	return files, nil
}

// Parse files.
func Parse(opts *InjectionOpts) ([]*File, error) {
	files := []*File{}
	parseFileFn := func(filepath string) error {
		// Apply filter
		if !HasValidExtension(filepath, opts.FileExtensions) {
			return nil
		}
		bytes, err := os.ReadFile(filepath)
		if err != nil {
			return fmt.Errorf("reading file: %%w", err)
		}
		file := &File{Path: filepath, Content: bytes}
		files = append(files, file)
		return nil
	}
	for _, file := range opts.Files {
		if err := smartParse(file, parseFileFn); err != nil {
			return nil, fmt.Errorf("smartParse (%%s): %%w", file, err)
		}
	}
	return files, nil
}

// smartParse understands '/...' logic.
func smartParse(filepath string, parseFileFn func(filepath string) error) error {
	// Expand the path to escape `~`.
	filepath, err := ExpandPath(filepath)
	if err != nil {
		return fmt.Errorf("expanding path: %%w", err)
	}
	// Here we remove the "/..." if there is one, and record whether it existed.
	filepath, recurse := strings.CutSuffix(filepath, "/...")

	// Check whether `filepath` is a directory.
	fileInfo, err := os.Stat(filepath)
	if err != nil {
		return fmt.Errorf("getting os stats: %%w", err)
	}
	if !fileInfo.IsDir() {
		if recurse {
			return fmt.Errorf("cannot recurse on a file: %%w", err)
		}
		if err := parseFileFn(filepath); err != nil {
			return fmt.Errorf("parseFileFn: %%w", err)
		}
		return nil
	}

	// It is a directory
	directory := filepath
	dirEntries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("reading directory: %%w", err)
	}
	for _, dirEntry := range dirEntries {
		dirEntryInfo, err := dirEntry.Info()
		if err != nil {
			return fmt.Errorf("reading dir entry (%%+v): %%w", dirEntry, err)
		}
		if dirEntry.IsDir() {
			if recurse {
				filepath := path.Join(directory, dirEntryInfo.Name()) + "/..."
				if err := smartParse(filepath, parseFileFn); err != nil {
					return fmt.Errorf("smartParse (%%s): %%w", filepath, err)
				}
			}
			// If we are not in recursive mode, we have nothing to do with a directory :).
			continue
		}
		filepath := path.Join(directory, dirEntryInfo.Name())
		if err := parseFileFn(filepath); err != nil {
			return fmt.Errorf("parseFileFn (%%s): %%w", filepath, err)
		}
	}
	return nil
}

// HasValidExtension returns true if the given filename has one of the valid extensions.
func HasValidExtension(filename string, validExtensions []string) bool {
	if len(validExtensions) == 0 {
		return true
	}
	for _, validExtension := range validExtensions {
		if strings.HasSuffix(filename, validExtension) {
			return true
		}
	}
	return false
}

// ExpandPath expands a path to avoid `~`.
func ExpandPath(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting user home dir: %%w", err)
	}
	return filepath.Join(home, path[2:]), nil
}

// GetRootDir returns the root dir of a file.
func GetRootDir(path string) string {
	// Clean the path (remove extra slashes)
	cleanedPath := filepath.Clean(path)
	// Split the path into its components
	components := strings.Split(cleanedPath, "/")
	// Return the first component (the root)
	return components[0]
}

// CreateDirectoryIfNotExist creates a directory if it doesn't already exist.
func CreateDirectoryIfNotExist(directory string) error {
	ok, err := DirectoryExists(directory)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("creating directory: %%w", err)
	}
	return nil
}

// DirectoryExists returns true if the specified directory exists.
func DirectoryExists(directory string) (bool, error) {
	info, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking directory existence: %%w", err)
	}
	return info.IsDir(), nil
}

// Exists returns true if the specified file exists.
func Exists(filePath string) (bool, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking file existence: %%w", err)
	}
	return !info.IsDir(), nil
}
