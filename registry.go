package main

import (
	"github.com/javimosch/memgraph/internal/fsx"

	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectRegistry maps human-readable project names to memory directories.
// Stored at ~/.memgraph/projects.json. Allows `--project memgraph` to resolve
// globally regardless of which repo you're in, and survives repo moves/deletes.
type ProjectRegistry struct {
	Projects map[string]ProjectEntry `json:"projects"`
	// damaged says why the file could not be read. It is then never saved
	// over, so that what it holds can be repaired.
	damaged error
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

// readRegistry reads the registry file. found is false when there is none.
func readRegistry() (reg *ProjectRegistry, found bool) {
	reg = &ProjectRegistry{Projects: make(map[string]ProjectEntry)}
	data, err := os.ReadFile(registryPath())
	if errors.Is(err, fs.ErrNotExist) {
		return reg, false
	}
	if err == nil {
		err = json.Unmarshal(data, reg)
	}
	if err != nil {
		reg.damaged = fmt.Errorf("%s cannot be read, so it is left as it is until you repair or move it: %v", registryPath(), err)
		reg.Projects = nil
	}
	if reg.Projects == nil {
		reg.Projects = make(map[string]ProjectEntry)
	}
	return reg, true
}

// loadRegistry loads the project registry, creating it if missing.
// Also auto-imports existing scope dirs on first run.
func loadRegistry() *ProjectRegistry {
	reg, found := readRegistry()
	if !found {
		defer reg.lockForChange()()
		if _, err := os.Stat(registryPath()); err != nil {
			_ = reg.save()
		}
	}
	return reg
}

// lockForChange makes this process the only one changing the registry, and
// reads it again, so that a change made meanwhile is not lost. Call it before
// looking at what is about to change, and call unlock after save. It must not
// be taken twice. A first run auto-imports the existing scope dirs.
func (reg *ProjectRegistry) lockForChange() (unlock func()) {
	dir := getGlobalMemgraphDir()
	unlock, err := fsx.Lock(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memgraph: warning: could not lock %s: %v\n", dir, err)
		unlock = func() {}
	}
	fresh, found := readRegistry()
	if !found {
		fresh.autoImportScopes()
	}
	*reg = *fresh
	return unlock
}

// save writes the registry to disk, replacing the file in one step.
func (reg *ProjectRegistry) save() error {
	if reg.damaged != nil {
		return reg.damaged
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	path := registryPath()
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // keep a linked registry linked
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, data, 0644)
}

// sameDir reports whether two paths name one directory.
func sameDir(a, b string) bool {
	resolve := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if target, err := filepath.EvalSymlinks(p); err == nil {
			p = target
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// moveRefusal says why name must not be pointed at dir: it belongs to another
// folder that is still there. It returns "" when attaching is safe.
func (reg *ProjectRegistry) moveRefusal(name, dir string) string {
	entry, taken := reg.Projects[name]
	if !taken || sameDir(entry.Path, dir) || !dirExists(entry.Path) {
		return ""
	}
	return fmt.Sprintf("project %q already names %s, so nothing was changed; to move the name run 'memgraph attach %s --force' with the same options", name, entry.Path, name)
}

// registerIfNew gives dir the name, unless the name is taken.
func registerIfNew(name, dir string) error {
	reg := loadRegistry()
	defer reg.lockForChange()()
	if reg.lookup(name) != "" {
		return nil
	}
	reg.register(name, dir, scopeForMemoryDir(dir))
	return reg.save()
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

// rename moves an entry to a new name, keeping the memory dir, remote key,
// and original registration time. Returns false if old is missing or new
// is already registered.
func (reg *ProjectRegistry) rename(oldName, newName string) bool {
	entry, ok := reg.Projects[oldName]
	if !ok {
		return false
	}
	if _, taken := reg.Projects[newName]; taken {
		return false
	}
	delete(reg.Projects, oldName)
	reg.Projects[newName] = entry
	return true
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

// scopeForMemoryDir derives the scope key a memory dir belongs to — the
// parent dir name (in the conventional ~/.memgraph/projects/<scope>/memory
// layout, that is <scope>). The key always describes the memory dir itself,
// never the directory memgraph was launched from: a cwd-derived git remote
// says nothing about an explicitly attached --memory-dir and produced
// silent write redirects (#9).
func scopeForMemoryDir(memDir string) string {
	abs, err := filepath.Abs(memDir)
	if err != nil {
		abs = filepath.Clean(memDir)
	}
	scope := filepath.Base(filepath.Dir(abs))
	if scope == "." || scope == string(filepath.Separator) {
		return filepath.Base(abs)
	}
	return scope
}

// registryWarnings reports entries that can actually misroute a write (#22):
//   - scope conflict: the recorded remote names a scope dir that exists on
//     disk with its own memory dir, so repo-local git-scoped writes land
//     there while --project <name> writes to the registered path.
//   - shadow: an alias collides with a real scope dir of the same name
//     while pointing elsewhere — --project <name> resolves the registry
//     entry, never the scope a caller would naturally expect.
//
// A remote that disagrees with the memory dir's scope but names no live
// scope dir is metadata drift only (registryDrift) — Path alone drives
// resolution, so a stale remote cannot misroute anything.
func (reg *ProjectRegistry) registryWarnings() []string {
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	var warnings []string
	for name, entry := range reg.Projects {
		if entry.Remote != "" && entry.Remote != scopeForMemoryDir(entry.Path) {
			remoteMem := filepath.Join(projectsDir, entry.Remote, "memory")
			if dirExists(remoteMem) && filepath.Clean(remoteMem) != filepath.Clean(entry.Path) {
				warnings = append(warnings, fmt.Sprintf("project %q resolves to %s, but scope %q also exists — repo-local writes land there while --project %s writes to the registered path; pick one with 'memgraph attach %s --from-scope <scope> --force'", name, entry.Path, entry.Remote, name, name))
			}
		}
		if shadowed := filepath.Join(projectsDir, name, "memory"); dirExists(shadowed) && filepath.Clean(entry.Path) != shadowed {
			warnings = append(warnings, fmt.Sprintf("project %q resolves to %s, but a scope dir named %q also exists — --project %s writes to the registered path, not that scope", name, entry.Path, name, name))
		}
	}
	sort.Strings(warnings)
	return warnings
}

// registryDrift reports entries whose remote metadata disagrees with the
// scope their memory dir lives in but cannot misroute anything — the
// recorded remote scope has no memory dir on disk. Pre-#17 attaches and
// auto-imports left these behind; Path is authoritative for resolution,
// so this is stale metadata, not a routing problem. 'memgraph projects
// --repair' rewrites it.
func (reg *ProjectRegistry) registryDrift() []string {
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	var drift []string
	for name, entry := range reg.Projects {
		scope := scopeForMemoryDir(entry.Path)
		if entry.Remote == "" || entry.Remote == scope {
			continue
		}
		if dirExists(filepath.Join(projectsDir, entry.Remote, "memory")) {
			continue // live remote scope — a warning, not drift
		}
		drift = append(drift, fmt.Sprintf("project %q records remote %q but its memory dir is in scope %q — stale metadata, 'memgraph projects --repair' fixes it", name, entry.Remote, scope))
	}
	sort.Strings(drift)
	return drift
}

// repairResult reports what repairRegistry did.
type repairResult struct {
	Repaired []string `json:"repaired"` // aliases whose remote was normalized to the real scope
	Skipped  []string `json:"skipped"`  // aliases needing a manual decision (live remote scope)
}

// repairRegistry normalizes drifted remote metadata in place. When the
// recorded remote names a scope with no memory dir on disk, Path is
// authoritative and Remote is rewritten to the memory dir's real scope —
// the same value attach would record today. Entries whose remote scope
// dir DOES exist are skipped: both scopes are live, and choosing one is a
// data decision a repair flag should not make silently.
func (reg *ProjectRegistry) repairRegistry() repairResult {
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	res := repairResult{Repaired: []string{}, Skipped: []string{}}
	for name, entry := range reg.Projects {
		scope := scopeForMemoryDir(entry.Path)
		if entry.Remote == "" || entry.Remote == scope {
			continue
		}
		if dirExists(filepath.Join(projectsDir, entry.Remote, "memory")) {
			res.Skipped = append(res.Skipped, name)
			continue
		}
		entry.Remote = scope
		reg.Projects[name] = entry
		res.Repaired = append(res.Repaired, name)
	}
	sort.Strings(res.Repaired)
	sort.Strings(res.Skipped)
	return res
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
	if dirs := scopeDirsNamed(name); len(dirs) > 0 {
		return dirs[0]
	}
	return ""
}

// scopeDirsNamed lists the memory dirs of the scopes whose inferred name is name.
func scopeDirsNamed(name string) (dirs []string) {
	projectsDir := filepath.Join(getGlobalMemgraphDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() && inferProjectName(entry.Name()) == name {
			if memDir := filepath.Join(projectsDir, entry.Name(), "memory"); dirExists(memDir) {
				dirs = append(dirs, memDir)
			}
		}
	}
	return dirs
}

// fmtError is a helper to avoid importing fmt in callers that only need Sprintf.
var _ = fmt.Sprintf
