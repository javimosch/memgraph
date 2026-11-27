package cloudsync

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testToken = "test-token"

// server is a real git server: git http-backend behind a token check. It has
// none of the production front end's rules beyond git's own.
type server struct {
	t    *testing.T
	url  string
	repo string
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newServer(t *testing.T) *server {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is needed to run the test server")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "p.git")
	gitCmd(t, root, "init", "-q", "--bare", "-b", "main", repo)
	gitCmd(t, repo, "config", "http.receivepack", "true")
	gitCmd(t, repo, "config", "receive.denyNonFastForwards", "true")
	backend := &cgi.Handler{Path: gitBin, Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "HOME=" + root}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pass, _ := r.BasicAuth(); pass != testToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.TransferEncoding, r.ContentLength, r.Body = nil, int64(len(body)), io.NopCloser(bytes.NewReader(body))
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return &server{t: t, url: ts.URL + "/p.git", repo: repo}
}

func (s *server) files() []string {
	out := gitCmd(s.t, s.repo, "ls-tree", "--name-only", "main")
	return strings.Fields(out)
}

func (s *server) show(name string) string { return gitCmd(s.t, s.repo, "show", "main:"+name) }

// everything returns all content ever committed, for leak checks.
func (s *server) everything() string { return gitCmd(s.t, s.repo, "log", "-p", "--all") }

type vm struct {
	t     *testing.T
	dir   string
	store Store
	opts  Options
}

func newVM(t *testing.T, s *server) *vm {
	v := &vm{t: t, dir: filepath.Join(t.TempDir(), "memory"), store: Store{Dir: t.TempDir()}}
	if err := os.MkdirAll(v.dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := v.store.SaveLink(Link{Project: "p", URL: s.url, MemoryDir: v.dir}); err != nil {
		t.Fatal(err)
	}
	v.opts = Options{Token: testToken, Content: func(_ string, data []byte) string {
		parts := strings.SplitN(string(data), "---\n", 3)
		return strings.Trim(parts[len(parts)-1], "\n")
	}}
	return v
}

func (v *vm) write(id, body string, front ...string) {
	extra := ""
	for _, f := range front {
		extra += f + "\n"
	}
	data := "---\nname: " + id + "\n" + extra + "---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(v.dir, "memory_"+id+".md"), []byte(data), 0644); err != nil {
		v.t.Fatal(err)
	}
}

func (v *vm) read(id string) string {
	data, err := os.ReadFile(filepath.Join(v.dir, "memory_"+id+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		v.t.Fatal(err)
	}
	return v.opts.Content("", data)
}

func (v *vm) remove(id string) {
	if err := os.Remove(filepath.Join(v.dir, "memory_"+id+".md")); err != nil {
		v.t.Fatal(err)
	}
}

func (v *vm) ledger(lines ...string) {
	path := filepath.Join(v.dir, LedgerName)
	old, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(old, []byte(strings.Join(lines, "\n")+"\n")...), 0644); err != nil {
		v.t.Fatal(err)
	}
}

func (v *vm) sync() *Result {
	v.t.Helper()
	res, err := Sync(v.store, v.opts)
	if err != nil {
		v.t.Fatalf("sync: %v", err)
	}
	return res
}

func (v *vm) blocked(want string) *BlockedError {
	v.t.Helper()
	_, err := Sync(v.store, v.opts)
	var b *BlockedError
	if !errors.As(err, &b) || !strings.Contains(b.Reason, want) {
		v.t.Fatalf("error = %v, want blocked with %q", err, want)
	}
	return b
}

// pair returns two machines that already share memories a and b.
func pair(t *testing.T) (*server, *vm, *vm) {
	s := newServer(t)
	one, two := newVM(t, s), newVM(t, s)
	one.write("a", "alpha")
	one.write("b", "bravo")
	one.sync()
	two.sync()
	if two.read("a") != "alpha" || two.read("b") != "bravo" {
		t.Fatal("second machine did not receive the memories")
	}
	return s, one, two
}

func TestTwoMachines(t *testing.T) {
	s, one, two := pair(t)
	one.write("a", "alpha edited")
	two.write("c", "charlie")
	one.sync()
	two.sync()
	one.sync()
	if two.read("a") != "alpha edited" || one.read("c") != "charlie" {
		t.Fatal("changes did not travel")
	}
	two.remove("b")
	two.sync()
	one.sync()
	if one.read("b") != "" || slices.Contains(s.files(), "memory_b.md") {
		t.Fatal("delete did not travel")
	}
	head := gitCmd(t, s.repo, "rev-parse", "main")
	if res := one.sync(); res.Commit != "" || len(res.Downloaded)+len(res.Uploaded) != 0 {
		t.Fatalf("idle sync did something: %+v", res)
	}
	if gitCmd(t, s.repo, "rev-parse", "main") != head {
		t.Fatal("idle sync committed")
	}
}

func TestOfflineDoubleEditKeepsBoth(t *testing.T) {
	_, one, two := pair(t)
	one.write("a", "from one")
	two.write("a", "from two")
	one.sync()
	res := two.sync()
	if two.read("a") != "from one" {
		t.Fatal("server version should be active")
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != BothChanged {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
	kept, err := os.ReadFile(res.Conflicts[0].Copy)
	if err != nil || !strings.Contains(string(kept), "from two") {
		t.Fatalf("losing edit not kept: %v %s", err, kept)
	}
	if strings.HasPrefix(res.Conflicts[0].Copy, two.dir) {
		t.Fatal("conflict copy is inside the memory dir")
	}
}

func TestEditBeatsDeleteBothWays(t *testing.T) {
	for _, deleterFirst := range []bool{true, false} {
		t.Run(fmt.Sprint("deleterFirst=", deleterFirst), func(t *testing.T) {
			s, one, two := pair(t)
			one.remove("a")
			two.write("a", "still needed")
			first, second := one, two
			if !deleterFirst {
				first, second = two, one
			}
			first.sync()
			res := second.sync()
			first.sync()
			if one.read("a") != "still needed" || two.read("a") != "still needed" ||
				!strings.Contains(s.show("memory_a.md"), "still needed") {
				t.Fatal("edit was lost")
			}
			if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != EditedDelete {
				t.Fatalf("conflicts %+v", res.Conflicts)
			}
		})
	}
}

func TestStalePushRetriesWithoutLosingEither(t *testing.T) {
	s, one, two := pair(t)
	one.write("from-one", "1")
	two.write("from-two", "2")
	raced := false
	one.opts.beforePush = func() {
		if !raced {
			raced = true
			two.sync()
		}
	}
	one.sync()
	for _, f := range []string{"memory_from-one.md", "memory_from-two.md"} {
		if !slices.Contains(s.files(), f) {
			t.Fatalf("%s missing: %v", f, s.files())
		}
	}
	if !raced || one.read("from-two") != "2" {
		t.Fatal("race did not happen or was not merged")
	}
}

func TestFileChangedDuringSyncIsNotOverwritten(t *testing.T) {
	s, one, two := pair(t)
	one.write("a", "server edit")
	one.sync()
	two.opts.beforeApply = func() error { two.write("a", "typed during sync"); return nil }
	res := two.sync()
	if two.read("a") != "typed during sync" || !slices.Equal(res.Deferred, []string{"memory_a.md"}) {
		t.Fatalf("local edit lost: %q %+v", two.read("a"), res)
	}
	two.opts.beforeApply = nil
	res = two.sync()
	if len(res.Conflicts) != 1 || two.read("a") != "server edit" || !strings.Contains(s.show("memory_a.md"), "server edit") {
		t.Fatalf("expected a conflict on the next sync: %+v", res)
	}
}

func TestEditOfPushedFileDuringSyncIsNoConflict(t *testing.T) {
	s, one, _ := pair(t)
	one.write("a", "v2")
	one.opts.beforeApply = func() error { one.write("a", "v3"); return nil }
	one.sync()
	one.opts.beforeApply = nil
	res := one.sync()
	if len(res.Conflicts) != 0 || !strings.Contains(s.show("memory_a.md"), "v3") {
		t.Fatalf("false conflict: %+v", res.Conflicts)
	}
}

func TestCrashAfterPushIsSafeToRerun(t *testing.T) {
	_, one, two := pair(t)
	one.write("a", "from one")
	one.sync()
	two.write("a", "from two")
	two.write("b", "two edits b")
	two.opts.beforeApply = func() error { return errors.New("crash") }
	if _, err := Sync(two.store, two.opts); err == nil {
		t.Fatal("expected the simulated crash")
	}
	two.opts.beforeApply = nil
	two.sync()
	two.sync()
	st, err := two.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if two.read("a") != "from one" || two.read("b") != "two edits b" || len(st.Open()) != 1 {
		t.Fatalf("a=%q b=%q conflicts=%+v", two.read("a"), two.read("b"), st.Open())
	}
}

func TestFirstLinkDoesNotResurrectDeleted(t *testing.T) {
	s, one, _ := pair(t)
	one.remove("a")
	one.ledger(`{"ts":"2026-01-01T00:00:00Z","action":"delete","memory_id":"a","snapshot":{"content":"alpha"}}`)
	one.sync()
	late := newVM(t, s)
	late.write("a", "alpha")
	late.write("mine", "new here")
	res := late.sync()
	if late.read("a") != "" || slices.Contains(s.files(), "memory_a.md") || !slices.Contains(s.files(), "memory_mine.md") {
		t.Fatalf("resurrected or lost: %v", s.files())
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != Resurrection || res.Conflicts[0].Copy == "" {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func TestLocalOnlyNeverLeaves(t *testing.T) {
	s := newServer(t)
	one := newVM(t, s)
	one.write("pub", "public text")
	one.write("priv", "PRIVATE-BODY", "local: true")
	// Created and deleted before any sync: only the snapshot flag protects it.
	one.ledger(`{"ts":"2026-01-01T00:00:00Z","action":"delete","memory_id":"gone","snapshot":{"content":"PRIVATE-GONE","local":true}}`,
		`{"ts":"2026-01-02T00:00:00Z","action":"supersede","memory_id":"priv","snapshot":{"content":"PRIVATE-OLD"}}`,
		`{"ts":"2026-01-03T00:00:00Z","action":"delete","memory_id":"other","snapshot":{"content":"shared history"}}`)
	one.sync()
	// An older binary rewrites the file and drops the flag.
	one.write("priv", "PRIVATE-REWRITTEN")
	one.sync()
	if all := s.everything(); strings.Contains(all, "PRIVATE") || !strings.Contains(all, "shared history") {
		t.Fatalf("leak or lost history:\n%s", all)
	}
	local, _ := os.ReadFile(filepath.Join(one.dir, LedgerName))
	if !strings.Contains(string(local), "PRIVATE-GONE") || one.read("priv") != "PRIVATE-REWRITTEN" {
		t.Fatal("local data was changed")
	}
}

func TestFlaggingASyncedMemoryHoldsIt(t *testing.T) {
	s, one, two := pair(t)
	one.write("a", "now secret edit", "local: true")
	res := one.sync()
	two.write("a", "two edits a")
	two.sync()
	one.sync()
	if !slices.Equal(res.Held, []string{"memory_a.md"}) || one.read("a") != "now secret edit" {
		t.Fatalf("held=%v local=%q", res.Held, one.read("a"))
	}
	if strings.Contains(s.everything(), "now secret") || !strings.Contains(s.show("memory_a.md"), "two edits a") {
		t.Fatal("held file was pushed, or the server copy was lost")
	}
}

func TestMassDeleteAndMissingDir(t *testing.T) {
	s := newServer(t)
	one, two := newVM(t, s), newVM(t, s)
	for i := range 30 {
		one.write(fmt.Sprint(i), "x")
	}
	one.sync()
	two.sync()
	for i := range 11 {
		one.remove(fmt.Sprint(i))
	}
	one.blocked("delete 11")
	if len(s.files()) != 30 {
		t.Fatal("blocked sync changed the server")
	}
	one.opts.AllowMassDelete = true
	one.sync()
	two.blocked("delete 11") // the receiving side is guarded too
	if two.read("0") != "x" {
		t.Fatal("blocked sync changed local files")
	}
	one.opts.AllowMassDelete = false
	if err := os.RemoveAll(one.dir); err != nil {
		t.Fatal(err)
	}
	one.opts.AllowMassDelete = true
	one.blocked("missing")
	if len(s.files()) != 19 {
		t.Fatal("server changed")
	}
}

func TestSecretBlocksWholeSync(t *testing.T) {
	s, one, _ := pair(t)
	fake := "gh" + "p_" + strings.Repeat("z9", 18)
	one.write("ok", "harmless")
	one.write("oops", "token "+fake)
	b := one.blocked("secret")
	if len(b.Findings) != 1 || slices.Contains(s.files(), "memory_ok.md") {
		t.Fatalf("findings %+v files %v", b.Findings, s.files())
	}
	st, _ := one.store.Load()
	st.Allowed[b.Findings[0].Fingerprint] = true
	if err := one.store.Save(st); err != nil {
		t.Fatal(err)
	}
	one.sync()
	if !slices.Contains(s.files(), "memory_oops.md") {
		t.Fatal("allowed finding still blocked")
	}
}

func TestSecretInLedgerBlocks(t *testing.T) {
	s, one, _ := pair(t)
	one.ledger(`{"ts":"2026-01-01T00:00:00Z","action":"delete","memory_id":"z","snapshot":{"content":"AKIA` + strings.Repeat("Q", 16) + `"}}`)
	one.blocked("secret")
	if slices.Contains(s.files(), LedgerName) {
		t.Fatal("ledger was pushed")
	}
}

func TestRewrittenHistoryStops(t *testing.T) {
	s, one, two := pair(t)
	old := gitCmd(t, s.repo, "rev-parse", "main")
	one.write("c", "charlie")
	one.sync()
	gitCmd(t, s.repo, "update-ref", "refs/heads/main", old) // restore an old backup
	one.blocked("history")
	if one.read("c") != "charlie" {
		t.Fatal("restored backup deleted a memory")
	}
	two.sync() // never saw the lost commit, so it carries on
}

func TestHostileServerTreeWritesNothing(t *testing.T) {
	for name, path := range map[string]string{"script": "evil.sh", "dir": "memory_x/y.md", "symlink": "memory_link.md"} {
		t.Run(name, func(t *testing.T) {
			s, _, two := pair(t)
			work := t.TempDir()
			gitCmd(t, work, "clone", "-q", s.repo, "w")
			w := filepath.Join(work, "w")
			target := filepath.Join(w, path)
			os.MkdirAll(filepath.Dir(target), 0755)
			if name == "symlink" {
				if err := os.Symlink("/etc/passwd", target); err != nil {
					t.Fatal(err)
				}
			} else {
				os.WriteFile(target, []byte("x"), 0644)
			}
			os.WriteFile(filepath.Join(w, "memory_good.md"), []byte("good"), 0644)
			gitCmd(t, w, "add", "-A")
			gitCmd(t, w, "commit", "-qm", "hostile")
			gitCmd(t, w, "push", "-q", s.repo, "HEAD:main")
			if _, err := Sync(two.store, two.opts); err == nil || !strings.Contains(err.Error(), "does not allow") {
				t.Fatalf("error = %v", err)
			}
			if two.read("good") != "" {
				t.Fatal("wrote files from a hostile tree")
			}
		})
	}
}

func TestDryRunAndOfflineChangeNothing(t *testing.T) {
	s, one, _ := pair(t)
	one.write("c", "charlie")
	head := gitCmd(t, s.repo, "rev-parse", "main")
	one.opts.DryRun = true
	res := one.sync()
	if !slices.Equal(res.Uploaded, []string{"memory_c.md"}) || gitCmd(t, s.repo, "rev-parse", "main") != head {
		t.Fatalf("dry run: %+v", res)
	}
	one.opts.DryRun = false
	link, _ := one.store.Link()
	good := link.URL
	link.URL = "http://127.0.0.1:1/p.git"
	one.store.SaveLink(link)
	if _, err := Sync(one.store, one.opts); err == nil {
		t.Fatal("expected a network error")
	}
	st, _ := one.store.Load()
	pending, _ := Pending(one.dir, st)
	if st.LastError == "" || !slices.Equal(pending, []string{"memory_c.md"}) || one.read("c") != "charlie" {
		t.Fatalf("offline state: %q %v", st.LastError, pending)
	}
	link.URL = good
	one.store.SaveLink(link)
	one.sync()
}

func TestBadTokenAndURLs(t *testing.T) {
	_, one, _ := pair(t)
	one.opts.Token = "wrong"
	if _, err := Sync(one.store, one.opts); err == nil {
		t.Fatal("wrong token accepted")
	}
	for url, ok := range map[string]bool{
		"https://memory.example.invalid/p.git": true, "http://127.0.0.1:8080/p.git": true,
		"http://memory.example.invalid/p.git": false, "/tmp/repo.git": false, "file:///tmp/repo.git": false,
		"https://user:pw@example.invalid/p.git": false, "git@example.invalid:p.git": false,
	} {
		if (CheckURL(url) == nil) != ok {
			t.Errorf("CheckURL(%q) ok=%v", url, !ok)
		}
	}
}
