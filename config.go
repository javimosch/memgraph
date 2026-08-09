package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func getDefaultMemoryDir() string {
	return ".memgraph"
}

func getGlobalMemgraphDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return getDefaultMemoryDir()
	}
	memgraphDir := filepath.Join(homeDir, ".memgraph")
	if _, err := os.Stat(memgraphDir); err == nil {
		return memgraphDir
	}
	sickDir := filepath.Join(homeDir, ".sick-memory")
	if _, err := os.Stat(sickDir); err == nil {
		return sickDir
	}
	return memgraphDir
}

func findGitRepositoryRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}

// getGitRemoteURL returns the origin remote URL if the repo has one.
// Used as a stable scope key — survives repo moves and re-clones.
func getGitRemoteURL() string {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// normalizeRemoteURL strips protocol, auth, and .git suffix from a remote URL
// to produce a stable scope key. Examples:
//   git@github.com:javimosch/memgraph.git  →  github.com-javimosch-memgraph
//   https://github.com/javimosch/memgraph  →  github.com-javimosch-memgraph
//   ssh://git@gitlab.com/foo/bar.git       →  gitlab.com-foo-bar
func normalizeRemoteURL(url string) string {
	s := url
	// Strip protocol prefix
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "ssh://")
	s = strings.TrimPrefix(s, "git://")
	// Strip git@ prefix (SCP-style SSH)
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		s = strings.Replace(s, ":", "-", 1) // first colon → dash (github.com:user → github.com-user)
	}
	// Strip .git suffix
	s = strings.TrimSuffix(s, ".git")
	// Strip trailing slash
	s = strings.TrimSuffix(s, "/")
	// Sanitize remaining path separators
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, ":", "-")
	return s
}

func sanitizePath(path string) string {
	sanitized := strings.ReplaceAll(path, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, "\\", "-")
	sanitized = strings.ReplaceAll(sanitized, ":", "-")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")
	return sanitized
}

// getProjectMemoryPath resolves the memory directory for a git repo.
// Scope resolution order:
//  1. Remote-based scope (if repo has a remote and that scope dir exists)
//  2. Path-based scope (backward compat — old memories may be here)
//  3. Remote-based scope (create new — stable across repo moves)
//  4. Path-based scope (no remote — local-only repo)
func getProjectMemoryPath(gitRoot string) string {
	globalDir := getGlobalMemgraphDir()
	remote := getGitRemoteURL()
	pathScope := sanitizePath(gitRoot)

	if remote != "" {
		remoteScope := normalizeRemoteURL(remote)
		remoteDir := filepath.Join(globalDir, "projects", remoteScope, "memory")
		pathDir := filepath.Join(globalDir, "projects", pathScope, "memory")

		// If remote-based scope exists, use it (already migrated or new)
		if dirExists(remoteDir) {
			return remoteDir
		}
		// If path-based scope exists, keep using it (backward compat)
		if dirExists(pathDir) {
			return pathDir
		}
		// Neither exists — create remote-based (stable across moves)
		return remoteDir
	}

	// No remote — use path-based scope (local-only repo)
	return filepath.Join(globalDir, "projects", pathScope, "memory")
}

// dirExists checks if a directory exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func loadGlobalConfig() GlobalConfig {
	globalDir := getGlobalMemgraphDir()
	configPath := filepath.Join(globalDir, "config.json")

	config := GlobalConfig{
		DefaultMemoryType: "user",
		MaxMemorySize:     1024 * 1024,
		AutoIndex:         true,
		SearchWeights: SearchWeights{
			TFIDF:      1.0,
			Phrase:     3.0,
			Exact:      2.0,
			Recency24h: 1.2,
			Recency7d:  1.1,
			Type:       1.15,
			Tag:        1.5,
		},
	}

	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, &config)
	} else if err := os.MkdirAll(globalDir, 0755); err == nil {
		configData, _ := json.MarshalIndent(config, "", "  ")
		_ = os.WriteFile(configPath, configData, 0644)
	}

	return config
}
