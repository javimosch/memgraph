package cloudserver

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"

	"errors"
	"github.com/javimosch/memgraph/internal/cloudsync"
	"os/exec"
)

func script(t *testing.T, body string) string {
	path := filepath.Join(t.TempDir(), "script")
	check(os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700))
	return path
}

// sleeping reports whether a process is exactly "sleep <seconds>". Matching any
// command line that merely mentions it would also catch the shell running the
// tests.
func sleeping(seconds string) bool {
	dirs := must(os.ReadDir("/proc"))
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		raw, _ := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if string(raw) == "sleep\x00"+seconds+"\x00" {
			return true
		}
	}
	return false
}

func eventually(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(within); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("never happened: " + what)
		}
	}
}

func TestStartupSelfCheck(t *testing.T) {
	for name, c := range map[string]struct {
		hook []string
		want string
	}{
		"hook program missing": {[]string{"/nonexistent/memgraph"}, "valid push was refused"},
		"hook always fails":    {[]string{"false"}, "valid push was refused"},
		"hook allows anything": {[]string{"true"}, "push rules are not enforced"},
	} {
		t.Run(name, func(t *testing.T) {
			data := Data{Dir: filepath.Join(t.TempDir(), "data")}
			opts := Options{DataDir: data.Dir, Limits: DefaultLimits, HookCommand: c.hook,
				LimitCommand: testLimit(must(os.Executable()))}
			s, err := New(opts)
			if err == nil {
				s.Close()
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
			if names := must(data.Projects()); len(names) != 0 {
				t.Errorf("projects left: %v", names)
			}
			if _, err := os.Stat(data.repo(selfCheckProject)); !os.IsNotExist(err) {
				t.Error("self-check repo left behind")
			}
			// A failed start must release the data directory.
			opts.HookCommand = []string{must(os.Executable()), hookArg}
			ok := must(New(opts))
			ok.Close()
		})
	}
}

func TestSelfCheckRepoIsUnreachable(t *testing.T) {
	s := start(t)
	if _, _, err := s.data.CreateToken("x", []string{selfCheckProject}); err == nil {
		t.Fatal("a token can name the self-check project")
	}
	req := must(http.NewRequest("GET", s.http.URL+"/"+selfCheckProject+".git/info/refs?service=git-upload-pack", nil))
	req.SetBasicAuth("x", s.token)
	resp := must(httpc.Do(req))
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal(resp.StatusCode)
	}
}

func TestHookTamperedWhileRunning(t *testing.T) {
	for name, tamper := range map[string]func(hook string){
		"removed":        func(h string) { check(os.Remove(h)) },
		"not executable": func(h string) { check(os.Chmod(h, 0600)) },
		"rewritten":      func(h string) { check(os.WriteFile(h, []byte("#!/bin/sh\nexit 0\n"), 0700)) },
		"replaced by link": func(h string) {
			check(os.Remove(h))
			check(os.Symlink("/bin/true", h))
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := start(t)
			b := newBuilder()
			cur, co := b.simple("root", []byte("root"))
			s.mustPush(t, b.pack(co), plumbing.ZeroHash, cur)
			tamper(s.server.hookPath)
			bad := b.blob([]byte("x"))
			tree := b.tree(file("evil.sh", bad))
			commit := b.commit(tree, "bad", cur)
			code, out, err := s.send(requestBody(b.pack([]plumbing.Hash{commit, tree, bad}),
				cmdOf(cur, commit, mainRef.String())), s.token)
			if err != nil || code != 503 || s.head() != cur {
				t.Fatalf("code=%d out=%q err=%v moved=%v", code, out, err, s.head() != cur)
			}
		})
	}
}

