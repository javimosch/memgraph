package cloudserver

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/javimosch/memgraph/internal/cloudsync"
)

const mainRef = plumbing.ReferenceName("refs/heads/main")

// hookArg makes the test binary act as the push hook the server installs.
const hookArg = "memgraph-test-hook"

// limitArg makes the test binary act as the command that bounds git.
const limitArg = "memgraph-test-limit"

// testLimit is the limit command of the tests. With -race, a Go program waits a
// second before it exits, and this one runs for every git.
func testLimit(self string) []string {
	return []string{"/usr/bin/env", "GORACE=atexit_sleep_ms=0", self, limitArg}
}

func TestMain(m *testing.M) {
	if dir := os.Getenv(serveEnv); dir != "" {
		serveForCrashTest(dir)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == limitArg {
		os.Exit(Limit(os.Args[2:]))
	}
	if len(os.Args) == 2 && os.Args[1] == hookArg {
		if err := PreReceive(os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "memgraph:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

type srv struct {
	data       Data
	url, token string
	tokenID    string
	opts       Options
	server     *Server
	http       *httptest.Server
}

// start runs a server with one project, "p", and a token for it.
func start(t *testing.T, change ...func(*Options)) *srv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("the server needs git")
	}
	s := &srv{data: Data{Dir: filepath.Join(t.TempDir(), "data")}}
	check(s.data.CreateProject("p"))
	secret, tok, err := s.data.CreateToken("test", []string{"p"})
	check(err)
	s.token, s.tokenID = secret, tok.ID
	self := must(os.Executable())
	s.opts = Options{DataDir: s.data.Dir, Limits: DefaultLimits, HookCommand: []string{self, hookArg}, LimitCommand: testLimit(self)}
	for _, c := range change {
		c(&s.opts)
	}
	s.boot(t)
	t.Cleanup(s.stop)
	return s
}

func (s *srv) boot(t *testing.T) {
	t.Helper()
	s.server = must(New(s.opts))
	s.http = httptest.NewUnstartedServer(s.server)
	s.http.Config = s.server.HTTPServer()
	s.http.Start()
	s.url = s.http.URL + "/p.git"
}

func (s *srv) stop() {
	if s.server != nil {
		s.http.Close()
		s.server.Close()
		s.server = nil
	}
}

func (s *srv) repo() string { return s.data.repo("p") }

func (s *srv) head() plumbing.Hash {
	out, err := gitTry(s.repo(), "-c", "safe.directory=*", "rev-parse", "--verify", "-q", "refs/heads/main")
	if err != nil {
		return plumbing.ZeroHash
	}
	return plumbing.NewHash(strings.TrimSpace(out))
}

// ---- hand-built objects, so tests can send things no normal client would ----

type builder struct{ st *memory.Storage }

func newBuilder() *builder { return &builder{memory.NewStorage()} }

func (b *builder) put(o plumbing.EncodedObject) plumbing.Hash {
	return must(b.st.SetEncodedObject(o))
}

func (b *builder) blob(data []byte) plumbing.Hash {
	o := b.st.NewEncodedObject()
	o.SetType(plumbing.BlobObject)
	w := must(o.Writer())
	must(w.Write(data))
	return b.put(o)
}

func (b *builder) tree(entries ...object.TreeEntry) plumbing.Hash {
	o := b.st.NewEncodedObject()
	check((&object.Tree{Entries: entries}).Encode(o))
	return b.put(o)
}

func (b *builder) commit(tree plumbing.Hash, msg string, parents ...plumbing.Hash) plumbing.Hash {
	sig := object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1700000000, 0)}
	o := b.st.NewEncodedObject()
	check((&object.Commit{Author: sig, Committer: sig, Message: msg, TreeHash: tree, ParentHashes: parents}).Encode(o))
	return b.put(o)
}

func file(name string, h plumbing.Hash) object.TreeEntry {
	return object.TreeEntry{Name: name, Mode: filemode.Regular, Hash: h}
}

// simple builds a one-file commit and returns it with the objects to send.
func (b *builder) simple(msg string, data []byte, parents ...plumbing.Hash) (plumbing.Hash, []plumbing.Hash) {
	bl := b.blob(data)
	tr := b.tree(file("memory_a.md", bl))
	c := b.commit(tr, msg, parents...)
	return c, []plumbing.Hash{c, tr, bl}
}

