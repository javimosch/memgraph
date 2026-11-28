package cloudserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/javimosch/memgraph/internal/cloudsync"
)

func backup(t *testing.T, d Data) ([]byte, *BackupSummary) {
	t.Helper()
	var buf bytes.Buffer
	summary, err := d.Backup(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), summary
}

// client is a machine syncing project "p".
type client struct {
	t     *testing.T
	dir   string
	store cloudsync.Store
}

func newClient(t *testing.T, url string) client {
	c := client{t: t, dir: t.TempDir(), store: cloudsync.Store{Dir: t.TempDir()}}
	check(c.store.SaveLink(cloudsync.Link{Project: "p", URL: url, MemoryDir: c.dir}))
	return c
}

func (c client) sync(token string) error {
	_, err := cloudsync.Sync(c.store, cloudsync.Options{Token: token})
	return err
}

func (c client) write(name, text string) {
	check(os.WriteFile(filepath.Join(c.dir, name), []byte(text), 0644))
}

// moveTo points a linked machine at another address, as a restored server
// under the same name would be.
func (c client) moveTo(url string) {
	link := must(c.store.Link())
	link.URL = url
	check(c.store.SaveLink(link))
}

func TestBackupAndRestore(t *testing.T) {
	s := start(t)
	check(s.data.CreateProject("empty"))
	one := newClient(t, s.url)
	one.write("memory_a.md", "alpha")
	one.write("ledger.jsonl", `{"ts":"2026-01-01T00:00:00Z","memory_id":"gone"}`+"\n")
	check(one.sync(s.token))

	first, summary := backup(t, s.data)
	if len(summary.Projects) != 2 || summary.Tokens != 1 || summary.Projects[0].Head != "" || summary.Projects[1].Head != s.head().String() {
		t.Fatalf("%+v", summary)
	}
	if again, _ := backup(t, s.data); !bytes.Equal(first, again) {
		t.Fatal("unchanged data gave different bytes")
	}
	if left := must(filepath.Glob(filepath.Join(s.data.Dir, "staging-*"))); len(left) != 0 {
		t.Fatal("staging left:", left)
	}

	restored := Data{Dir: filepath.Join(t.TempDir(), "new", "data")}
	got, err := restored.Restore(bytes.NewReader(first), true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Projects, summary.Projects) || !slices.Equal(must(restored.Projects()), []string{"empty", "p"}) {
		t.Fatalf("%+v", got)
	}
	if mode := must(os.Stat(restored.tokensPath())).Mode().Perm(); mode != 0600 {
		t.Fatal(mode)
	}

	// A server on the restored data: linked machines carry on, new ones clone.
	s.stop()
	s.data, s.opts.DataDir = restored, restored.Dir
	s.boot(t)
	one.moveTo(s.url)
	one.write("memory_b.md", "bravo")
	check(one.sync(s.token))
	two := newClient(t, s.url)
	check(two.sync(s.token))
	for _, name := range []string{"memory_a.md", "memory_b.md", "ledger.jsonl"} {
		if _, err := os.Stat(filepath.Join(two.dir, name)); err != nil {
			t.Error(err)
		}
	}
	s.stop()
	fsck(t, s.repo())
}

func TestRestoreOfAnOlderBackupStopsMachines(t *testing.T) {
	s := start(t)
	one := newClient(t, s.url)
	one.write("memory_a.md", "alpha")
	check(one.sync(s.token))
	old, _ := backup(t, s.data)
	one.write("memory_later.md", "after the backup")
	check(one.sync(s.token))

	restored := Data{Dir: filepath.Join(t.TempDir(), "data")}
	must(restored.Restore(bytes.NewReader(old), true))
	s.stop()
	s.data, s.opts.DataDir = restored, restored.Dir
	s.boot(t)
	one.moveTo(s.url)
	err := one.sync(s.token)
	if err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(one.dir, "memory_later.md")); err != nil {
		t.Fatal("the newer memory was removed from the machine")
	}
}

func TestRestoreCanDropTokens(t *testing.T) {
	s := start(t)
	revokedLater, _ := backup(t, s.data)
	check(s.data.RevokeToken(s.tokenID))
	for _, keep := range []bool{true, false} {
		d := Data{Dir: filepath.Join(t.TempDir(), "data")}
		must(d.Restore(bytes.NewReader(revokedLater), keep))
		// A backup brings back tokens revoked since, unless told not to.
		if _, ok := d.authorize(s.token); ok != keep {
			t.Errorf("keep=%v: authorized=%v", keep, ok)
		}
	}
}

func TestRestoreNeverTouchesExistingData(t *testing.T) {
	s := start(t)
	good, _ := backup(t, s.data)
	dir := t.TempDir()
	check(os.WriteFile(filepath.Join(dir, "precious"), []byte("x"), 0600))
	if _, err := (Data{Dir: dir}).Restore(bytes.NewReader(good), true); err == nil {
		t.Fatal("restored over existing files")
	}
	if _, err := s.data.Restore(bytes.NewReader(good), true); err == nil {
		t.Fatal("restored over a live data dir")
	}
	if entries := must(os.ReadDir(dir)); len(entries) != 1 {
		t.Fatal(entries)
	}
	empty := t.TempDir()
	if _, err := (Data{Dir: empty}).Restore(bytes.NewReader(good), true); err != nil {
		t.Fatal("an empty directory should do:", err)
	}
}

