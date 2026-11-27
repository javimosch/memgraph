package cloudsync

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/javimosch/memgraph/internal/fsx"
)

// Link ties a local memory directory to a server repository.
type Link struct {
	Project   string `json:"project"`
	URL       string `json:"url"`
	MemoryDir string `json:"memory_dir"`
}

// ConflictRecord is kept until someone marks it resolved.
type ConflictRecord struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	Copy     string    `json:"copy,omitempty"` // path of the content that lost
	Time     time.Time `json:"time"`
	Resolved bool      `json:"resolved,omitempty"`
}

// State is what this machine remembers about one link.
type State struct {
	Base      Manifest         `json:"base"`
	LastSeen  string           `json:"last_seen_commit"`
	LocalOnly map[string]bool  `json:"local_only_ids"`
	Allowed   map[string]bool  `json:"allowed_findings"`
	Conflicts []ConflictRecord `json:"conflicts"`
	LastSync  time.Time        `json:"last_sync"`
	LastError string           `json:"last_error,omitempty"`
}

// Store is the sync state directory of one link. It lives outside the memory
// directory so conflict copies never show up in recall.
type Store struct{ Dir string }

func (s Store) path(name string) string { return filepath.Join(s.Dir, name) }

// Lock keeps two syncs of the same link apart.
func (s Store) Lock() (func(), error) { return fsx.Lock(s.Dir) }

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, raw, 0600)
}

// Link reads the link. It returns fs.ErrNotExist when there is none.
func (s Store) Link() (Link, error) {
	var l Link
	return l, readJSON(s.path("link.json"), &l)
}

func (s Store) SaveLink(l Link) error { return writeJSON(s.path("link.json"), l) }

// Load returns the state, empty for a link that never synced. A damaged state
// file is an error: starting over would forget local-only memories.
func (s Store) Load() (*State, error) {
	st := &State{}
	if err := readJSON(s.path("state.json"), st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if st.Base == nil {
		st.Base = Manifest{}
	}
	if st.LocalOnly == nil {
		st.LocalOnly = map[string]bool{}
	}
	if st.Allowed == nil {
		st.Allowed = map[string]bool{}
	}
	return st, nil
}

func (s Store) Save(st *State) error { return writeJSON(s.path("state.json"), st) }

// addConflict saves the losing content and records the conflict once.
func (s Store) addConflict(st *State, c Conflict) (ConflictRecord, error) {
	rec := ConflictRecord{ID: len(st.Conflicts) + 1, Name: c.Name, Kind: c.Kind, Time: time.Now().UTC()}
	if c.Loser != nil {
		rec.Copy = s.path(filepath.Join("conflicts", c.Name+"."+BlobHash(c.Loser)[:12]))
		if err := os.MkdirAll(filepath.Dir(rec.Copy), 0700); err != nil {
			return rec, err
		}
		if err := fsx.WriteFileAtomic(rec.Copy, c.Loser, 0600); err != nil {
			return rec, err
		}
	}
	for _, old := range st.Conflicts {
		if !old.Resolved && old.Name == rec.Name && old.Kind == rec.Kind && old.Copy == rec.Copy {
			return old, nil
		}
	}
	st.Conflicts = append(st.Conflicts, rec)
	return rec, nil
}

// Open returns the conflicts nobody has resolved yet.
func (st *State) Open() []ConflictRecord {
	var out []ConflictRecord
	for _, c := range st.Conflicts {
		if !c.Resolved {
			out = append(out, c)
		}
	}
	return out
}

// Pending lists memory files that differ from the base, without any network.
// Local-only memories are not pending: they are never sent.
func Pending(dir string, st *State) ([]string, error) {
	local, _, err := readLocal(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for name, data := range local {
		if st.Base[name] != BlobHash(data) && !st.LocalOnly[MemoryID(name)] && !IsLocalOnly(data) {
			out = append(out, name)
		}
	}
	for name := range st.Base {
		if _, ok := local[name]; !ok && !st.LocalOnly[MemoryID(name)] {
			out = append(out, name)
		}
	}
	return out, nil
}
