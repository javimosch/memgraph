// Package cloudsync syncs one memory directory with one git branch.
// See docs/cloud-sync.md for the rules.
package cloudsync

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// LedgerName is merged by lines instead of by the three-way file rules.
const LedgerName = "ledger.jsonl"

var memoryName = regexp.MustCompile(`^memory_[A-Za-z0-9._-]+\.md$`)

// IsMemoryFile reports whether name is a memory file that sync may handle.
func IsMemoryFile(name string) bool { return memoryName.MatchString(name) }

// MemoryID is the ID memgraph derives from a memory filename.
func MemoryID(name string) string {
	return strings.TrimPrefix(strings.TrimSuffix(name, ".md"), "memory_")
}

// Files maps a filename to its content. A missing key means the file is absent.
type Files map[string][]byte

// Manifest maps a filename to the git blob hash this machine and the server
// last agreed on. It is the base of the three-way merge.
type Manifest map[string]string

// BlobHash is the git object ID of data.
func BlobHash(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func hashOf(f Files, name string) string {
	if data, ok := f[name]; ok {
		return BlobHash(data)
	}
	return ""
}

// Conflict kinds.
const (
	BothChanged  = "both-changed"  // server version stays active, local is copied aside
	EditedDelete = "edit-restored" // an edit beat a delete; nothing was lost
	Resurrection = "deleted-elsewhere"
)

// Conflict records a merge that a person or agent should look at.
type Conflict struct {
	Name string
	Kind string
	// Loser is the content that did not become active, nil if none was lost.
	Loser []byte
}

// MergeFiles applies the rules of docs/cloud-sync.md to memory files and
// returns what both sides should hold. "" below means absent.
func MergeFiles(base Manifest, local, remote Files) (Files, []Conflict) {
	names := map[string]bool{}
	for n := range base {
		names[n] = true
	}
	for n := range local {
		names[n] = true
	}
	for n := range remote {
		names[n] = true
	}
	merged := Files{}
	var conflicts []Conflict
	take := func(name string, from Files) {
		if data, ok := from[name]; ok {
			merged[name] = data
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		b, l, r := base[name], hashOf(local, name), hashOf(remote, name)
		switch {
		case l == r: // same on both sides, including both deleted
			take(name, local)
		case b == "" && r == "": // new here
			take(name, local)
		case b == "" && l == "": // new there
			take(name, remote)
		case b != "" && l == b: // only the server changed or deleted it
			take(name, remote)
		case b != "" && r == b: // only this machine changed or deleted it
			take(name, local)
		case b != "" && r == "": // edited here, deleted there
			take(name, local)
			conflicts = append(conflicts, Conflict{Name: name, Kind: EditedDelete})
		case b != "" && l == "": // deleted here, edited there
			take(name, remote)
			conflicts = append(conflicts, Conflict{Name: name, Kind: EditedDelete})
		default:
			take(name, remote)
			conflicts = append(conflicts, Conflict{Name: name, Kind: BothChanged, Loser: local[name]})
		}
	}
	return merged, conflicts
}

// LedgerEntry holds the ledger fields sync needs.
type LedgerEntry struct {
	TS       string `json:"ts"`
	Action   string `json:"action"`
	MemoryID string `json:"memory_id"`
	Snapshot *struct {
		Content string `json:"content"`
		Local   bool   `json:"local"`
	} `json:"snapshot"`
}

func ledgerLines(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseLine(line string) (e LedgerEntry, ok bool) {
	return e, json.Unmarshal([]byte(line), &e) == nil
}

// MergeLedger is the union of both ledgers: exact duplicate lines removed,
// stably ordered by timestamp. No line is ever dropped. A line that does not
// parse keeps its place after the line before it.
func MergeLedger(a, b []byte) []byte {
	type row struct{ ts, line string }
	seen := map[string]bool{}
	var rows []row
	for _, src := range [][]byte{a, b} {
		last := ""
		for _, line := range ledgerLines(src) {
			if e, ok := parseLine(line); ok && e.TS != "" {
				last = e.TS
			}
			if !seen[line] {
				seen[line] = true
				rows = append(rows, row{last, line})
			}
		}
	}
	slices.SortStableFunc(rows, func(x, y row) int { return strings.Compare(x.ts, y.ts) })
	var out bytes.Buffer
	for _, r := range rows {
		out.WriteString(r.line + "\n")
	}
	return out.Bytes()
}

// ActionWithhold marks the ledger history of a deleted memory as private. It
// does not make the memory itself local-only, so its deletion still syncs.
const ActionWithhold = "withhold"

// PublicLedger drops lines that carry a local-only memory, and every line,
// marker included, of a memory whose history is withheld. A line that does not
// parse cannot be shown to be safe, so it is dropped too.
func PublicLedger(data []byte, localIDs map[string]bool) []byte {
	lines := ledgerLines(data)
	withheld := map[string]bool{}
	for _, line := range lines {
		if e, ok := parseLine(line); ok && e.Action == ActionWithhold {
			withheld[e.MemoryID] = true
		}
	}
	var out bytes.Buffer
	for _, line := range lines {
		e, ok := parseLine(line)
		if !ok || localIDs[e.MemoryID] || withheld[e.MemoryID] || (e.Snapshot != nil && e.Snapshot.Local) {
			continue
		}
		out.WriteString(line + "\n")
	}
	return out.Bytes()
}

// IsLocalOnly reports whether a memory's frontmatter has "local: true". It
// reads the frontmatter the way memgraph's parser does.
func IsLocalOnly(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return false
	}
	for _, line := range lines[1:] {
		if line == "---" {
			return false
		}
		if v, ok := strings.CutPrefix(line, "local:"); ok && strings.TrimSpace(v) == "true" {
			return true
		}
	}
	return false
}