func (b *builder) pack(objs []plumbing.Hash) []byte {
	var buf bytes.Buffer
	must(packfile.NewEncoder(&buf, b.st, false).Encode(objs, 0))
	return buf.Bytes()
}

func cmdOf(old, new plumbing.Hash, name string) *packp.Command {
	return &packp.Command{Old: old, New: new, Name: plumbing.ReferenceName(name)}
}

func requestBody(pack []byte, cmds ...*packp.Command) []byte {
	req := packp.NewReferenceUpdateRequest()
	check(req.Capabilities.Set("report-status"))
	req.Commands = cmds
	if pack != nil {
		req.Packfile = io.NopCloser(bytes.NewReader(pack))
	}
	var buf bytes.Buffer
	check(req.Encode(&buf))
	return buf.Bytes()
}

var httpc = &http.Client{Timeout: 90 * time.Second}

// send returns the HTTP status and the report ("ok" lines or reason).
func (s *srv) send(body []byte, pass string) (int, string, error) {
	req := must(http.NewRequest("POST", s.url+"/git-receive-pack", bytes.NewReader(body)))
	req.SetBasicAuth("x", pass)
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), err
}

func accepted(code int, out string) bool {
	return code == 200 && strings.Contains(out, "unpack ok") &&
		strings.Contains(out, "ok refs/") && !strings.Contains(out, "ng ")
}

func (s *srv) mustPush(t testing.TB, pack []byte, old, new plumbing.Hash) {
	t.Helper()
	code, out, err := s.send(requestBody(pack, cmdOf(old, new, mainRef.String())), s.token)
	if err != nil || !accepted(code, out) {
		t.Fatalf("push refused: %d %q %v", code, out, err)
	}
}

// ---- official git, isolated from the user's configuration ----

func gitEnv(home string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
}

func gitTry(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir, c.Env = dir, gitEnv(os.TempDir()+"/nohome")
	out, err := c.CombinedOutput()
	return string(out), err
}