func TestLoginLimit(t *testing.T) {
	s := start(t, func(o *Options) {
		o.ClientIPHeader = "X-Forwarded-For"
		o.Limits.AuthFails = 3
	})
	now := time.Now()
	var mu sync.Mutex
	s.server.logins.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	get := func(forwarded, secret string) int {
		req := must(http.NewRequest("GET", s.url+"/info/refs?service=git-upload-pack", nil))
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		if secret != "" {
			req.SetBasicAuth("x", secret)
		}
		resp := must(httpc.Do(req))
		resp.Body.Close()
		if resp.StatusCode == 429 && resp.Header.Get("Retry-After") == "" {
			t.Error("no Retry-After")
		}
		return resp.StatusCode
	}
	expect := func(what string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %d, want %d", what, got, want)
		}
	}
	const attacker, vm = "203.0.113.9", "198.51.100.7"
	for range 5 { // git asks without credentials first: that is not a failure
		expect("anonymous", get(vm, ""), 401)
	}
	expect("vm still fine", get(vm, s.token), 200)
	for range 3 {
		expect("wrong", get(attacker, "wrong"), 401)
	}
	expect("blocked", get(attacker, "wrong"), 429)
	expect("blocked even with a valid token", get(attacker, s.token), 429)
	expect("forged earlier entries do not help", get(vm+", "+attacker, s.token), 429)
	expect("others unaffected", get(vm, s.token), 200)
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	expect("window over", get(attacker, s.token), 200)

	// Clients that cannot be told apart must never lock each other out.
	for range 10 {
		get("", "wrong")
	}
	expect("unidentified failures are limited", get("", "wrong"), 429)
	expect("unidentified valid token still works", get("", s.token), 200)
}

