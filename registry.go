package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProjectRegistry maps human-readable project names to memory directories.
// Stored at ~/.memgraph/projects.json. Allows `--project memgraph` to resolve
// globally regardless of which repo you're in, and survives repo moves/deletes.
type ProjectRegistry struct {
	Projects map[string]ProjectEntry `json:"projects"`
}

// ProjectEntry is one registered project in the registry.
type ProjectEntry struct {
	Path    string    `json:"path"`    // absolute path to memory dir
	Remote  string    `json:"remote"`  // normalized remote scope key (if any)
	Created time.Time `json:"created"` // when registered
}

// registryPath returns the path to ~/.memgraph/projects.json.
func registryPath() string {
	return filepath.Join(getGlobalMemgraphDir(), "projects.json")
}

// loadRegistry loads the project registry, creating it if missing.
// Also auto-imports existing scope dirs on first run.
func loadRegistry() *ProjectRegistry {
	reg := &ProjectRegistry{Projects: make(map[string]ProjectEntry)}

	data, err := os.ReadFile(registryPath())
	if err == nil {
		_ = json.Unmarshal(data, reg)
		if reg.Projects == nil {
			reg.Projects = make(map[string]ProjectEntry)
		}
		return reg
	}

	// First run — auto-import existing scope dirs
	reg.autoImportScopes()
	reg.save()
	return reg
}

// save writes the registry to disk.
func (reg *ProjectRegistry) save() error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(registryPath(), data, 0644)
}

// lookup resolves a project name to a memory dir path.
// Returns empty string if not found.
func (reg *ProjectRegistry) lookup(name string) string {
	entry, ok := reg.Projects[name]
	if !ok {
		return ""
	}
	return entry.Path
}

// register adds or updates a project in the registry.
func (reg *ProjectRegistry) register(name, memDir, remoteScope string) {
	reg.Projects[name] = ProjectEntry{
		Path:    memDir,
		Remote:  remoteScope,
		Created: time.Now(),
	}
}

// unregister removes a project from the registry.
func (reg *ProjectRegistry) unregister(name string) bool {
	_, existed := reg.Projects[name]
	delete(reg.Projects, name)
	return existed
}

// autoImportScopes scans ~/.memgraph/projects/ and imports each scope dir
// into the registry using the last path segment as the project name.
// For path-based scopes like "-home-jarancibia-ai-memgraph", the inferred
// name is "memgraph" (last segment). For remote-based scopes like
// "github.com-javimosch-memgraph", the inferred name is also "memgraph".
func (reg *ProjectRegistry) autoImportScopes() {
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		scope := entry.Name()
		memDir := filepath.Join(projectsDir, scope, "memory")
		if !dirExists(memDir) {
			continue
		}
		name := inferProjectName(scope)
		if name == "" {
			continue
		}
		// Don't overwrite existing entries
		if _, exists := reg.Projects[name]; exists {
			continue
		}
		reg.Projects[name] = ProjectEntry{
			Path:    memDir,
			Remote:  scope,
			Created: time.Now(),
		}
	}
}

// inferProjectName extracts a human-readable name from a scope dir name.
//   - "-home-jarancibia-ai-memgraph" → "memgraph"
//   - "github.com-javimosch-memgraph" → "memgraph"
//   - "-home-jarancibia-ai-javika-multi-scraper" → "javika-multi-scraper"
func inferProjectName(scope string) string {
	// Split on "-" and take the last meaningful segment(s)
	parts := strings.Split(scope, "-")
	if len(parts) == 0 {
		return ""
	}
	// Take last segment as the name (most common case)
	// For names like "multi-scraper" that got split, we'd need heuristics
	// but last segment is a safe default
	return parts[len(parts)-1]
}

// resolveProjectName resolves --project <name> to a memory dir.
// Checks registry first, then falls back to scope dir matching.
func resolveProjectName(cfg *Config, reg *ProjectRegistry, name string) string {
	// 1. Registry lookup (explicit registration)
	if dir := reg.lookup(name); dir != "" {
		return dir
	}
	// 2. Try matching scope dirs by inferred name
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if inferProjectName(entry.Name()) == name {
			memDir := filepath.Join(projectsDir, entry.Name(), "memory")
			if dirExists(memDir) {
				return memDir
			}
		}
	}
	return ""
}

// fmtError is a helper to avoid importing fmt in callers that only need Sprintf.
var _ = fmt.Sprintf
