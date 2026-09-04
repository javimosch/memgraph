package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleSupersede replaces a belief without destroying it. The old memory
// keeps its content and gains a forward pointer; the ledger gains an
// append-only record carrying a snapshot, so the trail survives even if the
// memory is later deleted.
func handleSupersede(cfg *Config) {
	positional, opts := parseCommandArgs(os.Args[2:])
	if len(positional) < 1 {
		errorResponse(80, "missing_argument", "Memory ID required. Usage: memgraph supersede <id> (--with <new-id> | --text <content>) [--reason <why>]", false)
		os.Exit(80)
	}

	oldID := positional[0]
	oldFile, ok := findMemoryFileByID(cfg.MemoryDir, oldID)
	if !ok {
		errorResponse(92, "memory_not_found", fmt.Sprintf("Memory with ID %s not found", oldID), false)
		os.Exit(92)
	}

	raw, err := os.ReadFile(oldFile)
	if err != nil {
		errorResponse(110, "read_error", fmt.Sprintf("Cannot read memory file: %v", err), false)
		os.Exit(110)
	}
	oldMemory := parseMemory(string(raw), filepath.Base(oldFile))

	if oldMemory.SupersededBy != "" {
		errorResponse(86, "already_superseded", fmt.Sprintf("Memory %s is already superseded by %s. Supersede that one instead.", oldID, oldMemory.SupersededBy), false)
		os.Exit(86)
	}

	// The replacement is either an existing memory or one written here.
	newID := strings.TrimSpace(opts.With)
	newText := ""
	if opts.TextSet {
		newText = opts.Text
	} else if len(positional) > 1 {
		newText = strings.Join(positional[1:], " ")
	}

	switch {
	case newID != "" && newText != "":
		errorResponse(85, "invalid_argument", "Pass either --with <existing-id> or replacement text, not both", false)
		os.Exit(85)
	case newID != "":
		if _, found := findMemoryFileByID(cfg.MemoryDir, newID); !found {
			errorResponse(92, "memory_not_found", fmt.Sprintf("Replacement memory %s not found", newID), false)
			os.Exit(92)
		}
		if newID == oldID {
			errorResponse(85, "invalid_argument", "A memory cannot supersede itself", false)
			os.Exit(85)
		}
	case newText != "":
		// Inherit the old memory's classification unless overridden, so a
		// correction lands in the same scope as the belief it replaces.
		memoryType := opts.MemoryType
		if memoryType == "" {
			memoryType = oldMemory.Type
		}
		project := oldMemory.Project
		if opts.ProjectSet {
			project = opts.Project
		}
		tags := oldMemory.Tags
		if opts.TagsSet {
			tags = opts.Tags
		}
		session := opts.Session
		if session == "" {
			session = os.Getenv("MEMGRAPH_SESSION")
		}
		if session == "" {
			session = os.Getenv("SICK_MEMORY_SESSION")
		}
		if session == "" {
			session = "default"
		}
		newID = writeSupersedingMemory(cfg, newText, memoryType, project, session, tags)
	default:
		errorResponse(80, "missing_argument", "Replacement required: pass --with <existing-id> or --text <content>", false)
		os.Exit(80)
	}

	now := time.Now().UTC()

	// The ledger is written BEFORE the memory changes. An action whose record
	// could not be persisted must not appear to have happened.
	entry := LedgerEntry{
		Timestamp: now.Format(time.RFC3339),
		Action:    "supersede",
		MemoryID:  oldMemory.ID,
		Target:    newID,
		Reason:    opts.Reason,
		Snapshot:  snapshotOf(oldMemory),
	}
	if err := appendLedger(cfg, entry); err != nil {
		errorResponse(110, "ledger_error", fmt.Sprintf("Cannot append to ledger, refusing to supersede: %v", err), false)
		os.Exit(110)
	}

	oldMemory.SupersededBy = newID
	oldMemory.SupersededAt = now
	oldMemory.SupersededReason = opts.Reason
	if err := os.WriteFile(oldFile, []byte(formatMemoryFile(oldMemory)), 0644); err != nil {
		errorResponse(110, "write_error", fmt.Sprintf("Cannot write memory file: %v", err), false)
		os.Exit(110)
	}

	updateSearchIndex(cfg)

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"id":            oldMemory.ID,
			"status":        "superseded",
			"superseded_by": newID,
			"superseded_at": now.Format(time.RFC3339),
			"reason":        opts.Reason,
			"ledger":        ledgerPath(cfg),
		})
	} else {
		fmt.Printf("Memory %s superseded by %s\n", oldMemory.ID, newID)
		if opts.Reason != "" {
			fmt.Printf("  reason: %s\n", opts.Reason)
		}
		fmt.Printf("  the old memory is kept and hidden from recall; pass --include-superseded to see it\n")
	}
}

// writeSupersedingMemory stores the replacement without printing, since the
// supersede command owns the output.
func writeSupersedingMemory(cfg *Config, content, memoryType, project, session string, tags []string) string {
	timestamp := time.Now().UTC()
	memoryID := fmt.Sprintf("%d", timestamp.UnixNano())
	filePath := filepath.Join(cfg.MemoryDir, fmt.Sprintf("memory_%s.md", memoryID))

	description := content
	if idx := strings.Index(description, "\n"); idx != -1 && idx < 50 {
		description = description[:idx]
	}
	if len(description) > 50 {
		description = description[:50] + "..."
	}

	memory := Memory{
		ID:          memoryID,
		Name:        "Memory " + memoryID,
		Description: description,
		Type:        memoryType,
		Project:     project,
		Session:     session,
		Tags:        tags,
		Created:     timestamp,
		Content:     content,
	}

	_ = os.MkdirAll(cfg.MemoryDir, 0755)
	if err := os.WriteFile(filePath, []byte(formatMemoryFile(memory)), 0644); err != nil {
		errorResponse(92, "resource_error", fmt.Sprintf("Failed to write replacement memory: %v", err), false)
		os.Exit(92)
	}
	return memoryID
}

func handleLedger(cfg *Config) {
	_, opts := parseCommandArgs(os.Args[2:])

	entries, err := readLedger(cfg)
	if err != nil {
		errorResponse(110, "read_error", fmt.Sprintf("Cannot read ledger: %v", err), false)
		os.Exit(110)
	}

	if opts.Since != "" {
		cutoff, ok := parseSince(opts.Since)
		if !ok {
			errorResponse(85, "invalid_argument", fmt.Sprintf("Cannot parse --since %q. Use RFC3339, YYYY-MM-DD, or a window like 7d/24h/30m.", opts.Since), false)
			os.Exit(85)
		}
		var filtered []LedgerEntry
		for _, entry := range entries {
			if ts, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil && ts.Before(cutoff) {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}

	// Newest first, matching every other listing in this CLI.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"count":   len(entries),
			"path":    ledgerPath(cfg),
			"entries": entries,
		})
		return
	}

	if len(entries) == 0 {
		fmt.Println("No ledger entries.")
		return
	}
	for _, entry := range entries {
		fmt.Printf("%s  %-10s %s", entry.Timestamp, entry.Action, entry.MemoryID)
		if entry.Target != "" {
			fmt.Printf(" -> %s", entry.Target)
		}
		fmt.Printf("  (%s)\n", entry.Actor)
		if entry.Reason != "" {
			fmt.Printf("    reason: %s\n", entry.Reason)
		}
	}
	fmt.Printf("\n%d entries in %s\n", len(entries), ledgerPath(cfg))
}
