package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// exitStaleFound is returned when verify finds at least one stale memory. It
// sits in memgraph's semantic 80-119 band so a cron job can branch on it
// without parsing JSON.
const exitStaleFound = 90

func handleVerify(cfg *Config) {
	_, opts := parseCommandArgs(os.Args[2:])

	index, err := loadSearchIndex(cfg.MemoryDir)
	if err != nil {
		if jsonOutput {
			errorResponse(92, "resource_not_found", "Memory directory not found", false)
		} else {
			fmt.Fprintln(os.Stderr, "Memory directory not found. Run 'memgraph init' first.")
		}
		os.Exit(92)
	}

	projectFilter := opts.Project
	if cfg.ScopeResolved {
		projectFilter = ""
	}

	var memories []Memory
	for _, memory := range index.Memories {
		if projectFilter != "" && memory.Project != projectFilter {
			continue
		}
		if opts.MemoryType != "" && memory.Type != opts.MemoryType {
			continue
		}
		if !memoryHasAllTags(memory.Tags, opts.Tags) {
			continue
		}
		if memory.SupersededBy != "" && !opts.IncludeSuperseded {
			continue
		}
		memories = append(memories, memory)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Created.After(memories[j].Created)
	})
	if opts.Limit > 0 && len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}

	checker := newClaimChecker(opts.Net)
	results := make([]VerifyResult, 0, len(memories))
	counts := map[string]int{}
	marked := 0

	for _, memory := range memories {
		claims := verifyMemoryContent(checker, memory.Content)
		verdict := worstVerdict(claims)
		counts[verdict]++

		result := VerifyResult{
			ID:      memory.ID,
			Name:    memory.Name,
			Project: memory.Project,
			Verdict: verdict,
			Created: memory.Created.Format(time.RFC3339),
			Claims:  claims,
		}
		if opts.Mark && (verdict == verdictStale || verdict == verdictFresh) {
			if markMemory(cfg, memory.ID, verdict == verdictStale) {
				result.Marked = true
				marked++
			}
		}
		results = append(results, result)
	}

	if marked > 0 {
		updateSearchIndex(cfg)
	}

	// Stale first, then unknown: the reader's attention goes where the
	// evidence is.
	rank := map[string]int{verdictStale: 0, verdictUnknown: 1, verdictFresh: 2, verdictNoClaims: 3}
	sort.SliceStable(results, func(i, j int) bool {
		return rank[results[i].Verdict] < rank[results[j].Verdict]
	})

	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"checked":   len(results),
			"stale":     counts[verdictStale],
			"unknown":   counts[verdictUnknown],
			"fresh":     counts[verdictFresh],
			"no_claims": counts[verdictNoClaims],
			"marked":    marked,
			"net":       opts.Net,
			"results":   results,
		})
	} else {
		printVerifyHuman(results, counts, marked, opts.Net)
	}

	if counts[verdictStale] > 0 {
		os.Exit(exitStaleFound)
	}
}

func printVerifyHuman(results []VerifyResult, counts map[string]int, marked int, useNet bool) {
	for _, result := range results {
		if result.Verdict == verdictFresh || result.Verdict == verdictNoClaims {
			continue
		}
		fmt.Printf("%-7s %s — %s\n", result.Verdict, result.ID, result.Name)
		for _, claim := range result.Claims {
			if claim.Verdict == verdictFresh {
				continue
			}
			detail := claim.Detail
			if detail != "" {
				detail = " (" + detail + ")"
			}
			fmt.Printf("          %s %s%s\n", claim.Kind, claim.Value, detail)
		}
	}
	fmt.Printf("\nchecked %d — %d stale, %d unknown, %d fresh, %d without claims",
		len(results), counts[verdictStale], counts[verdictUnknown], counts[verdictFresh], counts[verdictNoClaims])
	if marked > 0 {
		fmt.Printf(", %d marked", marked)
	}
	fmt.Println()
	if !useNet {
		fmt.Fprintln(os.Stderr, "note: URLs and repos were not checked — pass --net to reach the network")
	}
}

// markMemory writes (or clears) the stale flag and stamps the check time. It
// rewrites nothing else, and returns false when the file already said this.
func markMemory(cfg *Config, memoryID string, stale bool) bool {
	memoryFile, ok := findMemoryFileByID(cfg.MemoryDir, memoryID)
	if !ok {
		return false
	}
	content, err := os.ReadFile(memoryFile)
	if err != nil {
		return false
	}
	memory := parseMemory(string(content), filepath.Base(memoryFile))
	if memory.Stale == stale && !memory.Verified.IsZero() {
		return false
	}
	memory.Stale = stale
	memory.Verified = time.Now().UTC()
	if err := os.WriteFile(memoryFile, []byte(formatMemoryFile(memory)), 0644); err != nil {
		return false
	}
	return true
}
