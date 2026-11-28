package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/javimosch/memgraph/internal/cloudserver"
)

const cloudServerUsage = `memgraph cloud, server side (run on the machine that hosts the projects)

  cloud serve --data <dir> [--listen 127.0.0.1:8787] [--client-ip-header X-Forwarded-For]
  cloud admin project create <name> --data <dir>
  cloud admin project list --data <dir>
  cloud admin token create --name <machine> --project <p> [--project <p>] --data <dir>
  cloud admin token list --data <dir>
  cloud admin token revoke <id> --data <dir>
  cloud admin backup --data <dir> [--out <file>]          (default: stdout)
  cloud admin restore --data <new dir> [--in <file>] [--without-tokens]

--data may also come from MEMGRAPH_CLOUD_DATA. Run admin commands as the same
user as the server. See docs/cloud-server.md.`

// isCloudServerCommand reports whether args (after "cloud") are server side.
// These run before any local config or registry is touched: the push hook is
// started by git, and a server has no memories of its own.
func isCloudServerCommand(args []string) bool {
	// The first word decides. Flags may come first, because main() moves
	// global flags behind "cloud".
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--data" || arg == "--listen" || arg == "--name" || arg == "--project" ||
			arg == "--server" || arg == "--repack-every" || arg == "--memory-dir" || arg == "--client-ip-header" || arg == "--out" || arg == "--in":
			i++
		case !strings.HasPrefix(arg, "-"):
			return arg == "serve" || arg == "admin" || arg == "hook" || arg == "limit"
		}
	}
	return false
}

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

type serverArgs struct {
	words              []string
	data, listen, name string
	projects           repeated
	repackEvery        int
	clientIPHeader     string
	out, in            string
	withoutTokens      bool
	allowNonLoopback   bool
}

