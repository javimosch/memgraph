package cloudsync

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/javimosch/memgraph/internal/fsx"
)

// Options configure one sync.
type Options struct {
	Token           string
	AllowMassDelete bool
	// DryRun computes what would change and writes nothing to either side.
	DryRun bool
	// Content returns the body of a memory file, as the ledger records it.
	Content func(name string, data []byte) string

	beforePush  func() // test hooks
	beforeApply func() error
}

// Result says what a sync did, or with DryRun what it would do.
type Result struct {
	Uploaded      []string         `json:"uploaded"`
	DeletedRemote []string         `json:"deleted_on_server"`
	Downloaded    []string         `json:"downloaded"`
	DeletedLocal  []string         `json:"deleted_locally"`
	Conflicts     []ConflictRecord `json:"new_conflicts"`
	// Held files are local-only here but already on the server.
	Held []string `json:"held"`
	// Deferred files changed during the sync and wait for the next one.
	Deferred []string `json:"deferred"`
	Ignored  []string `json:"ignored"`
	Commit   string   `json:"commit,omitempty"`
}

// BlockedError means a guard stopped the sync before anything changed.
type BlockedError struct {
	Reason   string
	Findings []Finding
}

func (e *BlockedError) Error() string { return "sync blocked: " + e.Reason }

// ErrHistoryRewritten stops a sync against a restored or rewritten server.
var ErrHistoryRewritten = &BlockedError{Reason: "server history no longer contains the last synced commit " +
	"(restored backup or rewrite); disable this project and join it again to relink"}

const attempts = 4

// readLocal returns the memory files in dir, and names that look like memories
// but sync cannot carry.
func readLocal(dir string) (Files, []string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Files{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	files := Files{}
	var ignored []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "memory_") || !strings.HasSuffix(name, ".md") {
			continue
		}
		if !IsMemoryFile(name) {
			ignored = append(ignored, name)
			continue
		}
		// Skipping it would read as a deletion, so fail instead.
		if !e.Type().IsRegular() {
			return nil, nil, fmt.Errorf("%s is not a regular file", filepath.Join(dir, name))
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		if len(data) > maxFileBytes {
			return nil, nil, &BlockedError{Reason: name + " is larger than the 16 MB limit"}
		}
		files[name] = data
	}
	return files, ignored, nil
}

func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// plan is one attempt's view of both sides and what to do about it.
type plan struct {
	local, merged   Files // without held and local-only files
	push            Files // the server tree to have, ledger included
	ledger          []byte
	changed         bool
	conflicts       []Conflict
	remoteWasLedger []byte
	res             Result
}

// Sync runs one cycle for the link in store.
func Sync(store Store, opts Options) (*Result, error) {
	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	link, err := store.Link()
	if err != nil {
		return nil, fmt.Errorf("not linked: %w", err)
	}
	st, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("cannot read sync state: %w", err)
	}
	res, err := run(store, link, st, opts)
	if opts.DryRun {
		return res, err
	}
	st.LastError = ""
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastSync = time.Now().UTC()
	}
	if serr := store.Save(st); err == nil {
		err = serr
	}
	return res, err
}

func run(store Store, link Link, st *State, opts Options) (*Result, error) {
	rem, err := openRemote(store, link, opts.Token)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		head, err := rem.head()
		if err != nil {
			return nil, err
		}
		if err := rem.fetch(head); err != nil {
			return nil, err
		}
		if st.LastSeen != "" {
			ok, err := rem.descends(head, st.LastSeen)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ErrHistoryRewritten
			}
		}
		p, err := makePlan(rem, head, link, st, opts)
		if err != nil || opts.DryRun {
			if p == nil {
				return nil, err
			}
			return &p.res, err
		}
		// Local-only IDs are remembered even if the rest of the sync fails.
		if err := store.Save(st); err != nil {
			return nil, err
		}
		newHead := head
		if p.changed {
			c, err := rem.commit(head, p.push, "memgraph sync")
			if err != nil {
				return nil, err
			}
			if opts.beforePush != nil {
				opts.beforePush()
			}
			if err := rem.push(c); err != nil {
				// Retry only if the server moved under us.
				now, herr := rem.head()
				if herr == nil && now != head && attempt < attempts {
					continue
				}
				return nil, fmt.Errorf("push failed: %w", err)
			}
			newHead, p.res.Commit = c, c.String()
		}
		// Save what lost before any local file is replaced.
		for _, c := range p.conflicts {
			rec, err := store.addConflict(st, c)
			if err != nil {
				return nil, err
			}
			p.res.Conflicts = append(p.res.Conflicts, rec)
		}
		if err := store.Save(st); err != nil {
			return nil, err
		}
		if opts.beforeApply != nil {
			if err := opts.beforeApply(); err != nil {
				return nil, err
			}
		}
		if err := apply(link.MemoryDir, st, p); err != nil {
			return nil, err
		}
		if !newHead.IsZero() {
			st.LastSeen = newHead.String()
		}
		return &p.res, nil
	}
}

