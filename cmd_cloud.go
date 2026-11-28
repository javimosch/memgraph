package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/javimosch/memgraph/internal/cloudsync"
	"github.com/javimosch/memgraph/internal/fsx"
)

// Exit codes specific to cloud sync.
const (
	exitConflicts = 93 // synced, but conflicts wait for a decision
	exitBlocked   = 94 // a guard stopped the sync
)

var cloudProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

const cloudUsage = `memgraph cloud: sync a project between machines (opt-in per project)

  cloud login <url>              store a server token, read from stdin
  cloud enable --project <p>     upload a local project to an empty server project
  cloud clone <p>                join a server project on this machine
  cloud sync [--project <p>]     sync one project, or every linked one
  cloud status                   linked projects and what is pending
  cloud conflicts [show|restore|resolve <n>] --project <p>
  cloud local <id> --project <p>    keep a memory on this machine (before its first sync)
  cloud unlocal <id> --project <p>  let it sync again
  cloud allow <fingerprint> --project <p>   accept a reported secret finding
  cloud disable --project <p>    unlink; keeps local files and the server copy

Options: --server <url>, --allow-mass-delete, -y (no confirmation)
         --allow-finding <fingerprint>  with enable or clone, repeatable

cloud local also works before enable, to keep a memory out of the first upload.
For a deleted memory it keeps only the ledger history here.

Server side: cloud serve, cloud admin (see 'memgraph cloud server-help')`

type cloudArgs struct {
	words           []string
	project, server string
	allowMassDelete bool
	// allow accepts findings while joining, before there is a link to store them in.
	allow []string
}

func parseCloudArgs(args []string) (cloudArgs, error) {
	var a cloudArgs
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		switch name {
		case "--project", "--server", "--memory-dir", "--allow-finding":
			if !inline {
				if i+1 >= len(args) {
					return a, fmt.Errorf("%s needs a value", name)
				}
				i++
				value = args[i]
			}
			switch name {
			case "--project":
				a.project = value
			case "--server":
				a.server = value
			case "--allow-finding":
				a.allow = append(a.allow, value)
			}
		case "--allow-mass-delete":
			a.allowMassDelete = true
		case "--json", "-j", "-y", "--no-interactive":
		default:
			// An ignored typo could change what a command does.
			if strings.HasPrefix(args[i], "-") {
				return a, fmt.Errorf("unknown option %s", args[i])
			}
			a.words = append(a.words, args[i])
		}
	}
	return a, nil
}

func cloudRoot() string { return filepath.Join(getGlobalMemgraphDir(), "sync") }

func cloudStore(project string) cloudsync.Store {
	return cloudsync.Store{Dir: filepath.Join(cloudRoot(), project)}
}

// linkedProjects returns every project linked on this machine.
func linkedProjects() map[string]cloudsync.Link {
	out := map[string]cloudsync.Link{}
	entries, _ := os.ReadDir(cloudRoot())
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if link, err := cloudStore(e.Name()).Link(); err == nil {
			out[e.Name()] = link
		}
	}
	return out
}

func samePath(a, b string) bool {
	resolve := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// refuseIfLinked guards operations that delete and rewrite memory files in
// bulk, which sync would carry to every machine.
func refuseIfLinked(dir string) error {
	if os.Getenv("MEMGRAPH_ALLOW_INGEST_LINKED") == "1" {
		return nil
	}
	for name, link := range linkedProjects() {
		if samePath(link.MemoryDir, dir) {
			return fmt.Errorf("%s is synced as cloud project %q and ingest would delete its memories; "+
				"use another directory, or run 'memgraph cloud disable --project %s'", dir, name, name)
		}
	}
	return nil
}

type cloudCredentials struct {
	Default string            `json:"default_server"`
	Tokens  map[string]string `json:"tokens"`
}

func credentialsPath() string { return filepath.Join(cloudRoot(), "credentials.json") }

func loadCredentials() (cloudCredentials, error) {
	c := cloudCredentials{Tokens: map[string]string{}}
	raw, err := os.ReadFile(credentialsPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(raw, &c)
}

func saveCredentials(c cloudCredentials) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cloudRoot(), 0700); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(credentialsPath(), raw, 0600)
}

