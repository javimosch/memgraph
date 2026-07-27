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
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(85)
	}

	command := os.Args[1]

	if command == "--help" || command == "-h" || command == "help" {
		printHelp()
		return
	}
	if command == "--version" || command == "-v" || command == "version" {
		handleVersion()
		return
	}

	for i := 2; i < len(os.Args); i++ {
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
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printHelp()
		os.Exit(85)
	}
}
