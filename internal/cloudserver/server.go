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
	Seconds             int // for one git process serving a request
	MaintenanceSeconds  int // for one git process doing maintenance
	// Spools is how many request bodies may be on disk at once, and so bounds
	// that disk to Spools times Body.
	Spools     int
	Wait       time.Duration // for a free slot, before answering "busy"
	AuthFails  int           // failed logins per client per AuthWindow
	AuthWindow time.Duration
	// Connection timeouts, which stop a slow client from holding a slot.
	ReadHeader, Read, Write, Idle time.Duration
}

// DefaultLimits are the limits of docs/cloud-server.md.
var DefaultLimits = Limits{Body: 32 << 20, Object: 16 << 20, Total: 128 << 20,
	Objects: 100_000, Receives: 2, Uploads: 4, Seconds: 60, MaintenanceSeconds: 600,
	Spools: 8, Wait: 10 * time.Second, AuthFails: 20, AuthWindow: time.Minute,
	// Write starts when the headers are read, so it has to cover receiving
	// the body, waiting for a slot and running git.
	ReadHeader: 10 * time.Second, Read: 60 * time.Second, Write: 180 * time.Second, Idle: 60 * time.Second}

// Options configure a server.
type Options struct {
	DataDir string
	Limits  Limits
	// RepackEvery runs maintenance after this many pushes; 0 never does.
	RepackEvery int
	// HookCommand runs PreReceive on its standard input, program first.
	HookCommand []string
	// ClientIPHeader names the header in which a trusted proxy puts the
	// client's address, such as X-Forwarded-For. Empty trusts no header.
	ClientIPHeader string
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
	spools       chan struct{}
	logins       *limiter
	ipHeader     string

	hookPath, hookScript string
	projects             sync.Map
	maintenance          sync.WaitGroup
	unlock               func()
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
		receives: make(chan struct{}, opts.Limits.Receives), uploads: make(chan struct{}, opts.Limits.Uploads),
		spools: make(chan struct{}, opts.Limits.Spools), ipHeader: opts.ClientIPHeader,
		logins: newLimiter(opts.Limits.AuthFails, opts.Limits.AuthWindow)}
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
	s.hookPath, s.hookScript = filepath.Join(hooks, "pre-receive"), script+"\n"
	if err := fsx.WriteFileAtomic(s.hookPath, []byte(s.hookScript), 0700); err != nil {
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
	if err := s.recover(); err != nil {
		return nil, err
	}
	if err := s.selfCheck(); err != nil {
		return nil, fmt.Errorf("startup self-check failed: %w", err)
	}
	return s, s.resumeMaintenance()
}

// HTTPServer returns an http.Server for s with the connection timeouts set.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Handler: s, ReadHeaderTimeout: s.lim.ReadHeader,
		ReadTimeout: s.lim.Read, WriteTimeout: s.lim.Write, IdleTimeout: s.lim.Idle}
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
	client := clientOf(r, s.ipHeader)
	if client != "" && s.logins.blocked(client) {
		tooMany(w, s.lim.AuthWindow)
		return
	}
	var projects []string
	_, secret, ok := r.BasicAuth()
	if ok {
		// A valid token from an unidentified client is never refused for what
		// others did.
		if projects, ok = s.data.authorize(secret); !ok && s.logins.fail(client) {
			tooMany(w, s.lim.AuthWindow)
			return
		}
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

func tooMany(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()))))
	http.Error(w, "too many failed logins", http.StatusTooManyRequests)
}

// take waits for a free slot and returns how to give it back, or answers
// "busy" and returns nil.
func (s *Server) take(w http.ResponseWriter, r *http.Request, slots chan struct{}) func() {
	timer := time.NewTimer(s.lim.Wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }
	case <-timer.C:
	case <-r.Context().Done():
	}
	w.Header().Set("Retry-After", "5")
	http.Error(w, "busy", http.StatusServiceUnavailable)
	return nil
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
		// Taken before reading, so uploads cannot fill the disk.
		done := s.take(w, r, s.spools)
		if done == nil {
			return nil
		}
		defer done()
		var err error
		if body, size, err = s.spool(w, r); err != nil {
			return err
		}
		defer body.Close()
		slots := s.uploads
		if endpoint == "git-receive-pack" {
			slots = s.receives
		}
		free := s.take(w, r, slots)
		if free == nil {
			return nil
		}
		defer free()
	}
	p := s.project(name)
	if endpoint == "git-receive-pack" && r.Method == http.MethodPost {
		if err := s.prescan(body, size); err != nil {
			return err
		}
		p.pushes.RLock()
		defer p.pushes.RUnlock()
		if err := s.verifyHook(); err != nil {
			log.Printf("REFUSING PUSHES, restart the server: %v", err)
			http.Error(w, "server cannot enforce its push rules", http.StatusServiceUnavailable)
			return nil
		}
	}
	cw := &counter{ResponseWriter: w}
	s.backend(cw, r, body, size, log.Writer())
	if endpoint == "git-upload-pack" {
		log.Printf("%s upload-pack: sent %d bytes", name, cw.n)
	}
	if endpoint == "git-receive-pack" && r.Method == http.MethodPost {
		s.afterPush(name, p)
	}
	return nil
}

// backend runs git http-backend for one checked request. What git and the hook
// print goes to stderr.
func (s *Server) backend(w http.ResponseWriter, r *http.Request, body *os.File, size int64, stderr io.Writer) {
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
		Stderr: stderr,
	}
	h.ServeHTTP(w, r2)
}
