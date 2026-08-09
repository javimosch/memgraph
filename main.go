package main

import (
	"fmt"
	"os"
	"strings"
)

var (
	jsonOutput    bool
	noInteractive bool
	memoryDir     string
)

func main() {
	// Scan ALL args for global flags first, so jsonOutput is set
	// before any command dispatch or error handling.
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case arg == "--json" || arg == "-j":
			jsonOutput = true
		case arg == "--no-interactive" || arg == "-y":
			noInteractive = true
		case arg == "--memory-dir":
			if i+1 < len(os.Args) {
				memoryDir = os.Args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--memory-dir="):
			memoryDir = strings.TrimPrefix(arg, "--memory-dir=")
		}
	}

	// Find the first non-flag argument as the command
	command := ""
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if !strings.HasPrefix(arg, "-") {
			command = arg
			break
		}
		// Skip the value for --memory-dir
		if arg == "--memory-dir" && i+1 < len(os.Args) {
			i++
		}
	}

	if command == "" {
		if jsonOutput {
			errorResponse(85, "missing_command", "No command provided. Run 'memgraph --help' for available commands.", false)
		} else {
			printHelp()
		}
		os.Exit(85)
	}

	if command == "--help" || command == "-h" || command == "help" {
		printHelp()
		return
	}
	if command == "--version" || command == "-v" || command == "version" {
		handleVersion()
		return
	}

	// Intercept --help/-h as a subcommand argument (e.g. "memgraph recall --help")
	// so it doesn't get treated as a query or memory ID.
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == command {
			if i+1 < len(os.Args) && (os.Args[i+1] == "--help" || os.Args[i+1] == "-h") {
				printHelp()
				return
			}
			break
		}
	}

	cfg := Config{
		GlobalConfig: loadGlobalConfig(),
	}

	if memoryDir != "" {
		cfg.MemoryDir = memoryDir
	} else {
		gitRoot, err := findGitRepositoryRoot()
		if err == nil {
			cfg.ProjectRoot = gitRoot
			cfg.MemoryDir = getProjectMemoryPath(gitRoot)
		} else {
			cfg.MemoryDir = getDefaultMemoryDir()
		}
	}

	switch command {
	case "init":
		handleInit(&cfg)
	case "remember", "keep", "save":
		handleRemember(&cfg)
	case "recall", "search":
		handleRecall(&cfg)
	case "read":
		handleRead(&cfg)
	case "list", "ls":
		handleList(&cfg)
	case "sessions":
		handleSessions(&cfg)
	case "edit":
		handleEdit(&cfg)
	case "delete", "forget":
		handleDelete(&cfg)
	case "status":
		handleStatus(&cfg)
	case "config":
		handleConfig(&cfg)
	case "bridge":
		handleBridge(&cfg)
	case "profile":
		handleProfile(&cfg)
	case "projects":
		handleProjects(&cfg)
	case "demo":
		handleDemo(&cfg)
	case "import":
		handleImport(&cfg)
	case "graph-from-dir":
		handleGraphFromDir(&cfg)
	case "query":
		handleQuery(&cfg)
	case "related":
		handleRelated(&cfg)
	case "recommend":
		handleRecommend(&cfg)
	case "setup":
		handleSetup(&cfg)
	case "serve":
		handleServe(&cfg)
	case "watch":
		handleWatch(&cfg)
	case "plans":
		handlePlansList(&cfg)
	case "feedback":
		handleFeedback(&cfg)
	default:
		if jsonOutput {
			errorResponse(85, "unknown_command", fmt.Sprintf("Unknown command: %s. Run 'memgraph --help' for available commands.", command), false)
		} else {
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
			printHelp()
		}
		os.Exit(85)
	}
}