func gitRun(t testing.TB, dir string, args ...string) string {
	out, err := gitTry(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func fsck(t testing.TB, repo string) {
	gitRun(t, repo, "-c", "safe.directory=*", "fsck", "--full", "--strict")
}

func TestRejections(t *testing.T) {
	s := start(t)
	b := newBuilder()
	root, ro := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(ro), plumbing.ZeroHash, root)
	cur, co := b.simple("second", []byte("second"), root)
	s.mustPush(t, b.pack(co), root, cur)
	main := mainRef.String()
	good, goodObjs := b.simple("good", []byte("good"), cur)
	bl := b.blob([]byte("v"))

	try := func(name string, body []byte) {
		t.Run(name, func(t *testing.T) {
			code, out, err := s.send(body, s.token)
			if err != nil {
				t.Fatal(err)
			}
			got := accepted(code, out)
			moved := s.head() != cur
			refs := gitRun(t, s.repo(), "for-each-ref", "--format=%(refname)")
			if got || moved || refs != "refs/heads/main" {
				// Put the repo back so the other cases stay independent.
				gitRun(t, s.repo(), "update-ref", mainRef.String(), cur.String())
				for _, r := range strings.Fields(refs) {
					if r != mainRef.String() {
						gitRun(t, s.repo(), "update-ref", "-d", r)
					}
				}
				t.Fatalf("ACCEPTED=%v moved=%v refs=%q: %q", got, moved, refs, out)
			}
		})
	}
	update := func(tip plumbing.Hash, objs ...plumbing.Hash) []byte {
		return requestBody(b.pack(objs), cmdOf(cur, tip, main))
	}
	withTree := func(entries ...object.TreeEntry) []byte {
		tr := b.tree(entries...)
		c := b.commit(tr, "bad", cur)
		return update(c, c, tr, bl)
	}

	stale, so := b.simple("stale", []byte("stale"), root)
	try("stale old", requestBody(b.pack(so), cmdOf(root, stale, main)))
	try("non-descendant with correct old", update(stale, so...))
	orphan, oo := b.simple("orphan", []byte("orphan"))
	try("unrelated history", update(orphan, oo...))
	try("rewind to ancestor", requestBody(b.pack(nil), cmdOf(cur, root, main)))
	try("delete main", requestBody(nil, cmdOf(cur, plumbing.ZeroHash, main)))
	try("other branch", requestBody(b.pack(goodObjs), cmdOf(plumbing.ZeroHash, good, "refs/heads/other")))
	try("tag", requestBody(b.pack(goodObjs), cmdOf(plumbing.ZeroHash, good, "refs/tags/v1")))
	try("ref traversal", requestBody(b.pack(goodObjs), cmdOf(plumbing.ZeroHash, good, "refs/heads/../../x")))
	try("two commands", requestBody(b.pack(goodObjs), cmdOf(cur, good, main), cmdOf(plumbing.ZeroHash, good, "refs/heads/o")))
	try("missing blob", update(good, goodObjs[0], goodObjs[1]))
	try("missing tree", update(good, goodObjs[0], goodObjs[2]))
	try("missing commit", requestBody(b.pack(nil), cmdOf(cur, good, main)))
	try("path traversal", withTree(file("../memory_x.md", bl)))
	try("disallowed name", withTree(file("graph.json", bl)))
	try("slash in name", withTree(file("memory_a/b.md", bl)))
	try("dot git", withTree(file(".git", bl)))
	try("symlink", withTree(object.TreeEntry{Name: "memory_l.md", Mode: filemode.Symlink, Hash: bl}))
	try("executable", withTree(object.TreeEntry{Name: "memory_x.md", Mode: filemode.Executable, Hash: bl}))
	try("submodule", withTree(object.TreeEntry{Name: "memory_s.md", Mode: filemode.Submodule, Hash: cur}))
	sub := b.tree(file("memory_a.md", bl))
	dirTree := b.tree(object.TreeEntry{Name: "memory_d.md", Mode: filemode.Dir, Hash: sub})
	dirCommit := b.commit(dirTree, "dir", cur)
	try("subdirectory", update(dirCommit, dirCommit, dirTree, sub, bl))
	try("duplicate names", withTree(file("memory_a.md", bl), file("memory_a.md", bl)))
	try("blob entry points at commit", withTree(file("memory_a.md", cur)))
	mt := b.tree(file("memory_a.md", bl))
	merge := b.commit(mt, "merge", cur, root)
	try("merge commit", update(merge, merge, mt, bl))
	badT := b.tree(file("evil.sh", bl))
	mid := b.commit(badT, "bad middle", cur)
	top := b.commit(mt, "clean top", mid)
	try("bad intermediate commit", update(top, top, mid, badT, mt, bl))
	pk := b.pack(goodObjs)
	try("truncated pack", requestBody(pk[:len(pk)-30], cmdOf(cur, good, main)))
	flipped := append([]byte(nil), pk...)
	flipped[len(flipped)-1] ^= 1
	try("bad pack checksum", requestBody(flipped, cmdOf(cur, good, main)))
	try("garbage body", []byte("not a git request"))

	t.Run("valid push still works", func(t *testing.T) { s.mustPush(t, pk, cur, good) })
	fsck(t, s.repo())
	if left, _ := os.ReadDir(filepath.Join(s.data.Dir, "tmp")); len(left) != 0 {
		t.Fatal("temp files left behind:", len(left))
	}
}

// ---- raw pack writing for hostile packs ----

type rawPack struct {
	buf bytes.Buffer
	n   uint32
}

func (p *rawPack) header(typ int, size int) {
	c := byte(typ<<4) | byte(size&15)
	size >>= 4
	for size > 0 {
		p.buf.WriteByte(c | 0x80)
		c = byte(size & 127)
		size >>= 7
	}
	p.buf.WriteByte(c)
}

func deflate(d []byte) []byte {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(d)
	w.Close()
	return b.Bytes()
}

// add writes one object and returns its offset within the finished pack.
func (p *rawPack) add(typ, declared int, data []byte) int {
	off := 12 + p.buf.Len()
	p.header(typ, declared)
	p.buf.Write(deflate(data))
	p.n++
	return off
}

func (p *rawPack) addDelta(baseOff int, delta []byte) {
	off := 12 + p.buf.Len()
	p.header(6, len(delta))
	rel := off - baseOff
	enc := []byte{byte(rel & 127)}
	for rel >>= 7; rel > 0; rel >>= 7 {
		rel--
		enc = append([]byte{byte(rel&127) | 0x80}, enc...)
	}
	p.buf.Write(enc)
	p.buf.Write(deflate(delta))
	p.n++
}

func (p *rawPack) bytes() []byte {
	out := append([]byte("PACK"), 0, 0, 0, 2)
	out = binary.BigEndian.AppendUint32(out, p.n)
	out = append(out, p.buf.Bytes()...)
	sum := sha1.Sum(out)
	return append(out, sum[:]...)
}

// bombDelta turns a 64 KiB base into copies*64 KiB of output.
func bombDelta(copies int) []byte {
	d := binary.AppendUvarint(nil, 0x10000)
	d = binary.AppendUvarint(d, uint64(copies)*0x10000)
	for i := 0; i < copies; i++ {
		d = append(d, 0x80) // copy offset 0, size 0 meaning 0x10000
	}
	return d
}

func TestLimits(t *testing.T) {
	s := start(t)
	b := newBuilder()
	cur, co := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(co), plumbing.ZeroHash, cur)
	fake := plumbing.NewHash(strings.Repeat("1", 40))
	try := func(name string, pack []byte) {
		t.Run(name, func(t *testing.T) {
			code, out, err := s.send(requestBody(pack, cmdOf(cur, fake, mainRef.String())), s.token)
			if err == nil && accepted(code, out) {
				t.Fatal("ACCEPTED")
			}
			if s.head() != cur {
				t.Fatal("main moved")
			}
			// These must be refused before git runs, not by git running out.
			if err == nil && name != "lying header, inflates past the limit" && !strings.Contains(name, "delta lies") && code != 400 && code != 413 {
				t.Fatalf("reached git: %d %q", code, out)
			}
		})
	}
	big := make([]byte, 33<<20)
	rand.Read(big)
	try("oversized body", big)

	var p1 rawPack
	p1.add(3, 17<<20, make([]byte, 17<<20))
	try("one object over 16MB", p1.bytes())

	var p2 rawPack
	p2.add(3, 10, make([]byte, 40<<20)) // far above the object limit
	try("lying header, inflates past the limit", p2.bytes())

	var p3 rawPack
	for i := 0; i < 12; i++ {
		d := make([]byte, 15<<20)
		d[0] = byte(i)
		p3.add(3, len(d), d)
	}
	try("total over 128MB", p3.bytes())

	var p4 rawPack
	off := p4.add(3, 0x10000, make([]byte, 0x10000))
	for i := 0; i < 100; i++ {
		p4.addDelta(off, bombDelta(240)) // 15 MB each
	}
	try("delta bomb, 1.5GB from deltas", p4.bytes())

	var p5 rawPack
	off = p5.add(3, 0x10000, make([]byte, 0x10000))
	p5.addDelta(off, bombDelta(1<<15)) // 2 GB single result
	try("single 2GB delta result", p5.bytes())

	var p6 rawPack
	off = p6.add(3, 0x10000, make([]byte, 0x10000))
	lie := binary.AppendUvarint(binary.AppendUvarint(nil, 0x10000), 100)
	for i := 0; i < 1<<15; i++ {
		lie = append(lie, 0x80)
	}
	p6.addDelta(off, lie)
	try("delta lies: declares 100 bytes, produces 2GB", p6.bytes())

	huge := append([]byte("PACK"), 0, 0, 0, 2, 0xff, 0xff, 0xff, 0xff)
	try("4 billion declared objects", huge)

	next, no := b.simple("after", []byte("after"), cur)
	s.mustPush(t, b.pack(no), cur, next)
}