func makePlan(rem *remote, head plumbing.Hash, link Link, st *State, opts Options) (*plan, error) {
	remoteFiles, remoteLedger, err := rem.read(head)
	if err != nil {
		return nil, err
	}
	// A consistent snapshot: writers change the ledger and a file together.
	unlock, err := fsx.Lock(link.MemoryDir)
	if err != nil {
		return nil, err
	}
	local, ignored, err := readLocal(link.MemoryDir)
	var ledger []byte
	if err == nil {
		ledger, err = readOptional(filepath.Join(link.MemoryDir, LedgerName))
	}
	unlock()
	if err != nil {
		return nil, err
	}
	if len(st.Base) > 0 && len(local) == 0 {
		return nil, &BlockedError{Reason: "the memory directory is missing or has no memories, " +
			"but it did before: " + link.MemoryDir}
	}

	// Local-only is sticky: an older binary rewriting a file drops the flag.
	for name, data := range local {
		if IsLocalOnly(data) {
			st.LocalOnly[MemoryID(name)] = true
		}
	}
	for _, line := range ledgerLines(ledger) {
		if e, ok := parseLine(line); ok && e.Snapshot != nil && e.Snapshot.Local {
			st.LocalOnly[e.MemoryID] = true
		}
	}

	p := &plan{local: Files{}, res: Result{Ignored: ignored}}
	shared, base, held := Files{}, Manifest{}, Files{}
	private := func(name string) bool { return st.LocalOnly[MemoryID(name)] }
	for name, data := range local {
		if !private(name) {
			p.local[name] = data
		}
	}
	for name, data := range remoteFiles {
		if private(name) {
			held[name] = data // neither pushed nor pulled
		} else {
			shared[name] = data
		}
	}
	for name, h := range st.Base {
		if !private(name) {
			base[name] = h
		}
	}
	p.res.Held = slices.Sorted(maps.Keys(held))
	p.merged, p.conflicts = MergeFiles(base, p.local, shared)

	// First link: an old copy must not bring back a deleted memory.
	if st.LastSeen == "" && len(st.Base) == 0 && opts.Content != nil {
		deleted := map[string]string{}
		for _, line := range ledgerLines(remoteLedger) {
			if e, ok := parseLine(line); ok && e.Action == "delete" && e.Snapshot != nil {
				deleted[e.MemoryID] = strings.Trim(e.Snapshot.Content, "\n")
			}
		}
		for _, name := range slices.Sorted(maps.Keys(p.local)) {
			if _, onServer := shared[name]; onServer {
				continue
			}
			was, ok := deleted[MemoryID(name)]
			if ok && was == strings.Trim(opts.Content(name, p.local[name]), "\n") {
				delete(p.merged, name)
				p.conflicts = append(p.conflicts, Conflict{Name: name, Kind: Resurrection, Loser: p.local[name]})
			}
		}
	}

	p.ledger = MergeLedger(ledger, remoteLedger)
	public := MergeLedger(remoteLedger, PublicLedger(ledger, st.LocalOnly))
	p.push = maps.Clone(p.merged)
	maps.Copy(p.push, held)

	for _, name := range slices.Sorted(maps.Keys(p.push)) {
		if hashOf(p.push, name) != hashOf(remoteFiles, name) {
			p.res.Uploaded = append(p.res.Uploaded, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(remoteFiles)) {
		if _, ok := p.push[name]; !ok {
			p.res.DeletedRemote = append(p.res.DeletedRemote, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.merged)) {
		if hashOf(p.merged, name) != hashOf(p.local, name) {
			p.res.Downloaded = append(p.res.Downloaded, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(p.local)) {
		if _, ok := p.merged[name]; !ok {
			p.res.DeletedLocal = append(p.res.DeletedLocal, name)
		}
	}
	ledgerChanged := !bytes.Equal(public, remoteLedger)
	p.changed = ledgerChanged || len(p.res.Uploaded)+len(p.res.DeletedRemote) > 0
	if len(public) > 0 {
		p.push[LedgerName] = public
	}

	limit := max(10, max(len(st.Base), len(remoteFiles))/10)
	if n := max(len(p.res.DeletedRemote), len(p.res.DeletedLocal)); n > limit && !opts.AllowMassDelete {
		return p, &BlockedError{Reason: fmt.Sprintf("it would delete %d memories (limit %d); "+
			"if that is intended, run again with --allow-mass-delete", n, limit)}
	}
	var findings []Finding
	for _, name := range p.res.Uploaded {
		findings = append(findings, Scan(name, remoteFiles[name], p.push[name], st.Allowed)...)
	}
	findings = append(findings, Scan(LedgerName, remoteLedger, public, st.Allowed)...)
	if len(findings) > 0 {
		return p, &BlockedError{Reason: fmt.Sprintf("%d possible secret(s) in what would be uploaded", len(findings)), Findings: findings}
	}
	return p, nil
}

// apply writes the merge result to the memory directory. A file that changed
// since the snapshot is left alone and keeps its old base, so the next sync
// merges it. A file that needs no write gets the new base even if it changed:
// its newer content is an edit of what the server now holds.
func apply(dir string, st *State, p *plan) error {
	unlock, err := fsx.Lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	names := map[string]bool{}
	for _, m := range []Files{p.local, p.merged} {
		for n := range m {
			names[n] = true
		}
	}
	for n := range st.Base {
		if !st.LocalOnly[MemoryID(n)] {
			names[n] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		want, was := hashOf(p.merged, name), hashOf(p.local, name)
		if want != was {
			path := filepath.Join(dir, name)
			now, err := os.ReadFile(path)
			absent := errors.Is(err, fs.ErrNotExist)
			if err != nil && !absent {
				return err
			}
			if (was == "" && !absent) || (was != "" && (absent || BlobHash(now) != was)) {
				p.res.Deferred = append(p.res.Deferred, name)
				continue
			}
			if want == "" {
				err = os.Remove(path)
			} else {
				err = fsx.WriteFileAtomic(path, p.merged[name], 0644)
			}
			if err != nil {
				return err
			}
		}
		if want == "" {
			delete(st.Base, name)
		} else {
			st.Base[name] = want
		}
	}
	path := filepath.Join(dir, LedgerName)
	current, err := readOptional(path)
	if err != nil {
		return err
	}
	if merged := MergeLedger(current, p.ledger); !bytes.Equal(merged, current) {
		return fsx.WriteFileAtomic(path, merged, 0644)
	}
	return nil
}
