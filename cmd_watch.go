package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// handleWatch monitors skill directories for changes and rebuilds the graph
// automatically. This makes the stale graph bug (issue #1) structurally
// impossible — the graph is always live, like skills.match's filesystem scan.
//
// Inspired by planning-with-files (https://github.com/OthmanAdi/planning-with-files)
// which uses hooks to make persistence mechanical, not optional.
func handleWatch(cfg *Config) {
	_, opts := parseCommandArgs(os.Args[2:])

	// Determine directories to watch
	var dirs []string
	if len(opts.SyncDir) > 0 {
		dirs = parseSyncDirs(opts.SyncDir)
	} else {
		dirs = discoverSkillDirs()
	}
	if len(dirs) == 0 {
		errorResponse(80, "missing_argument", "No skill directories found. Use --sync-dir <path> or ensure standard skill dirs exist.", false)
		os.Exit(80)
	}

	// Determine target graph directory — always use the global skills-graph
	// (same as `graph-from-dir` with no args), not the project-local memory dir.
	targetDir := filepath.Join(getGlobalMemgraphDir(), "skills-graph")
	if cfg.MemoryDir != "" && strings.Contains(cfg.MemoryDir, "skills-graph") {
		targetDir = cfg.MemoryDir
	}
	cfg.MemoryDir = targetDir

	// Determine poll interval
	interval := 4 * time.Second
	if opts.PollInterval > 0 {
		interval = time.Duration(opts.PollInterval) * time.Second
	}

	// Initial build
	if err := watchRebuild(dirs, targetDir, cfg); err != nil {
		log.Printf("Initial build error: %v", err)
	}

	if jsonOutput {
		fmt.Printf(`{"status":"watching","dirs":%d,"interval_sec":%d,"target":"%s"}`+"\n", len(dirs), int(interval.Seconds()), targetDir)
	} else {
		fmt.Printf("memgraph watch: monitoring %d directories every %s\n", len(dirs), interval)
		fmt.Printf("  dirs: %s\n", strings.Join(dirs, ", "))
		fmt.Printf("  graph: %s\n", targetDir)
		fmt.Printf("  Press Ctrl+C to stop.\n\n")
	}

	// Track file state for change detection
	fileState := scanFileState(dirs)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			newState := scanFileState(dirs)
			changes := diffFileState(fileState, newState)
			if len(changes) > 0 {
				for _, c := range changes {
					if jsonOutput {
						fmt.Printf(`{"event":"%s","path":"%s"}`+"\n", c.Action, c.Path)
					} else {
						fmt.Printf("  [%s] %s\n", c.Action, c.Path)
					}
				}
				if err := watchRebuild(dirs, targetDir, cfg); err != nil {
					log.Printf("Rebuild error: %v", err)
				} else if !jsonOutput {
					fmt.Printf("  → graph rebuilt (%d changes)\n", len(changes))
				}
				fileState = newState
			}
		case <-sigCh:
			if !jsonOutput {
				fmt.Println("\nmemgraph watch: stopped.")
			}
			return
		}
	}
}

// watchRebuild performs a full graph rebuild from the given directories.
func watchRebuild(dirs []string, targetDir string, cfg *Config) error {
	graph, skillCount, namespaceCount, err := ingestMultiDir(dirs, targetDir, cfg)
	if err != nil {
		return err
	}
	_ = graph
	if !jsonOutput {
		log.Printf("  graph: %d skills, %d namespaces", skillCount, namespaceCount)
	}
	return nil
}

// fileState tracks the modification times of all SKILL.md files.
type fileState struct {
	mtimes map[string]time.Time
}

type fileChange struct {
	Path   string
	Action string // "created", "modified", "deleted"
}

// scanFileState walks the directories and records mtimes of all SKILL.md files.
func scanFileState(dirs []string) *fileState {
	state := &fileState{mtimes: make(map[string]time.Time)}
	for _, dir := range dirs {
		resolvedDir := expandHomeDir(dir)
		filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			// Follow symlinks: resolve to real path
			real, rerr := filepath.EvalSymlinks(path)
			if rerr != nil {
				real = path
			}
			if d.Name() == "SKILL.md" || strings.HasSuffix(d.Name(), ".md") {
				if info, err := os.Stat(real); err == nil {
					state.mtimes[real] = info.ModTime()
				}
			}
			return nil
		})
	}
	return state
}

// diffFileState compares old and new states, returning a list of changes.
func diffFileState(old, new *fileState) []fileChange {
	var changes []fileChange
	for path, newMtime := range new.mtimes {
		oldMtime, exists := old.mtimes[path]
		if !exists {
			changes = append(changes, fileChange{Path: path, Action: "created"})
		} else if !newMtime.Equal(oldMtime) {
			changes = append(changes, fileChange{Path: path, Action: "modified"})
		}
	}
	for path := range old.mtimes {
		if _, exists := new.mtimes[path]; !exists {
			changes = append(changes, fileChange{Path: path, Action: "deleted"})
		}
	}
	return changes
}