func TestClientOf(t *testing.T) {
	for _, c := range []struct{ remote, header, value, want string }{
		{"127.0.0.1:5", "", "203.0.113.1", ""}, // untrusted header, local proxy
		{"203.0.113.1:5", "", "198.51.100.1", "203.0.113.1"},
		{"127.0.0.1:5", "X-Forwarded-For", "198.51.100.1, 203.0.113.1", "203.0.113.1"},
		{"127.0.0.1:5", "X-Forwarded-For", "", ""},
		{"127.0.0.1:5", "X-Forwarded-For", "garbage", ""},
		{"127.0.0.1:5", "X-Forwarded-For", "2001:db8:1:2:3::4", "2001:db8:1:2::/64"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.value != "" {
			r.Header.Set("X-Forwarded-For", c.value)
		}
		if got := clientOf(r, c.header); got != c.want {
			t.Errorf("%+v: got %q", c, got)
		}
	}
}

func TestLimiterMemoryIsBounded(t *testing.T) {
	l := newLimiter(3, time.Minute)
	for i := range maxTrackedClients + 500 {
		l.fail(fmt.Sprint(i))
	}
	if len(l.clients) > maxTrackedClients+1 {
		t.Fatal(len(l.clients))
	}
}

// stall starts a POST whose body never finishes.
func stall(t *testing.T, s *srv, headersOnly bool) net.Conn {
	conn := must(net.Dial("tcp", s.http.Listener.Addr().String()))
	t.Cleanup(func() { conn.Close() })
	req := "POST /p.git/git-receive-pack HTTP/1.1\r\nHost: x\r\n"
	if !headersOnly {
		auth := httptest.NewRequest("GET", "/", nil)
		auth.SetBasicAuth("x", s.token)
		req += "Authorization: " + auth.Header.Get("Authorization") +
			"\r\nContent-Type: application/x-git-receive-pack-request\r\nContent-Length: 100000\r\n\r\npartial"
	}
	must(io.WriteString(conn, req))
	return conn
}

func TestUploadSlotsBoundDisk(t *testing.T) {
	s := start(t, func(o *Options) {
		o.Limits.Spools, o.Limits.Wait = 2, 200*time.Millisecond
	})
	one, two := stall(t, s, false), stall(t, s, false)
	eventually(t, 3*time.Second, "both slots taken", func() bool { return len(s.server.spools) == 2 })
	b := newBuilder()
	c, objs := b.simple("root", []byte("root"))
	body := requestBody(b.pack(objs), cmdOf(plumbing.ZeroHash, c, mainRef.String()))
	if code, _, _ := s.send(body, s.token); code != 503 {
		t.Fatalf("third upload: %d, want 503", code)
	}
	one.Close()
	two.Close()
	eventually(t, 3*time.Second, "slots freed", func() bool { return len(s.server.spools) == 0 })
	s.mustPush(t, b.pack(objs), plumbing.ZeroHash, c)
}

func TestSlowSenderIsCutOff(t *testing.T) {
	s := start(t, func(o *Options) {
		o.Limits.ReadHeader, o.Limits.Read = 500*time.Millisecond, time.Second
	})
	for name, headersOnly := range map[string]bool{"headers": true, "body": false} {
		t.Run(name, func(t *testing.T) {
			conn := stall(t, s, headersOnly)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			t0 := time.Now()
			if _, err := io.ReadAll(conn); err != nil {
				t.Fatalf("server kept the connection: %v", err)
			}
			if d := time.Since(t0); d > 4*time.Second {
				t.Fatal("too slow:", d)
			}
			eventually(t, 2*time.Second, "slot freed", func() bool { return len(s.server.spools) == 0 })
		})
	}
}

func TestSlowReaderDoesNotKeepASlot(t *testing.T) {
	s := start(t)
	big := make([]byte, 14<<20) // far more than socket buffers hold
	rand.Read(big)
	b := newBuilder()
	c, objs := b.simple("big", big)
	s.mustPush(t, b.pack(objs), plumbing.ZeroHash, c)
	// A short response limit only now: it covers receiving a request too.
	s.stop()
	s.opts.Limits.Write = 2 * time.Second
	s.boot(t)

	want := "want " + c.String() + "\n"
	body := fmt.Sprintf("%04x%s00000009done\n", len(want)+4, want)
	conn := must(net.Dial("tcp", s.http.Listener.Addr().String()))
	defer conn.Close()
	conn.(*net.TCPConn).SetReadBuffer(4096)
	auth := httptest.NewRequest("GET", "/", nil)
	auth.SetBasicAuth("x", s.token)
	must(fmt.Fprintf(conn, "POST /p.git/git-upload-pack HTTP/1.1\r\nHost: x\r\nAuthorization: %s\r\n"+
		"Content-Type: application/x-git-upload-pack-request\r\nContent-Length: %d\r\n\r\n%s",
		auth.Header.Get("Authorization"), len(body), body))
	eventually(t, 3*time.Second, "fetch started", func() bool { return len(s.server.uploads) == 1 })
	// Never read.
	eventually(t, 15*time.Second, "fetch slot freed", func() bool { return len(s.server.uploads) == 0 })
	eventually(t, 10*time.Second, "no git left", func() bool { return !gitIn(s.data.Dir) })
}

// gitIn reports whether a git process is working inside dir.
func gitIn(dir string) bool {
	for _, d := range must(os.ReadDir("/proc")) {
		cwd, err := os.Readlink("/proc/" + d.Name() + "/cwd")
		comm, _ := os.ReadFile("/proc/" + d.Name() + "/comm")
		if err == nil && strings.HasPrefix(cwd, dir) && strings.HasPrefix(string(comm), "git") {
			return true
		}
	}
	return false
}

func TestHungGitIsKilled(t *testing.T) {
	s := start(t)
	// Set after the startup check, which needs real time to run git.
	s.server.lim.Seconds, s.server.lim.MaintenanceSeconds = 1, 1
	const marker = "31.41592"
	// Like git, it starts a child of its own.
	s.server.git = script(t, "sleep "+marker+" &\nwait")
	req := must(http.NewRequest("GET", s.url+"/info/refs?service=git-upload-pack", nil))
	req.SetBasicAuth("x", s.token)
	seen := make(chan bool, 1)
	go func() {
		for end := time.Now().Add(900 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if sleeping(marker) {
				seen <- true
				return
			}
		}
		seen <- false
	}()
	t0 := time.Now()
	resp := must(httpc.Do(req))
	resp.Body.Close()
	if !<-seen {
		t.Fatal("the test never saw the child, so it cannot tell whether it was killed")
	}
	if d := time.Since(t0); d > 5*time.Second || resp.StatusCode == 200 {
		t.Fatalf("request: %v, status %d", d, resp.StatusCode)
	}
	eventually(t, 3*time.Second, "request children gone", func() bool { return !sleeping(marker) })
	t0 = time.Now()
	if _, err := s.server.maintain("p"); err == nil || time.Since(t0) > 5*time.Second {
		t.Fatalf("maintenance: %v after %v", err, time.Since(t0))
	}
	eventually(t, 3*time.Second, "maintenance children gone", func() bool { return !sleeping(marker) })
}

func (s *srv) pushes(t *testing.T, n int) {
	t.Helper()
	for range n {
		old := s.head()
		b := newBuilder()
		var parents []plumbing.Hash
		if !old.IsZero() {
			parents = []plumbing.Hash{old}
		}
		data := make([]byte, 16)
		rand.Read(data)
		c, objs := b.simple("n", data, parents...)
		s.mustPush(t, b.pack(objs), old, c)
	}
}

func (s *srv) savedCount() string {
	raw, _ := os.ReadFile(filepath.Join(s.repo(), pushCountFile))
	return strings.TrimSpace(string(raw))
}

func TestMaintenanceSurvivesRestarts(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 4 })
	s.pushes(t, 3)
	s.stop()
	if s.savedCount() != "3" {
		t.Fatalf("saved %q", s.savedCount())
	}
	s.boot(t)
	s.pushes(t, 1) // the fourth, counted across the restart
	s.stop()
	if s.savedCount() != "0" {
		t.Fatalf("maintenance did not run: %q", s.savedCount())
	}
	// Due when the server stopped: runs at the next start, without a push.
	check(os.WriteFile(filepath.Join(s.repo(), pushCountFile), []byte("9\n"), 0600))
	s.boot(t)
	s.stop()
	if s.savedCount() != "0" {
		t.Fatalf("not resumed: %q", s.savedCount())
	}
	fsck(t, s.repo())
}

