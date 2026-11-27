package cloudserver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/storage/memory"
)

// selfCheckProject cannot be a user's project: names never start with "-", so
// no token can name it and no request can reach it.
const selfCheckProject = "-selfcheck"

// verifyHook confirms the push hook is still the one the server installed.
// git accepts every push when the hook is missing or not executable.
func (s *Server) verifyHook() error {
	info, err := os.Lstat(s.hookPath)
	if err != nil {
		return fmt.Errorf("push hook: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
		return errors.New("push hook is not an executable file")
	}
	data, err := os.ReadFile(s.hookPath)
	if err != nil {
		return fmt.Errorf("push hook: %w", err)
	}
	if string(data) != s.hookScript {
		return errors.New("push hook was changed")
	}
	return nil
}

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

// canaryPush sends a one-file commit on top of parent through the same git
// setup real pushes use, and returns git's report.
func (s *Server) canaryPush(parent plumbing.Hash, file string, stderr io.Writer) (plumbing.Hash, string, error) {
	none := plumbing.ZeroHash
	st := memory.NewStorage()
	var objects []plumbing.Hash
	put := func(o plumbing.EncodedObject) (plumbing.Hash, error) {
		h, err := st.SetEncodedObject(o)
		objects = append(objects, h)
		return h, err
	}
	blob := st.NewEncodedObject()
	blob.SetType(plumbing.BlobObject)
	w, err := blob.Writer()
	if err != nil {
		return none, "", err
	}
	if _, err := io.WriteString(w, "self-check "+file+"\n"); err != nil {
		return none, "", err
	}
	blobHash, err := put(blob)
	if err != nil {
		return none, "", err
	}
	treeObject := st.NewEncodedObject()
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: file, Mode: filemode.Regular, Hash: blobHash}}}
	if err := tree.Encode(treeObject); err != nil {
		return none, "", err
	}
	treeHash, err := put(treeObject)
	if err != nil {
		return none, "", err
	}
	sig := object.Signature{Name: "memgraph", Email: "selfcheck@memgraph.invalid", When: time.Now()}
	commit := &object.Commit{Author: sig, Committer: sig, Message: "self-check", TreeHash: treeHash}
	if !parent.IsZero() {
		commit.ParentHashes = []plumbing.Hash{parent}
	}
	commitObject := st.NewEncodedObject()
	if err := commit.Encode(commitObject); err != nil {
		return none, "", err
	}
	tip, err := put(commitObject)
	if err != nil {
		return none, "", err
	}

	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, st, false).Encode(objects, 0); err != nil {
		return none, "", err
	}
	update := packp.NewReferenceUpdateRequest()
	if err := update.Capabilities.Set("report-status"); err != nil {
		return none, "", err
	}
	update.Commands = []*packp.Command{{Name: "refs/heads/main", Old: parent, New: tip}}
	update.Packfile = io.NopCloser(&pack)
	var body bytes.Buffer
	if err := update.Encode(&body); err != nil {
		return none, "", err
	}

	f, err := os.CreateTemp(filepath.Join(s.data.Dir, "tmp"), "check-")
	if err != nil {
		return none, "", err
	}
	defer f.Close()
	defer os.Remove(f.Name())
	size, err := io.Copy(f, &body)
	if err != nil {
		return none, "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return none, "", err
	}
	req, err := http.NewRequest(http.MethodPost, "http://selfcheck/"+selfCheckProject+".git/git-receive-pack", nil)
	if err != nil {
		return none, "", err
	}
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	rec := &recorder{header: http.Header{}}
	s.backend(rec, req, f, size, stderr)
	return tip, rec.body.String(), nil
}

// selfCheck proves, before serving, that a good push gets through and a bad
// one is refused by the hook.
func (s *Server) selfCheck() (err error) {
	var output bytes.Buffer
	defer func() {
		if said := strings.TrimSpace(output.String()); err != nil && said != "" {
			err = fmt.Errorf("%w\ngit said: %s", err, said)
		}
	}()
	repo := s.data.repo(selfCheckProject)
	if err := os.RemoveAll(repo); err != nil {
		return err
	}
	if err := s.data.initRepo(repo); err != nil {
		return err
	}
	defer os.RemoveAll(repo)
	if err := s.verifyHook(); err != nil {
		return err
	}
	good, report, err := s.canaryPush(plumbing.ZeroHash, "memory_selfcheck.md", &output)
	if err != nil {
		return err
	}
	if !strings.Contains(report, "ok refs/heads/main") {
		return fmt.Errorf("a valid push was refused; check that git works and the hook can run: %q", report)
	}
	_, report, err = s.canaryPush(good, "not-allowed.sh", &output)
	if err != nil {
		return err
	}
	if !strings.Contains(report, "ng refs/heads/main pre-receive hook declined") {
		return fmt.Errorf("push rules are not enforced: a forbidden push was not refused by the hook: %q", report)
	}
	return nil
}