func TestConcurrentPushesHaveOneWinner(t *testing.T) {
	s := start(t)
	b := newBuilder()
	cur, objs := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(objs), plumbing.ZeroHash, cur)
	for i := range 25 {
		var tips [2]plumbing.Hash
		var bodies [2][]byte
		for k := range tips {
			var o []plumbing.Hash
			tips[k], o = b.simple(fmt.Sprintf("%d-%d", i, k), []byte(fmt.Sprintf("%d-%d", i, k)), cur)
			bodies[k] = requestBody(b.pack(o), cmdOf(cur, tips[k], mainRef.String()))
		}
		var wg sync.WaitGroup
		var ok [2]bool
		gate := make(chan struct{})
		for k := range tips {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-gate
				code, out, err := s.send(bodies[k], s.token)
				ok[k] = err == nil && accepted(code, out)
			}()
		}
		close(gate)
		wg.Wait()
		if ok[0] == ok[1] {
			t.Fatalf("trial %d: accepted=%v", i, ok)
		}
		cur = tips[0]
		if ok[1] {
			cur = tips[1]
		}
		if s.head() != cur {
			t.Fatalf("trial %d: head is not the winner", i)
		}
	}
	fsck(t, s.repo())
}

func TestAccess(t *testing.T) {
	s := start(t)
	check(s.data.CreateProject("other"))
	b := newBuilder()
	c, objs := b.simple("root", []byte("root"))
	body := requestBody(b.pack(objs), cmdOf(plumbing.ZeroHash, c, mainRef.String()))
	status := func(method, path, secret string) int {
		req := must(http.NewRequest(method, s.http.URL+path, nil))
		if secret != "" {
			req.SetBasicAuth("x", secret)
		}
		resp := must(httpc.Do(req))
		resp.Body.Close()
		return resp.StatusCode
	}
	const refs = "/info/refs?service=git-upload-pack"
	for _, c := range []struct {
		name, method, path, secret string
		want                       int
	}{
		{"health", "GET", "/health", "", 200},
		{"allowed", "GET", "/p.git" + refs, s.token, 200},
		{"no token", "GET", "/p.git" + refs, "", 401},
		{"wrong token", "GET", "/p.git" + refs, "mgc_wrong", 401},
		{"forbidden project", "GET", "/other.git" + refs, s.token, 404},
		{"unknown project looks the same", "GET", "/nothing.git" + refs, s.token, 404},
		{"dumb protocol file", "GET", "/p.git/HEAD", s.token, 404},
		{"config file", "GET", "/p.git/config", s.token, 404},
		{"objects", "GET", "/p.git/objects/info/packs", s.token, 404},
		{"extra query", "GET", "/p.git" + refs + "&x=1", s.token, 404},
		{"traversal", "GET", "/p.git/../other.git" + refs, s.token, 404},
		{"wrong method", "POST", "/p.git" + refs, s.token, 404},
	} {
		if got := status(c.method, c.path, c.secret); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	check(s.data.RevokeToken(s.tokenID)) // while the server runs
	if code, _, _ := s.send(body, s.token); code != 401 {
		t.Errorf("revoked token: %d", code)
	}
	if !s.head().IsZero() {
		t.Error("a refused request changed the repository")
	}
}

func TestStaleLockRecoveredAtStart(t *testing.T) {
	s := start(t)
	b := newBuilder()
	cur, co := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(co), plumbing.ZeroHash, cur)
	next, no := b.simple("next", []byte("next"), cur)
	check(os.WriteFile(filepath.Join(s.repo(), "refs", "heads", "main.lock"), []byte("stale"), 0644))
	if code, out, _ := s.send(requestBody(b.pack(no), cmdOf(cur, next, mainRef.String())), s.token); accepted(code, out) {
		t.Fatal("the lock should block this push")
	}
	s.stop()
	s.boot(t)
	s.mustPush(t, b.pack(no), cur, next)
}

