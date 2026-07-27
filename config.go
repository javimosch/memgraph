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

func sanitizePath(path string) string {
	sanitized := strings.ReplaceAll(path, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, "\\", "-")
	sanitized = strings.ReplaceAll(sanitized, ":", "-")
	sanitized = strings.ReplaceAll(sanitized, " ", "_")
	return sanitized
}

func getProjectMemoryPath(gitRoot string) string {
	globalDir := getGlobalMemgraphDir()
	sanitizedRoot := sanitizePath(gitRoot)
	return filepath.Join(globalDir, "projects", sanitizedRoot, "memory")
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
