package cloudserver

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/javimosch/memgraph/internal/cloudsync"
)

// allowedFile is the same rule the client applies.
func allowedFile(name string) bool {
	return name == cloudsync.LedgerName || cloudsync.IsMemoryFile(name)
}

const (
	maxCommits = 100
	maxFiles   = 20_000
	zero       = "0000000000000000000000000000000000000000"
)

func gitOut(args ...string) ([]byte, error) { return exec.Command("git", args...).Output() }

// PreReceive enforces the push rules of docs/cloud-server.md. It runs inside git's
// quarantine, so a non-zero exit discards every uploaded object. git itself
// checks the old value atomically when it updates the ref, and checks that all
// referenced objects exist.
func PreReceive(in io.Reader) error {
	var lines []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 1 {
		return errors.New("exactly one ref update allowed")
	}
	f := strings.Fields(lines[0])
	if len(f) != 3 {
		return errors.New("bad update line")
	}
	old, tip, ref := f[0], f[1], f[2]
	if ref != "refs/heads/main" {
		return errors.New("only refs/heads/main")
	}
	if tip == zero {
		return errors.New("deletion not allowed")
	}
	if t, err := gitOut("cat-file", "-t", tip); err != nil || strings.TrimSpace(string(t)) != "commit" {
		return errors.New("tip is not a commit")
	}
	span := tip
	if old != zero {
		if _, err := gitOut("merge-base", "--is-ancestor", old, tip); err != nil {
			return errors.New("not a fast-forward")
		}
		span = old + ".." + tip
	}
	// One line per new commit: tree, then parents.
	out, err := gitOut("log", "--no-show-signature", "--format=%T %P", fmt.Sprintf("--max-count=%d", maxCommits+1), span, "--")
	if err != nil {
		return errors.New("cannot list new commits")
	}
	commits := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(commits) > maxCommits {
		return errors.New("too many commits")
	}
	if commits[0] == "" {
		return errors.New("nothing to update")
	}
	blobs := map[string]bool{}
	for _, c := range commits {
		ids := strings.Fields(c)
		if len(ids) > 2 {
			return errors.New("merge commits not allowed")
		}
		// Not recursive: a subdirectory shows up as a tree entry and is refused.
		list, err := gitOut("ls-tree", "-z", "--full-tree", ids[0])
		if err != nil {
			return errors.New("cannot read tree")
		}
		entries := bytes.Split(bytes.TrimSuffix(list, []byte{0}), []byte{0})
		if len(list) == 0 {
			entries = nil
		}
		if len(entries) > maxFiles {
			return errors.New("too many files")
		}
		prev := ""
		for _, e := range entries {
			meta, name, ok := strings.Cut(string(e), "\t")
			m := strings.Fields(meta)
			if !ok || len(m) != 3 || m[0] != "100644" || m[1] != "blob" || !allowedFile(name) {
				return errors.New("path or mode not allowed")
			}
			if prev != "" && prev >= name {
				return errors.New("tree not sorted or has duplicates")
			}
			prev = name
			blobs[m[2]] = true
		}
	}
	return checkBlobs(blobs)
}

// checkBlobs confirms every tree entry names an object that really is a blob.
func checkBlobs(blobs map[string]bool) error {
	var in strings.Builder
	for h := range blobs {
		in.WriteString(h + "\n")
	}
	cmd := exec.Command("git", "cat-file", "--batch-check=%(objecttype)")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return errors.New("cannot check objects")
	}
	for _, t := range strings.Fields(string(out)) {
		if t != "blob" {
			return errors.New("missing object or not a blob")
		}
	}
	return nil
}