// tokenFor returns the token of the server a project URL belongs to.
func tokenFor(url string) (string, error) {
	c, err := loadCredentials()
	if err != nil {
		return "", err
	}
	for server, token := range c.Tokens {
		if strings.HasPrefix(url, server+"/") {
			return token, nil
		}
	}
	return "", errors.New("no token for this server; run 'memgraph cloud login <url>'")
}

func cloudOptions(url string, a cloudArgs) (cloudsync.Options, error) {
	token, err := tokenFor(url)
	return cloudsync.Options{Token: token, AllowMassDelete: a.allowMassDelete,
		Content: func(name string, data []byte) string { return parseMemory(string(data), name).Content }}, err
}

func cloudFail(code int, kind string, err error) {
	if jsonOutput {
		errorResponse(code, kind, err.Error(), code != 110)
	} else {
		fmt.Fprintln(os.Stderr, "memgraph cloud:", err)
	}
	os.Exit(code)
}

func cloudOutput(v any, human func()) {
	if jsonOutput {
		json.NewEncoder(os.Stdout).Encode(v)
		return
	}
	human()
}

func printSync(project string, r *cloudsync.Result) {
	list := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		fmt.Printf("  %s (%d):\n", label, len(names))
		for i, n := range names {
			if i == 20 {
				fmt.Printf("    ... and %d more\n", len(names)-i)
				break
			}
			fmt.Println("   ", n)
		}
	}
	fmt.Printf("%s:\n", project)
	list("upload", r.Uploaded)
	list("delete on server", r.DeletedRemote)
	list("download", r.Downloaded)
	list("delete here", r.DeletedLocal)
	list("held (local-only here, already on server)", r.Held)
	list("changed during sync, left for next time", r.Deferred)
	list("not syncable, unsupported filename", r.Ignored)
	for _, c := range r.Conflicts {
		fmt.Printf("  conflict %d: %s (%s)\n", c.ID, c.Name, c.Kind)
	}
}

