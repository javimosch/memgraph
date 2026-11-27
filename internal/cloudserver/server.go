package cloudserver

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/javimosch/memgraph/internal/fsx"
)

var repoPath = regexp.MustCompile(`^/([a-z0-9][a-z0-9-]{0,62})\.git/(info/refs|git-upload-pack|git-receive-pack)$`)

// Limits bound what one request may cost.
type Limits struct {
	Body, Object, Total int64 // bytes: request, one object, all objects of a push
	Objects             int
	Receives, Uploads   int // requests handled at once
	Seconds             int // for one git process
}

// DefaultLimits are the limits of docs/cloud-server.md.
var DefaultLimits = Limits{Body: 32 << 20, Object: 16 << 20, Total: 128 << 20,
	Objects: 100_000, Receives: 2, Uploads: 4, Seconds: 60}

// Options configure a server.
type Options struct {
	DataDir string
	Limits  Limits
	// RepackEvery runs maintenance after this many pushes; 0 never does.
	RepackEvery int
	// HookCommand runs PreReceive on its standard input, program first.
	HookCommand []string
}

type project struct {
	pushes sync.RWMutex // pushes share it; repack takes it exclusively
	mu     sync.Mutex
	count  int
	repack bool
}

// Server is an http.Handler for the projects of one data directory.
type Server struct {
	data        Data
	lim         Limits
	repackEvery int

	git, timeout string
	env          []string
	receives     chan struct{}
	uploads      chan struct{}
	projects     sync.Map
	maintenance  sync.WaitGroup
	unlock       func()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// New prepares a server. Only one may use a data directory at a time.
func New(opts Options) (_ *Server, err error) {
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, err
	}
	if len(opts.HookCommand) == 0 {
		return nil, errors.New("a hook command is required: without it any push would be accepted")
	}
	s := &Server{data: Data{Dir: dir}, lim: opts.Limits, repackEvery: opts.RepackEvery,
		receives: make(chan struct{}, opts.Limits.Receives), uploads: make(chan struct{}, opts.Limits.Uploads)}
	if s.git, err = exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("the server needs git: %w", err)
	}
	if s.timeout, err = exec.LookPath("timeout"); err != nil {
		return nil, fmt.Errorf("the server needs coreutils timeout: %w", err)
	}
	if err := s.data.init(); err != nil {
		return nil, err
	}
	if s.unlock, err = fsx.TryLockFile(filepath.Join(dir, "server.lock")); err != nil {
		return nil, fmt.Errorf("another server uses this data dir: %w", err)
	}
	defer func() {
		if err != nil {
			s.unlock()
		}
	}()
	tmp, home, hooks := filepath.Join(dir, "tmp"), filepath.Join(dir, "home"), filepath.Join(dir, "hooks")
	if err := os.RemoveAll(tmp); err != nil {
		return nil, err
	}
	for _, d := range []string{tmp, home, hooks} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	script := "#!/bin/sh\nexec"
	for _, arg := range opts.HookCommand {
		script += " " + shellQuote(arg)
	}
	if err := fsx.WriteFileAtomic(filepath.Join(hooks, "pre-receive"), []byte(script+"\n"), 0700); err != nil {
		return nil, err
	}

	// All git settings come from here, never from user or system config.
	cfg := [][2]string{
		{"core.hooksPath", hooks},
		{"core.fsyncObjectFiles", "true"},
		{"http.receivepack", "true"},
		{"receive.fsckObjects", "true"},
		{"receive.denyDeletes", "true"},
		{"receive.denyNonFastForwards", "true"},
		{"receive.maxInputSize", strconv.FormatInt(s.lim.Body, 10)},
		{"receive.autogc", "false"},
		{"gc.auto", "0"},
		{"pack.threads", "1"},
		{"pack.windowMemory", "32m"},
		{"pack.deltaCacheSize", "16m"},
		{"core.packedGitWindowSize", "16m"},
		{"core.packedGitLimit", "64m"},
	}
	s.env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=" + strconv.Itoa(len(cfg))}
	for i, kv := range cfg {
		s.env = append(s.env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	return s, s.recover()
}

// Close waits for maintenance and releases the data directory. Stop serving
// requests first.
func (s *Server) Close() {
	s.maintenance.Wait()
	s.unlock()
}

// recover removes what a killed git leaves behind. A stale main.lock would
// block every later push. Only safe because no git process of an earlier run
// is alive: run the server so that stopping it kills its children (a systemd
// unit does) and never touch the repos by hand while it runs.
func (s *Server) recover() error {
	return filepath.WalkDir(s.data.repos(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		stale := strings.HasSuffix(name, ".lock") || strings.HasPrefix(name, "incoming-") ||
			strings.HasPrefix(name, "tmp_") || strings.HasPrefix(name, ".tmp-")
		if !stale {
			return nil
		}
		log.Printf("recovery: removing %s", p)
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

func (s *Server) project(name string) *project {
	p, _ := s.projects.LoadOrStore(name, &project{})
	return p.(*project)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		fmt.Fprintln(w, "ok")
		return
	}
	m := repoPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	var projects []string
	_, secret, ok := r.BasicAuth()
	if ok {
		projects, ok = s.data.authorize(secret)
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="memgraph"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name, endpoint := m[1], m[2]
	if !slices.Contains(projects, name) || !s.data.hasProject(name) {
		http.NotFound(w, r)
		return
	}
	// Only these exact requests reach git.
	switch {
	case endpoint == "info/refs" && r.Method == http.MethodGet:
		q := r.URL.RawQuery
		if q != "service=git-upload-pack" && q != "service=git-receive-pack" {
			http.NotFound(w, r)
			return
		}
	case endpoint != "info/refs" && r.Method == http.MethodPost && r.URL.RawQuery == "" &&
		r.Header.Get("Content-Type") == "application/x-"+endpoint+"-request":
	default:
		http.NotFound(w, r)
		return
	}
	if err := s.serveGit(w, r, name, endpoint); err != nil {
		log.Printf("%s %s: %v", name, endpoint, err)
		code := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "request rejected", code)
	}
}

// spool writes the decompressed, size-limited body to a file. Go's CGI handler
// refuses chunked requests, and git must never see an unbounded gzip stream.
func (s *Server) spool(w http.ResponseWriter, r *http.Request) (*os.File, int64, error) {
	var rd io.Reader = http.MaxBytesReader(w, r.Body, s.lim.Body)
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "":
	case "gzip", "x-gzip":
		z, err := gzip.NewReader(rd)
		if err != nil {
			return nil, 0, err
		}
		rd = http.MaxBytesReader(w, z, s.lim.Body)
	default:
		return nil, 0, errors.New("unsupported content encoding")
	}
	f, err := os.CreateTemp(filepath.Join(s.data.Dir, "tmp"), "body-")
	if err != nil {
		return nil, 0, err
	}
	os.Remove(f.Name()) // lives only as long as the descriptor
	n, err := io.Copy(f, rd)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, n, nil
}

type counter struct {
	http.ResponseWriter
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.ResponseWriter.Write(p)
}

func (s *Server) serveGit(w http.ResponseWriter, r *http.Request, name, endpoint string) error {
	var body *os.File
	var size int64
	if r.Method == http.MethodPost {
		slots := s.uploads
		if endpoint == "git-receive-pack" {
			slots = s.receives
		}
		var err error
		if body, size, err = s.spool(w, r); err != nil {
			return err
		}
		defer body.Close()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-time.After(10 * time.Second):
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return nil
		}
	}
	p := s.project(name)
	if endpoint == "git-receive-pack" {
		if err := s.prescan(body, size); err != nil {
			return err
		}
		p.pushes.RLock()
		defer p.pushes.RUnlock()
	}

	r2 := r.Clone(r.Context())
	for _, h := range []string{"Authorization", "Content-Encoding", "Proxy"} {
		r2.Header.Del(h) // the token never enters a child's environment
	}
	r2.TransferEncoding = nil
	r2.ContentLength = size
	r2.Body = http.NoBody
	if body != nil {
		r2.Body = io.NopCloser(body)
	}
	// coreutils timeout bounds the whole git process group.
	h := &cgi.Handler{
		Path: s.timeout,
		Args: []string{"--signal=KILL", strconv.Itoa(s.lim.Seconds), s.git, "http-backend"},
		Env: append(slices.Clone(s.env),
			"GIT_PROJECT_ROOT="+s.data.repos(),
			"GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=memgraph"),
		Logger: log.Default(),
		Stderr: log.Writer(),
	}
	cw := &counter{ResponseWriter: w}
	h.ServeHTTP(cw, r2)
	if endpoint == "git-upload-pack" {
		log.Printf("%s upload-pack: sent %d bytes", name, cw.n)
	}
	if endpoint == "git-receive-pack" {
		s.afterPush(name, p)
	}
	return nil
}

// afterPush repacks in the background. Pushes wait for it, fetches do not.
func (s *Server) afterPush(name string, p *project) {
	if s.repackEvery <= 0 {
		return
	}
	p.mu.Lock()
	p.count++
	due := p.count%s.repackEvery == 0 && !p.repack
	p.repack = p.repack || due
	p.mu.Unlock()
	if !due {
		return
	}
	s.maintenance.Add(1)
	go func() {
		defer s.maintenance.Done()
		p.pushes.Lock() // waits for the triggering push to release its read lock
		defer p.pushes.Unlock()
		start := time.Now()
		// A push that loses the ref race has already left its objects behind,
		// so prune too. Both are safe because no push is running.
		var out []byte
		var err error
		for _, args := range [][]string{{"repack", "-adq"}, {"prune", "--expire=now"}} {
			cmd := exec.Command(s.git, append([]string{"-C", s.data.repo(name)}, args...)...)
			cmd.Env = s.env
			if out, err = cmd.CombinedOutput(); err != nil {
				break
			}
		}
		log.Printf("repack %s: %v err=%v %s", name, time.Since(start), err, out)
		p.mu.Lock()
		p.repack = false
		p.mu.Unlock()
	}()
}
