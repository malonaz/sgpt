package file

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetGitHubRepo returns the GitHub repository name by walking up the directory tree
// from the given file path until it finds a .git directory, then parsing the git config.
// Returns empty string if not in a git repo or if not a GitHub repository.
func GetGitHubRepo(filePath string) (string, error) {
	// First find the .git directory by walking up
	dir := filepath.Dir(filePath)
	gitRoot := ""

	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			gitRoot = dir
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// We've reached the root without finding .git
			return "", nil
		}
		dir = parent
	}

	// If we found a .git directory, try to read the config file
	configPath := filepath.Join(gitRoot, ".git", "config")
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("opening git config: %%w", err)
	}
	defer file.Close()

	// Scan the config file looking for the remote "origin" URL
	scanner := bufio.NewScanner(file)
	foundOrigin := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == `[remote "origin"]` {
			foundOrigin = true
			continue
		}

		if foundOrigin && strings.HasPrefix(line, "url = ") {
			url := strings.TrimPrefix(line, "url = ")
			return parseGitHubRepo(url), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning git config: %%w", err)
	}

	return "", nil
}

// parseGitHubRepo extracts the GitHub repository name from a git URL
func parseGitHubRepo(url string) string {
	// Handle different URL formats:
	// https://github.com/owner/repo.git
	// git@github.com:owner/repo.git
	// https://github.com/owner/repo

	url = strings.TrimSuffix(url, ".git")

	if strings.HasPrefix(url, "https://github.com/") {
		return strings.TrimPrefix(url, "https://github.com/")
	}

	if strings.HasPrefix(url, "git@github.com:") {
		return strings.TrimPrefix(url, "git@github.com:")
	}

	return ""
}
