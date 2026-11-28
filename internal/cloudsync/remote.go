package cloudsync

import (
	"bytes"
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
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp/capability"
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

// remote talks to one server repository. It drives go-git's protocol sessions
// itself: Remote.Push and Remote.Fetch walk the whole history, which would make
// every sync slower than the last and rule out pruning the local store.
type remote struct {
	repo   *git.Repository
	dir    string
	client transport.Transport
	end    *transport.Endpoint
	auth   transport.AuthMethod
}

func openRemote(store Store, link Link, token string) (*remote, error) {
	if err := CheckURL(link.URL); err != nil {
		return nil, err
	}
	hardenHTTP()
	end, err := transport.NewEndpoint(link.URL)
	if err != nil {
		return nil, err
	}
	c, err := client.NewClient(end)
	if err != nil {
		return nil, err
	}
	r := &remote{dir: store.path("repo.git"), client: c, end: end,
		auth: &githttp.BasicAuth{Username: "memgraph", Password: token}}
	return r, r.open()
}

func (r *remote) open() (err error) {
	r.repo, err = git.PlainOpen(r.dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		r.repo, err = git.PlainInit(r.dir, true)
	}
	return err
}

// head asks the server where main is; zero means the repository is empty.
func (r *remote) head() (plumbing.Hash, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	session, err := r.client.NewUploadPackSession(r.end, r.auth)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer session.Close()
	ar, err := session.AdvertisedReferencesContext(ctx)
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return plumbing.ZeroHash, nil
	}
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if h, ok := ar.References[mainRef.String()]; ok {
		return h, nil
	}
	if len(ar.References) > 0 {
		return plumbing.ZeroHash, errors.New("server repository has no main branch")
	}
	return plumbing.ZeroHash, nil
}

// fetch downloads head. have is the last synced commit, whose snapshot the
// local store holds, so the server sends only what is newer. Without it the
// server sends the whole history.
func (r *remote) fetch(head plumbing.Hash, have string) error {
	if head.IsZero() {
		return nil
	}
	if _, err := r.repo.CommitObject(head); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	session, err := r.client.NewUploadPackSession(r.end, r.auth)
	if err != nil {
		return err
	}
	defer session.Close()
	ar, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return err
	}
	req := packp.NewUploadPackRequest()
	req.Wants = []plumbing.Hash{head}
	// Claim a commit only if it is really here: after a repair it is not.
	if _, err := r.repo.CommitObject(plumbing.NewHash(have)); err != nil {
		have = ""
	}
	if have != "" {
		req.Haves = []plumbing.Hash{plumbing.NewHash(have)}
	}
	// No multi_ack (go-git cannot read it) and no thin pack.
	if ar.Capabilities.Supports(capability.OFSDelta) {
		if err := req.Capabilities.Set(capability.OFSDelta); err != nil {
			return err
		}
	}
	res, err := session.UploadPack(ctx, req)
	if err != nil {
		return err
	}
	defer res.Close()
	if err := packfile.UpdateObjectStorage(r.repo.Storer, res); err != nil {
		return err
	}
	// Reopen: the storage caches its list of packs.
	if err := r.open(); err != nil {
		return err
	}
	_, err = r.repo.CommitObject(head)
	// A server asked for an ancestor of "have" sends nothing.
	if errors.Is(err, plumbing.ErrObjectNotFound) && have != "" {
		return ErrHistoryRewritten
	}
	return err
}

// descends reports whether old is tip or one of its ancestors on main.
func (r *remote) descends(tip plumbing.Hash, old string) (bool, error) {
	for h := tip; !h.IsZero(); {
		if h.String() == old {
			return true, nil
		}
		c, err := r.repo.CommitObject(h)
		// The server sends every commit between old and a descendant of it,
		// so a gap means this history does not lead to old.
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return false, nil
		}
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

// commit writes files as one flat commit on top of parent and returns it with
// the objects the parent's snapshot does not already have.
func (r *remote) commit(parent plumbing.Hash, files Files, message string) (plumbing.Hash, []plumbing.Hash, error) {
	none := plumbing.ZeroHash
	had := map[plumbing.Hash]bool{}
	if !parent.IsZero() {
		_, entries, err := r.snapshot(parent)
		if err != nil {
			return none, nil, err
		}
		for _, e := range entries {
			had[e.Hash] = true
		}
	}
	var entries []object.TreeEntry
	var send []plumbing.Hash
	for _, name := range slices.SortedFunc(maps.Keys(files), strings.Compare) {
		h := plumbing.NewHash(BlobHash(files[name]))
		if !had[h] {
			stored, err := r.storeBlob(files[name])
			if err != nil {
				return none, nil, err
			}
			h, had[stored] = stored, true
			send = append(send, h)
		}
		entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: h})
	}
	tree, err := r.store(&object.Tree{Entries: entries})
	if err != nil {
		return none, nil, err
	}
	sig := object.Signature{Name: "memgraph", Email: "sync@memgraph.invalid", When: time.Now().UTC()}
	c := &object.Commit{Author: sig, Committer: sig, Message: message, TreeHash: tree}
	if !parent.IsZero() {
		c.ParentHashes = []plumbing.Hash{parent}
	}
	h, err := r.store(c)
	return h, append(send, tree, h), err
}

// push moves main from old to commit, sending objects. The server applies it
// only if main still is old, and commit's parent is old, so it never rewrites.
func (r *remote) push(old, commit plumbing.Hash, objects []plumbing.Hash) error {
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	session, err := r.client.NewReceivePackSession(r.end, r.auth)
	if err != nil {
		return err
	}
	defer session.Close()
	ar, err := session.AdvertisedReferencesContext(ctx)
	if err != nil {
		return err
	}
	if now := ar.References[mainRef.String()]; now != old {
		return errors.New("server moved")
	}
	req := packp.NewReferenceUpdateRequestFromCapabilities(ar.Capabilities)
	req.Commands = []*packp.Command{{Name: mainRef, Old: old, New: commit}}
	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, r.repo.Storer, false).Encode(objects, 10); err != nil {
		return err
	}
	req.Packfile = io.NopCloser(&pack)
	report, err := session.ReceivePack(ctx, req)
	if err != nil {
		return err
	}
	if report == nil {
		return errors.New("server did not report the result of the push")
	}
	return report.Error()
}