func TestFailedMaintenanceIsRetriedSoon(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 12 })
	s.server.git = "false"
	p := s.server.project("p")
	p.mu.Lock()
	p.count = 12
	s.server.maintainIfDue("p", p)
	p.mu.Unlock()
	s.server.maintenance.Wait()
	if s.savedCount() != "2" { // due again after 10 more pushes
		t.Fatalf("count %q", s.savedCount())
	}
}

// A dozen machines at once, with the normal limits.
func TestManyClientsAtOnce(t *testing.T) {
	s := start(t, func(o *Options) { o.RepackEvery = 5 })
	const n = 12
	var wg sync.WaitGroup
	attempts := make([]int, n)
	dirs := make([]string, n)
	stores := make([]cloudsync.Store, n)
	for i := range n {
		dirs[i], stores[i] = t.TempDir(), cloudsync.Store{Dir: t.TempDir()}
		check(stores[i].SaveLink(cloudsync.Link{Project: "p", URL: s.url, MemoryDir: dirs[i]}))
		check(os.WriteFile(filepath.Join(dirs[i], fmt.Sprintf("memory_%d.md", i)), []byte(fmt.Sprint(i)), 0644))
	}
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				attempts[i]++
				_, err := cloudsync.Sync(stores[i], cloudsync.Options{Token: s.token})
				if err == nil {
					return
				}
				// Losing every push race or finding the server busy is allowed,
				// as long as running sync again gets there.
				if attempts[i] >= 40 {
					t.Errorf("client %d: %v", i, err)
					return
				}
				time.Sleep(time.Duration(50+i*20) * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	for i := range n {
		if _, err := cloudsync.Sync(stores[i], cloudsync.Options{Token: s.token}); err != nil {
			t.Fatal(err)
		}
		if found := must(filepath.Glob(filepath.Join(dirs[i], "memory_*.md"))); len(found) != n {
			t.Fatalf("client %d has %d memories", i, len(found))
		}
	}
	s.stop()
	fsck(t, s.repo())
	total := 0
	for _, a := range attempts {
		total += a
	}
	t.Logf("%d clients needed %d sync runs in total", n, total)
}

// Limit passes on what the program did when it ends in time.
func TestLimitPassesOnOutputAndExitCode(t *testing.T) {
	self := must(os.Executable())
	cmd := exec.Command(self, limitArg, "20", "sh", "-c", "cat; echo err >&2; exit 7")
	cmd.Stdin = strings.NewReader("in\n")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("err = %v", err)
	}
	if stdout.String() != "in\n" || stderr.String() != "err\n" {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	for _, bad := range [][]string{{}, {"20"}, {"0", "true"}, {"x", "true"}} {
		if err := exec.Command(self, append([]string{limitArg}, bad...)...).Run(); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