func TestMaintenancePrunesLeftovers(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 3 })
	b := newBuilder()
	root, ro := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(ro), plumbing.ZeroHash, root)
	cur, co := b.simple("second", []byte("second"), root)
	s.mustPush(t, b.pack(co), root, cur)
	stale, so := b.simple("stale", []byte("stale"), root)
	if code, out, _ := s.send(requestBody(b.pack(so), cmdOf(root, stale, mainRef.String())), s.token); accepted(code, out) {
		t.Fatal("stale push accepted")
	}
	s.stop() // waits for maintenance
	if u := gitRun(t, s.repo(), "fsck", "--unreachable", "--no-reflogs"); strings.Contains(u, "unreachable") {
		t.Fatalf("leftovers remain:\n%s", u)
	}
	fsck(t, s.repo())
}

func TestOneServerPerDataDirAndHookRequired(t *testing.T) {
	s := start(t)
	if _, err := New(s.opts); err == nil {
		t.Fatal("two servers shared a data dir")
	}
	s.stop()
	bad := s.opts
	bad.HookCommand = nil
	if _, err := New(bad); err == nil {
		t.Fatal("started without a hook command")
	}
	s.boot(t)
}

func TestAdmin(t *testing.T) {
	d := Data{Dir: filepath.Join(t.TempDir(), "data")}
	for _, bad := range []string{"", "A", "../x", "a/b", "-a", strings.Repeat("a", 64)} {
		if d.CreateProject(bad) == nil {
			t.Errorf("created project %q", bad)
		}
	}
	check(d.CreateProject("one"))
	check(d.CreateProject("two"))
	if d.CreateProject("one") == nil {
		t.Error("created a project twice")
	}
	if names := must(d.Projects()); !slices.Equal(names, []string{"one", "two"}) {
		t.Errorf("projects %v", names)
	}
	for name, bad := range map[string][]string{"no project": nil, "unknown project": {"three"}} {
		if _, _, err := d.CreateToken("vm", bad); err == nil {
			t.Error(name)
		}
	}
	if _, _, err := d.CreateToken(" ", []string{"one"}); err == nil {
		t.Error("unnamed token")
	}
	// Concurrent admin changes must not lose tokens.
	var wg sync.WaitGroup
	secrets := make([]string, 8)
	for i := range secrets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			secret, _, err := d.CreateToken("vm", []string{"two", "one", "two"})
			check(err)
			secrets[i] = secret
		}()
	}
	wg.Wait()
	tokens := must(d.Tokens())
	if len(tokens) != 8 || !slices.Equal(tokens[0].Projects, []string{"one", "two"}) {
		t.Fatalf("tokens %+v", tokens)
	}
	raw := must(os.ReadFile(d.tokensPath()))
	for _, secret := range secrets {
		if !strings.HasPrefix(secret, TokenPrefix) || bytes.Contains(raw, []byte(secret)) {
			t.Fatal("secret stored or malformed")
		}
		if _, ok := d.authorize(secret); !ok {
			t.Fatal("token does not authorize")
		}
		// The client's scanner must notice a token pasted into a memory.
		if len(cloudsync.Scan("m", nil, []byte("token "+secret), nil)) != 1 {
			t.Fatal("secret scan misses server tokens")
		}
	}
	check(d.RevokeToken(tokens[0].ID))
	if d.RevokeToken(tokens[0].ID) == nil || len(must(d.Tokens())) != 7 {
		t.Error("revoke")
	}
	for path, want := range map[string]os.FileMode{d.Dir: 0700, d.tokensPath(): 0600} {
		if got := must(os.Stat(path)).Mode().Perm(); got != want {
			t.Errorf("%s mode %v", path, got)
		}
	}
}