// rewrite rebuilds a backup with its entries changed.
func rewrite(t *testing.T, data []byte, change func(h *tar.Header, body []byte) []*entry) []byte {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	w := tar.NewWriter(gz)
	r := tar.NewReader(must(gzip.NewReader(bytes.NewReader(data))))
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		check(err)
		for _, e := range change(h, must(io.ReadAll(r))) {
			e.h.Size = int64(len(e.body))
			if e.h.Typeflag == tar.TypeSymlink {
				e.h.Size = 0
				e.body = nil
			}
			check(w.WriteHeader(e.h))
			must(w.Write(e.body))
		}
	}
	check(w.Close())
	check(gz.Close())
	return out.Bytes()
}

type entry struct {
	h    *tar.Header
	body []byte
}

func TestDamagedOrHostileBackupsCreateNothing(t *testing.T) {
	s := start(t)
	check(s.data.CreateProject("q"))
	b := newBuilder()
	big := make([]byte, 200_000)
	rand.Read(big)
	c, objs := b.simple("root", big)
	s.mustPush(t, b.pack(objs), plumbing.ZeroHash, c)
	good, _ := backup(t, s.data)

	keep := func(h *tar.Header, body []byte) []*entry { return []*entry{{h, body}} }
	on := func(name string, f func(h *tar.Header, body []byte) []*entry) []byte {
		return rewrite(t, good, func(h *tar.Header, body []byte) []*entry {
			if h.Name == name {
				return f(h, body)
			}
			return keep(h, body)
		})
	}
	drop := func(*tar.Header, []byte) []*entry { return nil }
	flip := func(h *tar.Header, body []byte) []*entry {
		body[len(body)/2] ^= 1
		return keep(h, body)
	}
	extra := func(add *tar.Header) []byte {
		return on(tokensName, func(h *tar.Header, body []byte) []*entry {
			return []*entry{{add, []byte("x")}, {h, body}}
		})
	}
	flipped := bytes.Clone(good)
	flipped[len(flipped)/2] ^= 1
	cases := map[string][]byte{
		"empty":             nil,
		"not gzip":          []byte("hello"),
		"cut in half":       good[:len(good)/2],
		"last byte missing": good[:len(good)-1],
		"bit flipped":       flipped,
		"no manifest":       on(manifestName, drop),
		"no tokens":         on(tokensName, drop),
		"no bundle":         on("bundles/p.bundle", drop),
		"changed bundle":    on("bundles/p.bundle", flip),
		"changed tokens":    on(tokensName, flip),
		"path traversal":    extra(&tar.Header{Name: "../escape", Mode: 0600, Typeflag: tar.TypeReg}),
		"bundle traversal":  extra(&tar.Header{Name: "bundles/../../escape.bundle", Mode: 0600, Typeflag: tar.TypeReg}),
		"absolute path":     extra(&tar.Header{Name: "/tmp/escape", Mode: 0600, Typeflag: tar.TypeReg}),
		"symlink":           extra(&tar.Header{Name: "bundles/x.bundle", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}),
		"unlisted bundle":   extra(&tar.Header{Name: "bundles/stowaway.bundle", Mode: 0600, Typeflag: tar.TypeReg}),
		"unknown file":      extra(&tar.Header{Name: "hooks", Mode: 0700, Typeflag: tar.TypeReg}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			d := Data{Dir: filepath.Join(parent, "data")}
			if _, err := d.Restore(bytes.NewReader(data), true); err == nil {
				t.Fatal("restored")
			}
			if entries := must(os.ReadDir(parent)); len(entries) != 0 {
				t.Fatal("left behind:", entries)
			}
		})
	}
}

func TestBackupDuringPushesAndMaintenance(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 3 })
	s.pushes(t, 2)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				s.pushes(t, 1)
			}
		}
	}()
	heads := map[string]bool{}
	last := ""
	for range 8 {
		// Wait for a new push, however slow this machine is, then back up
		// while the next ones are under way.
		eventually(t, 60*time.Second, "another push", func() bool { return s.head().String() != last })
		data, summary := backup(t, s.data)
		last = summary.Projects[0].Head
		d := Data{Dir: filepath.Join(t.TempDir(), "data")}
		if _, err := d.Restore(bytes.NewReader(data), true); err != nil {
			t.Fatal(err)
		}
		heads[summary.Projects[0].Head] = true
	}
	close(stop)
	<-done
	if len(heads) != 8 {
		t.Fatal("backups did not each see new pushes:", len(heads))
	}
	t.Logf("8 backups during pushes, %d different heads, all restored", len(heads))
}