// parseServerArgs accepts flags before, between and after the words.
func parseServerArgs(args []string) (serverArgs, error) {
	var a serverArgs
	fs := flag.NewFlagSet("cloud", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.data, "data", os.Getenv("MEMGRAPH_CLOUD_DATA"), "")
	fs.StringVar(&a.listen, "listen", "127.0.0.1:8787", "")
	fs.StringVar(&a.name, "name", "", "")
	fs.Var(&a.projects, "project", "")
	fs.IntVar(&a.repackEvery, "repack-every", 500, "")
	fs.StringVar(&a.out, "out", "", "")
	fs.StringVar(&a.in, "in", "", "")
	fs.BoolVar(&a.withoutTokens, "without-tokens", false, "")
	fs.StringVar(&a.clientIPHeader, "client-ip-header", "", "")
	fs.BoolVar(&a.allowNonLoopback, "allow-non-loopback", false, "")
	for _, global := range []string{"json", "j", "y", "no-interactive"} {
		fs.Bool(global, false, "") // handled by main
	}
	for {
		if err := fs.Parse(args); err != nil {
			return a, err
		}
		if fs.NArg() == 0 {
			return a, nil
		}
		a.words = append(a.words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// checkListen keeps plain HTTP, which carries tokens, on this machine.
func checkListen(addr string, allowNonLoopback bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if allowNonLoopback || host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("the server speaks plain HTTP: listen on loopback behind a TLS proxy, " +
		"or pass --allow-non-loopback if the network path is already private")
}

func handleCloudServer(args []string) {
	// The server starts this to run git. What follows is git's, not flags of
	// ours.
	if len(args) > 0 && args[0] == "limit" {
		os.Exit(cloudserver.Limit(args[1:]))
	}
	// git starts the hook with exactly these arguments; check them first.
	if len(args) > 0 && args[0] == "hook" {
		if len(args) != 2 || args[1] != "pre-receive" {
			os.Exit(1) // a hook that does not understand must refuse
		}
		if err := cloudserver.PreReceive(os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, "memgraph:", err)
			os.Exit(1)
		}
		return
	}
	a, err := parseServerArgs(args)
	if err != nil {
		cloudFail(85, "invalid_argument", err)
	}
	if a.words[0] == "hook" {
		os.Exit(1)
	}
	if a.data == "" {
		cloudFail(80, "missing_argument", errors.New("--data <dir> is required\n\n"+cloudServerUsage))
	}
	if a.words[0] == "serve" {
		if len(a.words) != 1 {
			cloudFail(85, "invalid_argument", errors.New("cloud serve takes only options"))
		}
		if err := serveCloud(a); err != nil {
			cloudFail(110, "server", err)
		}
		return
	}
	adminCloud(cloudserver.Data{Dir: a.data}, a)
}

func adminCloud(data cloudserver.Data, a serverArgs) {
	usage := func() { cloudFail(85, "invalid_argument", errors.New("unknown admin command\n\n"+cloudServerUsage)) }
	if len(a.words) == 2 && (a.words[1] == "backup" || a.words[1] == "restore") {
		if err := backupOrRestore(data, a.words[1], a); err != nil {
			cloudFail(110, a.words[1], err)
		}
		return
	}
	if len(a.words) < 3 {
		usage()
	}
	what, rest := a.words[1]+" "+a.words[2], a.words[3:]
	one := func() string {
		if len(rest) != 1 {
			usage()
		}
		return rest[0]
	}
	none := func() {
		if len(rest) != 0 {
			usage()
		}
	}
	fail := func(err error) {
		if err != nil {
			cloudFail(110, "admin", err)
		}
	}
	switch what {
	case "project create":
		name := one()
		fail(data.CreateProject(name))
		cloudOutput(map[string]any{"status": "created", "project": name}, func() {
			fmt.Printf("Created project %s\n", name)
		})
	case "project list":
		none()
		names, err := data.Projects()
		fail(err)
		cloudOutput(map[string]any{"projects": names}, func() {
			for _, n := range names {
				fmt.Println(n)
			}
		})
	case "token create":
		none()
		secret, token, err := data.CreateToken(a.name, a.projects)
		fail(err)
		cloudOutput(map[string]any{"id": token.ID, "name": token.Name, "projects": token.Projects, "token": secret}, func() {
			// Only the secret goes to stdout, so it can be piped.
			fmt.Fprintf(os.Stderr, "Token %s for %s (%s). It is shown only now:\n",
				token.ID, token.Name, strings.Join(token.Projects, ", "))
			fmt.Println(secret)
		})
	case "token list":
		none()
		tokens, err := data.Tokens()
		fail(err)
		rows := []map[string]any{} // without hashes
		for _, t := range tokens {
			rows = append(rows, map[string]any{"id": t.ID, "name": t.Name, "projects": t.Projects, "created": t.Created})
		}
		cloudOutput(map[string]any{"tokens": rows}, func() {
			for _, t := range tokens {
				fmt.Printf("%s  %s  %s  %s\n", t.ID, t.Created.Format(time.DateOnly), t.Name, strings.Join(t.Projects, ","))
			}
		})
	case "token revoke":
		id := one()
		fail(data.RevokeToken(id))
		cloudOutput(map[string]any{"status": "revoked", "id": id}, func() { fmt.Printf("Revoked token %s\n", id) })
	default:
		usage()
	}
}

func serveCloud(a serverArgs) error {
	if err := checkListen(a.listen, a.allowNonLoopback); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	srv, err := cloudserver.New(cloudserver.Options{DataDir: a.data, Limits: cloudserver.DefaultLimits,
		RepackEvery: a.repackEvery, HookCommand: []string{self, "cloud", "hook", "pre-receive"},
		LimitCommand:   []string{self, "cloud", "limit"},
		ClientIPHeader: a.clientIPHeader})
	if err != nil {
		return err
	}
	defer srv.Close()
	ln, err := net.Listen("tcp", a.listen)
	if err != nil {
		return err
	}
	hs := srv.HTTPServer()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- hs.Serve(ln) }()
	log.Printf("memgraph cloud listening on %s", ln.Addr())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	log.Print("shutting down")
	wait, cancel := context.WithTimeout(context.Background(), cloudserver.DefaultLimits.Write)
	defer cancel()
	return hs.Shutdown(wait)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// backupTo writes a backup to a new file, which appears only when complete.
func backupTo(data cloudserver.Data, path string) (summary *cloudserver.BackupSummary, err error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".backup-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	summary, err = data.Backup(f)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	// Link fails if the name appeared meanwhile, where Rename would replace it.
	return summary, os.Link(f.Name(), path)
}

// backupOrRestore reports on stderr: stdout may be the backup itself.
func backupOrRestore(data cloudserver.Data, what string, a serverArgs) error {
	report := func(verb string, s *cloudserver.BackupSummary) {
		fmt.Fprintf(os.Stderr, "%s %d project(s) and %d token(s)\n", verb, len(s.Projects), s.Tokens)
		for _, p := range s.Projects {
			fmt.Fprintf(os.Stderr, "  %s %s\n", p.Name, cmp.Or(p.Head, "(empty)"))
		}
	}
	if what == "backup" {
		if a.out != "" {
			s, err := backupTo(data, a.out)
			if err == nil {
				report("Backed up", s)
			}
			return err
		}
		if isTerminal(os.Stdout) {
			return errors.New("a backup is binary: redirect stdout or pass --out <file>")
		}
		s, err := data.Backup(os.Stdout)
		if err == nil {
			report("Backed up", s)
		}
		return err
	}
	in := os.Stdin
	if a.in != "" {
		f, err := os.Open(a.in)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	} else if isTerminal(os.Stdin) {
		return errors.New("pass --in <file> or pipe a backup to stdin")
	}
	s, err := data.Restore(in, !a.withoutTokens)
	if err != nil {
		return err
	}
	report("Restored", s)
	if s.Tokens > 0 {
		fmt.Fprintln(os.Stderr, "Tokens revoked after this backup work again. Check 'cloud admin token list' and revoke them.")
	}
	return nil
}