// The real client against the real server.
func TestClientSyncsThroughServer(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 4 })
	type vm struct {
		dir   string
		store cloudsync.Store
	}
	newVM := func() vm {
		v := vm{dir: t.TempDir(), store: cloudsync.Store{Dir: t.TempDir()}}
		check(v.store.SaveLink(cloudsync.Link{Project: "p", URL: s.url, MemoryDir: v.dir}))
		return v
	}
	syncVM := func(v vm) *cloudsync.Result {
		t.Helper()
		res, err := cloudsync.Sync(v.store, cloudsync.Options{Token: s.token})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	write := func(v vm, name, text string) {
		check(os.WriteFile(filepath.Join(v.dir, name), []byte(text), 0644))
	}
	read := func(v vm, name string) string {
		data, _ := os.ReadFile(filepath.Join(v.dir, name))
		return string(data)
	}
	one, two := newVM(), newVM()
	big := make([]byte, 3<<20) // larger than git's chunking threshold
	rand.Read(big)
	write(one, "memory_a.md", "alpha")
	write(one, "memory_big.md", string(big))
	syncVM(one)
	syncVM(two)
	for i := range 10 { // across several rounds of maintenance
		write(one, "memory_a.md", fmt.Sprint("version ", i))
		syncVM(one)
		write(two, fmt.Sprintf("memory_%d.md", i), "x")
		syncVM(two)
	}
	syncVM(one)
	if read(two, "memory_a.md") != "version 9" || read(one, "memory_9.md") != "x" || read(two, "memory_big.md") != string(big) {
		t.Fatal("content did not travel")
	}
	write(one, "memory_a.md", "from one")
	write(two, "memory_a.md", "from two")
	syncVM(one)
	if res := syncVM(two); len(res.Conflicts) != 1 {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	late := newVM()
	syncVM(late)
	if read(late, "memory_a.md") != "from one" {
		t.Fatal("clone is wrong")
	}
	s.stop()
	fsck(t, s.repo())
}
