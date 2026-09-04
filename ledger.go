package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ledgerFileName is the append-only record of governance actions, one JSON
// object per line, in the memory scope it belongs to.
const ledgerFileName = "ledger.jsonl"

func ledgerPath(cfg *Config) string {
	return filepath.Join(cfg.MemoryDir, ledgerFileName)
}

// ledgerActor names who took the action. It is advisory, not authenticated —
// the ledger records what happened, it does not prove who did it.
func ledgerActor() string {
	for _, key := range []string{"MEMGRAPH_ACTOR", "USER", "LOGNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "unknown"
}

func snapshotOf(memory Memory) *LedgerSnapshot {
	return &LedgerSnapshot{
		Name:        memory.Name,
		Description: memory.Description,
		Type:        memory.Type,
		Project:     memory.Project,
		Tags:        memory.Tags,
		Created:     memory.Created.UTC().Format(time.RFC3339),
		Content:     memory.Content,
	}
}

// appendLedger is append-only by construction: O_APPEND, never truncate,
// never rewrite. A failure to write is returned rather than swallowed —
// an action whose record was lost must not be reported as done.
func appendLedger(cfg *Config, entry LedgerEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.Actor == "" {
		entry.Actor = ledgerActor()
	}
	if err := os.MkdirAll(cfg.MemoryDir, 0755); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(ledgerPath(cfg), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// readLedger returns entries oldest-first. Malformed lines are skipped rather
// than fatal: a corrupt tail must not hide the history before it.
func readLedger(cfg *Config) ([]LedgerEntry, error) {
	file, err := os.Open(ledgerPath(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var entries []LedgerEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry LedgerEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

// parseSince accepts an RFC3339 timestamp, a date, or a relative window such
// as 7d / 24h / 30m.
func parseSince(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), true
	}
	if len(value) > 1 {
		unit := value[len(value)-1]
		if n, err := strconv.Atoi(value[:len(value)-1]); err == nil && n >= 0 {
			switch unit {
			case 'd':
				return time.Now().UTC().Add(-time.Duration(n) * 24 * time.Hour), true
			case 'h':
				return time.Now().UTC().Add(-time.Duration(n) * time.Hour), true
			case 'm':
				return time.Now().UTC().Add(-time.Duration(n) * time.Minute), true
			}
		}
	}
	return time.Time{}, false
}
