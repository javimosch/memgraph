package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// mcpSupersede replaces a belief and records the replacement. This is the tool
// an agent should reach for instead of edit-or-delete when a memory has become
// wrong: edit destroys what was believed, supersede keeps it.
func mcpSupersede(cfg *Config, args map[string]any) string {
	id := mcpGetString(args, "id")
	if id == "" {
		return "Error: id is required"
	}
	cfg = resolveMCPConfig(cfg, mcpGetString(args, "project"))

	oldFile, ok := findMemoryFileByID(cfg.MemoryDir, id)
	if !ok {
		return fmt.Sprintf("Memory %s not found", id)
	}
	raw, err := os.ReadFile(oldFile)
	if err != nil {
		return fmt.Sprintf("Cannot read memory file: %v", err)
	}
	oldMemory := parseMemory(string(raw), filepath.Base(oldFile))
	if oldMemory.SupersededBy != "" {
		return fmt.Sprintf("Memory %s is already superseded by %s — supersede that one instead", id, oldMemory.SupersededBy)
	}

	newID := mcpGetString(args, "with")
	text := mcpGetString(args, "text")
	switch {
	case newID != "" && text != "":
		return "Error: pass either 'with' (an existing memory ID) or 'text' (new content), not both"
	case newID != "":
		if _, found := findMemoryFileByID(cfg.MemoryDir, newID); !found {
			return fmt.Sprintf("Replacement memory %s not found", newID)
		}
		if newID == id {
			return "Error: a memory cannot supersede itself"
		}
	case text != "":
		memoryType := mcpGetString(args, "type")
		if memoryType == "" {
			memoryType = oldMemory.Type
		}
		tags := mcpGetStringSlice(args, "tags")
		if len(tags) == 0 {
			tags = oldMemory.Tags
		}
		newID = writeSupersedingMemory(cfg, text, memoryType, oldMemory.Project, oldMemory.Session, tags)
	default:
		return "Error: replacement required — pass 'with' (existing memory ID) or 'text' (new content)"
	}

	now := time.Now().UTC()
	entry := LedgerEntry{
		Timestamp: now.Format(time.RFC3339),
		Action:    "supersede",
		MemoryID:  oldMemory.ID,
		Target:    newID,
		Reason:    mcpGetString(args, "reason"),
		Snapshot:  snapshotOf(oldMemory),
	}
	if err := appendLedger(cfg, entry); err != nil {
		return fmt.Sprintf("Cannot append to ledger, refusing to supersede: %v", err)
	}

	oldMemory.SupersededBy = newID
	oldMemory.SupersededAt = now
	oldMemory.SupersededReason = entry.Reason
	if err := os.WriteFile(oldFile, []byte(formatMemoryFile(oldMemory)), 0644); err != nil {
		return fmt.Sprintf("Cannot write memory file: %v", err)
	}
	updateSearchIndex(cfg)

	out, _ := json.Marshal(map[string]any{
		"id":            oldMemory.ID,
		"status":        "superseded",
		"superseded_by": newID,
		"superseded_at": now.Format(time.RFC3339),
		"reason":        entry.Reason,
	})
	return string(out)
}

// mcpVerify reports staleness. It never marks unless explicitly asked, and
// never reaches the network unless explicitly asked.
func mcpVerify(cfg *Config, args map[string]any) string {
	cfg = resolveMCPConfig(cfg, mcpGetString(args, "project"))

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		return "Memory directory not found"
	}

	useNet := mcpGetBool(args, "net", false)
	mark := mcpGetBool(args, "mark", false)
	limit := mcpGetInt(args, "limit", 0)

	var memories []Memory
	for _, memory := range index.Memories {
		if memory.SupersededBy != "" {
			continue
		}
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Created.After(memories[j].Created)
	})
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}

	checker := newClaimChecker(useNet)
	counts := map[string]int{}
	marked := 0
	var flagged []VerifyResult

	for _, memory := range memories {
		claims := verifyMemoryContent(checker, memory.Content)
		verdict := worstVerdict(claims)
		counts[verdict]++
		if mark && (verdict == verdictStale || verdict == verdictFresh) {
			if markMemory(cfg, memory.ID, verdict == verdictStale) {
				marked++
			}
		}
		if verdict == verdictStale || verdict == verdictUnknown {
			flagged = append(flagged, VerifyResult{
				ID:      memory.ID,
				Name:    memory.Name,
				Project: memory.Project,
				Verdict: verdict,
				Created: memory.Created.Format(time.RFC3339),
				Claims:  claims,
			})
		}
	}
	if marked > 0 {
		updateSearchIndex(cfg)
	}

	out, _ := json.Marshal(map[string]any{
		"checked":   len(memories),
		"stale":     counts[verdictStale],
		"unknown":   counts[verdictUnknown],
		"fresh":     counts[verdictFresh],
		"no_claims": counts[verdictNoClaims],
		"marked":    marked,
		"net":       useNet,
		"flagged":   flagged,
	})
	return string(out)
}

func mcpLedger(cfg *Config, args map[string]any) string {
	cfg = resolveMCPConfig(cfg, mcpGetString(args, "project"))

	entries, err := readLedger(cfg)
	if err != nil {
		return fmt.Sprintf("Cannot read ledger: %v", err)
	}
	if since := mcpGetString(args, "since"); since != "" {
		cutoff, ok := parseSince(since)
		if !ok {
			return fmt.Sprintf("Cannot parse since %q — use RFC3339, YYYY-MM-DD, or a window like 7d/24h/30m", since)
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
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if limit := mcpGetInt(args, "limit", 20); limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	out, _ := json.Marshal(map[string]any{
		"count":   len(entries),
		"entries": entries,
	})
	return string(out)
}