// syncError reports a failed sync and returns the exit code for it.
func syncError(project string, joining bool, err error) int {
	var blocked *cloudsync.BlockedError
	code := 110
	if errors.As(err, &blocked) {
		code = exitBlocked
	}
	if jsonOutput {
		out := map[string]any{"project": project, "error": err.Error(), "code": code}
		if blocked != nil {
			out["findings"] = blocked.Findings
		}
		json.NewEncoder(os.Stdout).Encode(out)
		return code
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", project, err)
	if blocked != nil {
		for _, f := range blocked.Findings {
			fmt.Fprintf(os.Stderr, "  %s:%d %s, memory %s (fingerprint %s)\n", f.File, f.Line, f.Rule,
				cmp.Or(f.MemoryID, "unknown"), f.Fingerprint)
		}
		if len(blocked.Findings) > 0 {
			accept := "'cloud allow <fingerprint> --project " + project + "'"
			if joining {
				accept = "repeat the command with '--allow-finding <fingerprint>'"
			}
			fmt.Fprintf(os.Stderr, "Remove it, run 'cloud local <id> --project %s', or %s.\n", project, accept)
			fmt.Fprintln(os.Stderr, "Deleting a memory copies its text into the ledger. For a deleted one, 'cloud local <id>' "+
				"keeps that history here. Replace any real secret too.")
		}
	}
	return code
}

// syncOne syncs a linked project and returns an exit code. enabling marks the
// first sync of 'cloud enable'.
func syncOne(cfg *Config, project string, a cloudArgs, enabling bool) int {
	store := cloudStore(project)
	link, err := store.Link()
	if err != nil {
		return syncError(project, false, errors.New("not linked"))
	}
	opts, err := cloudOptions(link.URL, a)
	if err != nil {
		return syncError(project, false, err)
	}
	opts.RequireEmptyServer = enabling
	res, err := cloudsync.Sync(store, opts)
	if errors.Is(err, cloudsync.ErrServerNotEmpty) {
		// Another machine got there first. Nothing was synced, so unlink.
		if rerr := os.RemoveAll(store.Dir); rerr != nil {
			return syncError(project, false, rerr)
		}
		return syncError(project, false, notEmpty(project))
	}
	if err != nil {
		return syncError(project, false, err)
	}
	st, err := store.Load()
	if err != nil {
		return syncError(project, false, err)
	}
	open := len(st.Open())
	cloudOutput(map[string]any{"project": project, "result": res, "open_conflicts": open}, func() {
		printSync(project, res)
		if open > 0 {
			fmt.Printf("  %d open conflict(s): memgraph cloud conflicts --project %s\n", open, project)
		}
	})
	if open > 0 {
		return exitConflicts
	}
	return 0
}

func confirmCloud(question string) bool {
	if noInteractive {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func notEmpty(project string) error {
	return fmt.Errorf("server project %q already has history; join it with 'memgraph cloud clone %s'", project, project)
}

// join links a project after showing what the first sync would do.
func join(cfg *Config, a cloudArgs, project, dir string, enabling bool) int {
	creds, err := loadCredentials()
	if err != nil {
		cloudFail(110, "credentials", err)
	}
	server := strings.TrimRight(a.server, "/")
	if server == "" {
		server = creds.Default
	}
	if server == "" {
		cloudFail(85, "invalid_argument", errors.New("no server: run 'memgraph cloud login <url>' or pass --server"))
	}
	link := cloudsync.Link{Project: project, URL: server + "/" + project + ".git"}
	if link.MemoryDir, err = filepath.Abs(dir); err != nil {
		cloudFail(110, "path", err)
	}
	if err := cloudsync.CheckURL(link.URL); err != nil {
		cloudFail(85, "invalid_argument", err)
	}
	for name, other := range linkedProjects() {
		if name == project || samePath(other.MemoryDir, link.MemoryDir) {
			cloudFail(86, "already_linked", fmt.Errorf("already linked as %q", name))
		}
	}
	store := cloudStore(project)
	// State left over from an earlier failed or disabled link must not be reused.
	if _, err := os.Stat(filepath.Join(store.Dir, "state.json")); err == nil {
		cloudFail(86, "stale_state", fmt.Errorf("old sync state exists in %s", store.Dir))
	}
	opts, err := cloudOptions(link.URL, a)
	if err != nil {
		cloudFail(85, "not_logged_in", err)
	}

	// The preview uses a throwaway state, so declining leaves nothing behind.
	tmp, err := os.MkdirTemp("", "memgraph-preview-")
	if err != nil {
		cloudFail(110, "tmp", err)
	}
	preview := cloudsync.Store{Dir: tmp}
	var res *cloudsync.Result
	allowed := &cloudsync.State{Allowed: map[string]bool{}}
	for _, fp := range a.allow {
		allowed.Allowed[fp] = true
	}
	if err = errors.Join(preview.SaveLink(link), preview.Save(allowed)); err == nil {
		dry := opts
		dry.DryRun, dry.RequireEmptyServer = true, enabling
		res, err = cloudsync.Sync(preview, dry)
	}
	os.RemoveAll(tmp)
	if errors.Is(err, cloudsync.ErrServerNotEmpty) {
		cloudFail(86, "not_empty", notEmpty(project))
	}
	if err != nil {
		return syncError(project, true, err)
	}
	if !jsonOutput {
		printSync(project, res)
		fmt.Printf("  memory dir: %s\n  server:     %s\n", link.MemoryDir, link.URL)
	}
	if !confirmCloud("Link and sync?") {
		fmt.Fprintln(os.Stderr, "Nothing changed.")
		return 0
	}
	if err := os.MkdirAll(link.MemoryDir, 0755); err != nil {
		cloudFail(110, "mkdir", err)
	}
	if err := store.SaveLink(link); err != nil {
		cloudFail(110, "link", err)
	}
	if len(a.allow) > 0 {
		if err := store.Save(allowed); err != nil {
			cloudFail(110, "state", err)
		}
	}
	reg := loadRegistry()
	if reg.lookup(project) == "" {
		reg.register(project, link.MemoryDir, scopeForMemoryDir(link.MemoryDir))
		if err := reg.save(); err != nil {
			cloudFail(110, "registry", err)
		}
	}
	return syncOne(cfg, project, a, enabling)
}

// withholdHistory marks the ledger history of a memory that no longer has a
// file, so sync keeps it on this machine. It reports false when the ledger
// knows no memory by exactly this ID.
func withholdHistory(dir, id string) (bool, error) {
	lcfg := &Config{MemoryDir: dir}
	entries, err := readLedger(lcfg)
	if err != nil {
		return false, err
	}
	known := false
	for _, e := range entries {
		if e.MemoryID != id {
			continue
		}
		if e.Action == cloudsync.ActionWithhold {
			return true, nil
		}
		known = true
	}
	if !known {
		return false, nil
	}
	return true, appendLedger(lcfg, LedgerEntry{Action: cloudsync.ActionWithhold, MemoryID: id})
}

// withholdDeleted handles 'cloud local' and 'unlocal' for an ID with no memory
// file. It reports whether there is a file, which the caller then handles.
func withholdDeleted(dir, id string, local bool) (hasFile bool) {
	defer lockMemoryDir(&Config{MemoryDir: dir})()
	if _, ok := findMemoryFileByID(dir, id); ok {
		return true
	}
	if !local {
		cloudFail(92, "memory_not_found", fmt.Errorf("memory %s has no file; withheld history cannot be shared again with this command", id))
	}
	found, err := withholdHistory(dir, id)
	if err != nil {
		cloudFail(110, "ledger", err)
	}
	if !found {
		cloudFail(92, "memory_not_found", fmt.Errorf("memory %s not found, in a file or in the ledger", id))
	}
	cloudOutput(map[string]any{"id": id, "status": "history_withheld"}, func() {
		fmt.Printf("%s is deleted: its ledger history now stays on this machine. The deletion itself still syncs.\n", id)
		fmt.Println("History already uploaded stays on the server. A real secret should also be replaced.")
	})
	return false
}

// setLocal rewrites a memory with the local-only flag set or cleared.
func setLocal(dir, id string, local bool) (Memory, string) {
	defer lockMemoryDir(&Config{MemoryDir: dir})()
	path, ok := findMemoryFileByID(dir, id)
	if !ok {
		cloudFail(92, "memory_not_found", fmt.Errorf("memory %s not found", id))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		cloudFail(110, "read", err)
	}
	memory := parseMemory(string(raw), filepath.Base(path))
	memory.Local = local
	if err := writeMemoryBytes(path, []byte(formatMemoryFile(memory))); err != nil {
		cloudFail(110, "write", err)
	}
	fmt.Printf("%s: local-only=%v\n", memory.ID, local)
	return memory, path
}

func handleCloud(cfg *Config) {
	a, err := parseCloudArgs(os.Args[2:])
	if err != nil {
		cloudFail(85, "invalid_argument", err)
	}
	if len(a.words) == 0 {
		fmt.Println(cloudUsage)
		return
	}
	sub, rest := a.words[0], a.words[1:]
	if a.project != "" && !cloudProjectName.MatchString(a.project) {
		cloudFail(85, "invalid_argument", errors.New("cloud project names use a-z, 0-9 and '-'"))
	}
	arg := func(what string) string {
		if len(rest) == 0 {
			cloudFail(80, "missing_argument", fmt.Errorf("cloud %s needs %s", sub, what))
		}
		return rest[len(rest)-1]
	}
	linked := func() (cloudsync.Store, cloudsync.Link) {
		if a.project == "" {
			cloudFail(80, "missing_argument", fmt.Errorf("cloud %s needs --project", sub))
		}
		store := cloudStore(a.project)
		link, err := store.Link()
		if err != nil {
			cloudFail(92, "not_linked", fmt.Errorf("project %q is not linked", a.project))
		}
		return store, link
	}
	// update changes the sync state while no sync is running.
	update := func(change func(*cloudsync.State, cloudsync.Link)) {
		store, link := linked()
		unlock, err := store.Lock()
		if err != nil {
			cloudFail(110, "lock", err)
		}
		defer unlock()
		st, err := store.Load()
		if err != nil {
			cloudFail(110, "state", err)
		}
		change(st, link)
		if err := store.Save(st); err != nil {
			cloudFail(110, "state", err)
		}
	}

	switch sub {
	case "help":
		fmt.Println(cloudUsage)
	case "server-help":
		fmt.Println(cloudServerUsage)
	case "login":
		server := strings.TrimRight(arg("a server URL"), "/")
		if err := cloudsync.CheckURL(server); err != nil {
			cloudFail(85, "invalid_argument", err)
		}
		fmt.Fprint(os.Stderr, "Paste the token and press Enter (it will be visible): ")
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, 4096)).ReadString('\n')
		token := strings.TrimSpace(line)
		if token == "" {
			cloudFail(80, "missing_argument", fmt.Errorf("no token on stdin: %v", err))
		}
		creds, err := loadCredentials()
		if err != nil {
			cloudFail(110, "credentials", err)
		}
		creds.Default, creds.Tokens[server] = server, token
		if err := saveCredentials(creds); err != nil {
			cloudFail(110, "credentials", err)
		}
		cloudOutput(map[string]any{"status": "logged_in", "server": server}, func() {
			fmt.Printf("\nToken stored for %s\n", server)
		})
	case "enable":
		// Never fall back to the current directory's scope: this uploads.
		if a.project == "" || !(cfg.ScopeResolved || memoryDir != "") {
			cloudFail(80, "missing_argument", errors.New(
				"cloud enable needs --project naming a registered project (see 'memgraph projects'), or --memory-dir"))
		}
		if _, err := os.Stat(cfg.MemoryDir); err != nil {
			cloudFail(92, "memory_not_found", err)
		}
		os.Exit(join(cfg, a, a.project, cfg.MemoryDir, true))
	case "clone":
		project := arg("a project name")
		if !cloudProjectName.MatchString(project) {
			cloudFail(85, "invalid_argument", errors.New("cloud project names use a-z, 0-9 and '-'"))
		}
		dir := memoryDir
		if dir == "" {
			if dir = loadRegistry().lookup(project); dir == "" {
				dir = filepath.Join(getGlobalMemgraphDir(), "projects", project, "memory")
			}
		}
		os.Exit(join(cfg, a, project, dir, false))
	case "sync":
		names := []string{a.project}
		if a.project == "" {
			names = slices.Sorted(maps.Keys(linkedProjects()))
		}
		if len(names) == 0 {
			cloudFail(92, "not_linked", errors.New("no project is linked on this machine"))
		}
		worst := 0
		for _, name := range names {
			// A failure outranks conflicts.
			if code := syncOne(cfg, name, a, false); code != 0 && (worst == 0 || worst == exitConflicts) {
				worst = code
			}
		}
		os.Exit(worst)
	case "status":
		links := linkedProjects()
		var out []map[string]any
		for _, name := range slices.Sorted(maps.Keys(links)) {
			row := map[string]any{"project": name, "memory_dir": links[name].MemoryDir, "url": links[name].URL}
			st, err := cloudStore(name).Load()
			if err != nil {
				row["last_error"] = err.Error()
			} else {
				pending, perr := cloudsync.Pending(links[name].MemoryDir, st)
				if perr != nil {
					row["last_error"] = perr.Error()
				} else if st.LastError != "" {
					row["last_error"] = st.LastError
				}
				row["pending"], row["open_conflicts"] = len(pending), len(st.Open())
				if !st.LastSync.IsZero() {
					row["last_sync"] = st.LastSync
				}
			}
			out = append(out, row)
		}
		cloudOutput(map[string]any{"projects": out}, func() {
			if len(out) == 0 {
				fmt.Println("No project is linked. Projects stay local until you enable them.")
			}
			for _, row := range out {
				fmt.Printf("%s\n", row["project"])
				for _, k := range []string{"url", "memory_dir", "last_sync", "pending", "open_conflicts", "last_error"} {
					if v, ok := row[k]; ok {
						fmt.Printf("  %-15s %v\n", k, v)
					}
				}
			}
		})
	case "conflicts":
		if len(rest) == 0 {
			store, _ := linked()
			st, err := store.Load()
			if err != nil {
				cloudFail(110, "state", err)
			}
			cloudOutput(map[string]any{"conflicts": st.Open()}, func() {
				if len(st.Open()) == 0 {
					fmt.Println("No open conflicts.")
				}
				for _, c := range st.Open() {
					fmt.Printf("%d  %s  %s  %s\n", c.ID, c.Name, c.Kind, c.Copy)
				}
			})
			return
		}
		action := rest[0]
		id, err := strconv.Atoi(arg("a conflict number"))
		if err != nil || len(rest) != 2 || (action != "show" && action != "restore" && action != "resolve") {
			cloudFail(85, "invalid_argument", errors.New("usage: cloud conflicts [show|restore|resolve <n>] --project <p>"))
		}
		update(func(st *cloudsync.State, link cloudsync.Link) {
			i := slices.IndexFunc(st.Conflicts, func(c cloudsync.ConflictRecord) bool { return c.ID == id })
			if i < 0 {
				cloudFail(92, "not_found", fmt.Errorf("no conflict %d", id))
			}
			c := &st.Conflicts[i]
			if action == "resolve" {
				c.Resolved = true
				fmt.Printf("Conflict %d resolved.\n", id)
				return
			}
			if c.Copy == "" {
				cloudFail(92, "no_copy", errors.New("nothing was lost in this conflict; the active file has the content"))
			}
			data, err := os.ReadFile(c.Copy)
			if err != nil {
				cloudFail(110, "read", err)
			}
			if action == "show" {
				os.Stdout.Write(data)
				return
			}
			unlock := lockMemoryDir(&Config{MemoryDir: link.MemoryDir})
			defer unlock()
			if err := writeMemoryBytes(filepath.Join(link.MemoryDir, c.Name), data); err != nil {
				cloudFail(110, "write", err)
			}
			c.Resolved = true
			fmt.Printf("Restored your version of %s. Run 'memgraph cloud sync' to share it.\n", c.Name)
		})
	case "local", "unlocal":
		id := arg("a memory ID")
		if a.project != "" {
			if _, err := cloudStore(a.project).Link(); errors.Is(err, os.ErrNotExist) {
				if !(cfg.ScopeResolved || memoryDir != "") {
					cloudFail(80, "missing_argument", errors.New("--project must name a registered project"))
				}
				if withholdDeleted(cfg.MemoryDir, id, sub == "local") {
					setLocal(cfg.MemoryDir, id, sub == "local")
				}
				return
			}
		}
		update(func(st *cloudsync.State, link cloudsync.Link) {
			if !withholdDeleted(link.MemoryDir, id, sub == "local") {
				return
			}
			memory, path := setLocal(link.MemoryDir, id, sub == "local")
			if memory.Local {
				st.LocalOnly[memory.ID] = true
				if st.Base[filepath.Base(path)] != "" {
					fmt.Fprintln(os.Stderr, "warning: already synced. The server and its history keep their copy; "+
						"this file is now neither uploaded nor updated.")
				}
			} else {
				delete(st.LocalOnly, memory.ID)
			}
		})
	case "allow":
		fp := arg("a fingerprint")
		update(func(st *cloudsync.State, _ cloudsync.Link) { st.Allowed[fp] = true })
		fmt.Printf("Finding %s will no longer block this project.\n", fp)
	case "disable":
		store, link := linked()
		unlock, err := store.Lock()
		if err != nil {
			cloudFail(110, "lock", err)
		}
		defer unlock()
		// Moved aside, not deleted: it holds conflict copies and the
		// local-only list.
		aside := filepath.Join(getGlobalMemgraphDir(), "sync-disabled", fmt.Sprintf("%s-%d", a.project, os.Getpid()))
		if err := os.MkdirAll(filepath.Dir(aside), 0700); err != nil {
			cloudFail(110, "mkdir", err)
		}
		if err := os.Rename(store.Dir, aside); err != nil {
			cloudFail(110, "disable", err)
		}
		cloudOutput(map[string]any{"status": "disabled", "old_state": aside}, func() {
			fmt.Printf("Unlinked %s. Memories stay in %s and on the server.\nOld sync state: %s\n",
				a.project, link.MemoryDir, aside)
		})
	default:
		cloudFail(85, "unknown_command", fmt.Errorf("unknown cloud command %q", sub))
	}
}
