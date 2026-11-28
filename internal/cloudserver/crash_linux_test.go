package cloudserver

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

const serveEnv = "MEMGRAPH_TEST_SERVE_DATA"

// serveForCrashTest runs a server as its own process, so it can be killed.
func serveForCrashTest(dir string) {
	self := must(os.Executable())
	os.Unsetenv(serveEnv) // the hook runs this binary too
	s := must(New(Options{DataDir: dir, Limits: DefaultLimits, HookCommand: []string{self, hookArg}, LimitCommand: testLimit(self)}))
	ln := must(net.Listen("tcp", "127.0.0.1:0"))
	fmt.Println("ADDR", ln.Addr())
	check(http.Serve(ln, s))
}

// children maps every process to its direct children.
func children() map[int][]int {
	m := map[int][]int{}
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		f := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
		ppid, _ := strconv.Atoi(f[1])
		m[ppid] = append(m[ppid], pid)
	}
	return m
}

func descendants(root int) []int {
	m := children()
	out := []int{root}
	for i := 0; i < len(out); i++ {
		out = append(out, m[out[i]]...)
	}
	return out
}

// killTree stops everything first so nothing escapes, then kills it, as a
// systemd unit stop would. It returns how many git-side processes were alive.
func killTree(root int) int {
	stopped := map[int]bool{}
	for {
		fresh := false
		for _, p := range descendants(root) {
			if !stopped[p] {
				syscall.Kill(p, syscall.SIGSTOP)
				stopped[p], fresh = true, true
			}
		}
		if !fresh {
			break
		}
	}
	for p := range stopped {
		syscall.Kill(p, syscall.SIGKILL)
	}
	for p := range stopped {
		for i := 0; i < 500 && p != root; i++ {
			raw, err := os.ReadFile("/proc/" + strconv.Itoa(p) + "/stat")
			if err != nil || strings.Contains(string(raw), ") Z ") {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	return len(stopped) - 1
}

func TestKillDuringPushLosesNothingAcknowledged(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	s := start(t)
	s.stop()
	var cmd *exec.Cmd
	boot := func() {
		cmd = exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), serveEnv+"="+s.data.Dir)
		out := must(cmd.StdoutPipe())
		check(cmd.Start())
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if addr, ok := strings.CutPrefix(sc.Text(), "ADDR "); ok {
				s.url = "http://" + addr + "/p.git"
				return
			}
		}
		t.Fatal("server process did not start")
	}
	kill := func() int {
		n := killTree(cmd.Process.Pid)
		cmd.Wait()
		return n
	}
	boot()
	defer func() { kill() }()
	b := newBuilder()
	cur, co := b.simple("root", []byte("root"))
	s.mustPush(t, b.pack(co), plumbing.ZeroHash, cur)
	payload := make([]byte, 1<<20)
	// Time one push, so kills spread over a push on this machine.
	rand.Read(payload)
	b = newBuilder()
	timed, to := b.simple("timed", payload, cur)
	t0 := time.Now()
	s.mustPush(t, b.pack(to), cur, timed)
	window := int(time.Since(t0)*5/4) + 1
	cur = timed
	var interrupted, midGit int
	const trials = 20
	for i := range trials {
		rand.Read(payload)
		b = newBuilder()
		next, no := b.simple(fmt.Sprint("k", i), payload, cur)
		body := requestBody(b.pack(no), cmdOf(cur, next, mainRef.String()))
		result := make(chan bool, 1)
		go func() {
			code, out, err := s.send(body, s.token)
			result <- err == nil && accepted(code, out)
		}()
		// From the start of a push to well after a typical one.
		time.Sleep(time.Duration(mrand.IntN(window)))
		if kill() > 0 {
			midGit++
		}
		acked := <-result
		boot()
		fsck(t, s.repo())
		h := s.head()
		switch {
		case acked && h != next:
			t.Fatalf("trial %d: an acknowledged push was lost", i)
		case h != cur && h != next:
			t.Fatalf("trial %d: head is neither old nor new", i)
		}
		if !acked {
			interrupted++
		}
		cur = h
	}
	// The server must still accept work after the last crash.
	b = newBuilder()
	last, lo := b.simple("last", []byte("last"), cur)
	s.mustPush(t, b.pack(lo), cur, last)
	t.Logf("%d kills over %v: %d before the reply, %d while git was running",
		trials, time.Duration(window), interrupted, midGit)
}
