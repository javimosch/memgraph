package cloudsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

const (
	mainRef      = plumbing.ReferenceName("refs/heads/main")
	maxFileBytes = 16 << 20
	netTimeout   = 90 * time.Second
)

// CheckURL accepts only HTTPS, or plain HTTP to this machine. Other
// transports are refused: go-git serves file paths with an in-process server
// that accepts stale pushes.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("bad server URL: %w", err)
	}
	if u.User != nil {
		return errors.New("put no credentials in the URL; use 'memgraph cloud login'")
	}
	if u.Scheme == "https" && u.Host != "" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("server URL must use https (http is allowed for localhost only)")
}

var installOnce sync.Once

// A redirect could carry the token elsewhere, so none is followed.
func hardenHTTP() {
	installOnce.Do(func() {
		c := githttp.NewClient(&http.Client{
			Timeout:       netTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
		})
		client.InstallProtocol("https", c)
		client.InstallProtocol("http", c)
	})
}

type remote struct {
	repo *git.Repository
	r    *git.Remote
	auth transport.AuthMethod
}

func openRemote(store Store, link Link, token string) (*remote, error) {
	if err := CheckURL(link.URL); err != nil {
		return nil, err
	}
	hardenHTTP()
	dir := store.path("repo.git")
	repo, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInit(dir, true)
	}
	if err != nil {
		return nil, err
	}
	cfg := &config.RemoteConfig{Name: "origin", URLs: []string{link.URL}}
	return &remote{repo: repo, r: git.NewRemote(repo.Storer, cfg),
		auth: &githttp.BasicAuth{Username: "memgraph", Password: token}}, nil
}

// head asks the server where main is; zero means the repository is empty.
func (r *remote) head() (plumbing.Hash, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	refs, err := r.r.ListContext(ctx, &git.ListOptions{Auth: r.auth})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return plumbing.ZeroHash, nil
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}
	for _, ref := range refs {
		if ref.Name() == mainRef {
			return ref.Hash(), nil
		}
	}
	if len(refs) > 0 {
		return plumbing.ZeroHash, errors.New("server repository has no main branch")
	}
	return plumbing.ZeroHash, nil
}

func (r *remote) fetch(h plumbing.Hash) error {
	if h.IsZero() {
		return nil
	}
	if _, err := r.repo.CommitObject(h); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	err := r.r.FetchContext(ctx, &git.FetchOptions{Auth: r.auth, Tags: git.NoTags,
		RefSpecs: []config.RefSpec{"+refs/heads/main:refs/remotes/origin/main"}})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return err
	}
	_, err = r.repo.CommitObject(h)
	return err
}

// descends reports whether old is tip or one of its ancestors on main.
func (r *remote) descends(tip plumbing.Hash, old string) (bool, error) {
	for h := tip; !h.IsZero(); {
		if h.String() == old {
			return true, nil
		}
		c, err := r.repo.CommitObject(h)
		if err != nil {
			return false, err
		}
		h = plumbing.ZeroHash
		if len(c.ParentHashes) > 0 {
			h = c.ParentHashes[0]
		}
	}
	return false, nil
}

// read returns the memory files and ledger of a commit. The server is not
// trusted: any other path or file type stops the sync before a byte is written.
func (r *remote) read(h plumbing.Hash) (Files, []byte, error) {
	files := Files{}
	if h.IsZero() {
		return files, nil, nil
	}
	c, err := r.repo.CommitObject(h)
	if err != nil {
		return nil, nil, err
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, nil, err
	}
	var ledger []byte
	for _, e := range tree.Entries {
		if e.Mode != filemode.Regular || (e.Name != LedgerName && !IsMemoryFile(e.Name)) {
			return nil, nil, fmt.Errorf("server holds a path sync does not allow: %q", e.Name)
		}
		blob, err := r.repo.BlobObject(e.Hash)
		if err != nil {
			return nil, nil, err
		}
		if blob.Size > maxFileBytes {
			return nil, nil, fmt.Errorf("server file %s is too large", e.Name)
		}
		rd, err := blob.Reader()
		if err != nil {
			return nil, nil, err
		}
		data, err := io.ReadAll(rd)
		rd.Close()
		if err != nil {
			return nil, nil, err
		}
		if e.Name == LedgerName {
			ledger = data
		} else if _, dup := files[e.Name]; dup {
			return nil, nil, fmt.Errorf("server tree lists %s twice", e.Name)
		} else {
			files[e.Name] = data
		}
	}
	return files, ledger, nil
}

func (r *remote) store(obj interface {
	Encode(plumbing.EncodedObject) error
}) (plumbing.Hash, error) {
	o := r.repo.Storer.NewEncodedObject()
	if err := obj.Encode(o); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.repo.Storer.SetEncodedObject(o)
}

func (r *remote) storeBlob(data []byte) (plumbing.Hash, error) {
	o := (&memory.Storage{}).NewEncodedObject()
	o.SetType(plumbing.BlobObject)
	w, err := o.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(data); err != nil {
		return plumbing.ZeroHash, err
	}
	return r.repo.Storer.SetEncodedObject(o)
}

// commit writes files as one flat commit on top of parent.
func (r *remote) commit(parent plumbing.Hash, files Files, message string) (plumbing.Hash, error) {
	var entries []object.TreeEntry
	for _, name := range slices.SortedFunc(maps.Keys(files), strings.Compare) {
		h, err := r.storeBlob(files[name])
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: h})
	}
	tree, err := r.store(&object.Tree{Entries: entries})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	sig := object.Signature{Name: "memgraph", Email: "sync@memgraph.invalid", When: time.Now().UTC()}
	c := &object.Commit{Author: sig, Committer: sig, Message: message, TreeHash: tree}
	if !parent.IsZero() {
		c.ParentHashes = []plumbing.Hash{parent}
	}
	return r.store(c)
}

// push sends commit to main. It never forces.
func (r *remote) push(commit plumbing.Hash) error {
	if err := r.repo.Storer.SetReference(plumbing.NewHashReference(mainRef, commit)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	return r.r.PushContext(ctx, &git.PushOptions{RemoteName: "origin", Auth: r.auth,
		RefSpecs: []config.RefSpec{"refs/heads/main:refs/heads/main"}})
}
