package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// writeFlags lists the commands that change something, each with the flags it
// takes besides the global ones.
var writeFlags = map[string][]string{
	"init":           nil,
	"remember":       {"--type", "--tags", "--session", "--text"},
	"edit":           {"--type", "--tags", "--session"},
	"delete":         {"--reason"},
	"supersede":      {"--with", "--text", "--reason", "--type", "--tags", "--session"},
	"verify":         {"--mark", "--net", "--include-superseded", "--tags", "--session", "--limit"},
	"import":         {"--type", "--session"},
	"demo":           nil,
	"attach":         {"--from-scope", "--remove", "--attach-name", "--force"},
	"detach":         {"--purge"},
	"rename":         nil,
	"projects":       {"--repair"},
	"bridge":         nil,
	"setup":          {"--sync-dir"},
	"graph-from-dir": {"--include-plans"},
}

var commandAliases = map[string]string{"keep": "remember", "save": "remember", "forget": "delete", "unregister": "detach"}

var globalFlags = []string{"--json", "-j", "--no-interactive", "-y", "--human", "--memory-dir", "--project"}

// checkWriteFlags refuses, for a command that changes something, a flag it does
// not take and a flag left without its value. Text may start with a dash if it
// has a space in it, or follows "--".
func checkWriteFlags(command string, args []string) error {
	if alias, ok := commandAliases[command]; ok {
		command = alias
	}
	allowed, writes := writeFlags[command]
	if !writes {
		return nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return nil
		}
		// "-" is standard input, and "-5" a number.
		if len(arg) < 2 || arg[0] != '-' || unicode.IsDigit(rune(arg[1])) || strings.ContainsFunc(arg, unicode.IsSpace) && !strings.Contains(arg, "=") {
			continue
		}
		name, value, joined := strings.Cut(arg, "=")
		if !slices.Contains(allowed, name) && !slices.Contains(globalFlags, name) {
			return fmt.Errorf("%s does not take %s, so nothing was done; put -- before text that starts with a dash", command, name)
		}
		if !valueFlags[name] || name == "--remove" {
			continue
		}
		if joined {
			if value == "" && name != "--tags" {
				return fmt.Errorf("%s needs a value, so nothing was done", name)
			}
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return fmt.Errorf("%s needs a value, so nothing was done", name)
		}
		i++
	}
	return nil
}

// writesMemories reports whether a command changes the memories of a project.
func writesMemories(command string, opts CommandOptions) bool {
	if alias, ok := commandAliases[command]; ok {
		command = alias
	}
	switch command {
	case "init", "remember", "edit", "delete", "supersede", "import", "demo", "graph-from-dir":
		return true
	case "verify":
		return opts.Mark
	}
	return false
}

// checkWriteProject refuses to write for a project name that resolves to
// nothing, which would change the current repository instead. The repository's
// own name is accepted, so that its first save works.
func checkWriteProject(cfg *Config, reg *ProjectRegistry, name string) error {
	if reg.damaged != nil {
		return fmt.Errorf("nothing was done: %w", reg.damaged)
	}
	if reg.lookup(name) == "" {
		if dirs := scopeDirsNamed(name); len(dirs) > 1 {
			return fmt.Errorf("%q is not registered and matches more than one folder, so nothing was done: %s; register the one you mean with 'memgraph attach %s --from-scope <scope>'", name, strings.Join(dirs, ", "), name)
		}
	}
	if resolveProjectName(cfg, reg, name) != "" {
		return nil
	}
	if cfg.ProjectRoot != "" && (name == filepath.Base(cfg.ProjectRoot) || name == inferProjectName(scopeForMemoryDir(cfg.MemoryDir))) {
		return nil
	}
	return fmt.Errorf("unknown project %q, so nothing was done; see 'memgraph projects', or register it with 'memgraph attach %s' from its repository", name, name)
}
