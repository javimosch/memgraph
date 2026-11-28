package main

// These tests run the real program, each in a home of its own.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	builtPath string
	buildErr  error
	buildDir  string
)

func program(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if buildDir, buildErr = os.MkdirTemp("", "memgraph-test-bin-"); buildErr != nil {
			return
		}
		builtPath = filepath.Join(buildDir, "memgraph")
		if out, err := exec.Command("go", "build", "-o", builtPath, ".").CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("%v: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtPath
}

type box struct {
	t          *testing.T
	home, repo string
}

func newBox(t *testing.T) *box {
	home := t.TempDir()
	b := &box{t: t, home: home, repo: filepath.Join(home, "code", "my-app")}
	b.newRepo(b.repo)
	return b
}

func (b *box) newRepo(dir string) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		b.t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		b.t.Fatal(err, string(out))
	}
}

func (b *box) in(dir, stdin string, args ...string) (int, string) {
	cmd := exec.Command(program(b.t), args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + b.home, "PATH=" + os.Getenv("PATH")}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(out)
		}
		b.t.Fatal(err)
	}
	return 0, string(out)
}

func (b *box) run(args ...string) (int, string) { return b.in(b.repo, "", args...) }

func (b *box) save(text string, extra ...string) string {
	b.t.Helper()
	code, out := b.run(append([]string{"remember", "--json", "--text", text}, extra...)...)
	var reply struct{ ID string }
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &reply); err != nil || code != 0 {
		b.t.Fatalf("save: %d %s", code, out)
	}
	return reply.ID
}

func (b *box) files() []string {
	found, err := filepath.Glob(filepath.Join(b.home, ".memgraph", "projects", "*", "memory", "memory_*.md"))
	if err != nil {
		b.t.Fatal(err)
	}
	return found
}

func (b *box) file(id string) string {
	b.t.Helper()
	for _, f := range b.files() {
		if filepath.Base(f) == "memory_"+id+".md" {
			return f
		}
	}
	b.t.Fatalf("no file for %s", id)
	return ""
}

func (b *box) mcp(tool string, args map[string]any) string {
	b.t.Helper()
	var in strings.Builder
	for i, m := range []map[string]any{
		{"method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}}},
		{"method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}},
	} {
		m["jsonrpc"], m["id"] = "2.0", i+1
		line, _ := json.Marshal(m)
		in.Write(append(line, '\n'))
	}
	_, out := b.in(b.repo, in.String(), "mcp")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

const divided = "intro line that is long enough to fill the whole short description\n\n---\n\nBETWEENRULES stays\n\n---\n\nTAIL stays"

func bodyOf(t *testing.T, path string) string {
	_, body, _ := strings.Cut(readString(t, path), "\n---\n")
	return body
}

func TestDividersInTextAreText(t *testing.T) {
	b := newBox(t)
	id := b.save(divided)
	for _, args := range [][]string{{"recall", "intro", "--json"}, {"read", id}} {
		if _, out := b.run(args...); !strings.Contains(out, "BETWEENRULES stays") {
			t.Errorf("%v hides text: %s", args, out)
		}
	}
	path := b.file(id)
	b.run("supersede", id, "--text", "newer")
	if !strings.Contains(bodyOf(t, path), "BETWEENRULES stays") {
		t.Errorf("supersede dropped text:\n%s", readString(t, path))
	}
	id = b.save(divided)
	b.run("delete", id)
	_, out := b.run("ledger", "--json")
	if strings.Count(out, "BETWEENRULES stays") == 0 {
		t.Errorf("the ledger copy lacks text: %s", out)
	}
}

func TestTextCannotSetMetadata(t *testing.T) {
	b := newBox(t)
	b.save("a\n---\ntype: reference\nproject: hijacked\ntags: injected\n---\nz")
	_, out := b.run("list", "--json")
	var reply struct {
		Results []struct {
			Type, Project string
			Tags          []string
		}
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil || len(reply.Results) != 1 {
		t.Fatal(err, out)
	}
	if got := reply.Results[0]; got.Project == "hijacked" || got.Type == "reference" || len(got.Tags) != 0 {
		t.Errorf("metadata taken from the text: %+v", got)
	}
}

func TestRewritesKeepUnknownHeaderFields(t *testing.T) {
	for _, op := range [][]string{{"edit", "ID", "changed"}, {"supersede", "ID", "--text", "newer"}} {
		b := newBox(t)
		id := b.save("some text")
		path := b.file(id)
		added := "myfield: keep-me\nmetadata:\n  type: nested\n  items:\n    - one\n"
		writeString(t, path, strings.Replace(readString(t, path), "type:", added+"type:", 1))
		op[1] = id
		b.run(op...)
		got := readString(t, path)
		if !strings.Contains(got, "myfield: keep-me\n") || !strings.Contains(got, "metadata:\n  type: nested\n  items:\n    - one\n") {
			t.Errorf("%s lost fields:\n%s", op[0], got)
		}
	}
}

func TestOnlyExactIDsMatch(t *testing.T) {
	b := newBox(t)
	b.run("demo")
	var before []string
	for _, f := range b.files() {
		before = append(before, readString(t, f))
	}
	for _, args := range [][]string{{"delete", "1"}, {"edit", "2", "OVERWRITTEN"}, {"supersede", "3", "--text", "x"}, {"read", "0"}} {
		if code, out := b.run(args...); code == 0 {
			t.Errorf("%v succeeded: %s", args, out)
		}
	}
	b.mcp("memgraph_delete", map[string]any{"id": "1"})
	b.mcp("memgraph_edit", map[string]any{"id": "2", "text": "OVERWRITTEN"})
	for i, f := range b.files() {
		if i >= len(before) || readString(t, f) != before[i] {
			t.Fatalf("a memory changed: %s", f)
		}
	}
	if len(b.files()) != len(before) {
		t.Fatalf("%d memories, want %d", len(b.files()), len(before))
	}
}

func TestUnknownProjectStopsWrites(t *testing.T) {
	b := newBox(t)
	id := b.save("belongs here")
	path := b.file(id)
	before := readString(t, path)
	for _, args := range [][]string{
		{"delete", id}, {"edit", id, "X"}, {"supersede", id, "--text", "x"}, {"remember", "elsewhere"},
		{"demo"}, {"verify", "--mark"}, {"init"},
	} {
		if code, out := b.run(append(args, "--project", "no-such-project")...); code == 0 {
			t.Errorf("%v succeeded: %s", args, out)
		}
	}
	for tool, args := range map[string]map[string]any{
		"memgraph_delete": {"id": id}, "memgraph_edit": {"id": id, "text": "X"},
		"memgraph_save": {"text": "elsewhere"}, "memgraph_supersede": {"id": id, "text": "x"},
	} {
		args["project"] = "no-such-project"
		if out := b.mcp(tool, args); !strings.Contains(out, "unknown project") {
			t.Errorf("%s: %s", tool, out)
		}
	}
	if len(b.files()) != 1 || readString(t, path) != before {
		t.Errorf("the current repository changed: %v", b.files())
	}
	// Reading still works, and so does a first save under the repository's own
	// name, also in a repository that has no memories yet.
	if code, out := b.run("recall", "belongs", "--project", "no-such-project"); code != 0 {
		t.Error(out)
	}
	fresh := filepath.Join(b.home, "code", "brand-new-thing")
	b.newRepo(fresh)
	if code, out := b.in(fresh, "", "remember", "first", "--project", "brand-new-thing"); code != 0 {
		t.Error(out)
	}
}

func TestWriteCommandsRejectUnknownFlags(t *testing.T) {
	b := newBox(t)
	id := b.save("keep me")
	path := b.file(id)
	before := readString(t, path)
	for _, args := range [][]string{
		{"delete", id, "--dry-run"}, {"delete", id, "-n"}, {"edit", id, "new", "--force-it"},
		{"supersede", id, "--text", "x", "--dryrun"}, {"remember", "more", "--no-save"},
		{"demo", "--dry-run"}, {"edit", id, "new", "--project"}, {"delete", id, "--reason"},
	} {
		if code, out := b.run(args...); code == 0 {
			t.Errorf("%v succeeded: %s", args, out)
		}
	}
	if len(b.files()) != 1 || readString(t, path) != before {
		t.Fatalf("something changed: %v", b.files())
	}
	// Text that starts with dashes can still be saved.
	if code, out := b.run("remember", "--", "--dry-run"); code != 0 {
		t.Error(out)
	}
	if code, out := b.run("remember", "--verbose is a flag of that tool"); code != 0 {
		t.Error(out)
	}
	if code, out := b.run("--json", "delete", id, "-y", "--reason", "done"); code != 0 {
		t.Error(out)
	}
}

func TestPurgeOnlyRemovesOneUnsharedProject(t *testing.T) {
	b := newBox(t)
	id := b.save("first")
	b.run("attach", "mine")
	dir := filepath.Dir(b.file(id))
	projects := filepath.Join(b.home, ".memgraph", "projects")
	for name, target := range map[string]string{"everything": projects, "scope": filepath.Dir(dir), "alias": dir} {
		b.run("attach", name, "--memory-dir", target)
		if code, out := b.run("detach", name, "--purge", "-y"); code == 0 {
			t.Errorf("purge of %s succeeded: %s", name, out)
		}
		b.run("detach", name)
		if len(b.files()) != 1 {
			t.Fatalf("purging %q removed memories", name)
		}
	}
	if out := b.mcp("memgraph_admin", map[string]any{"command": "detach", "args": map[string]any{"name": "mine", "purge": true}}); len(b.files()) != 1 {
		t.Fatalf("an agent purged: %s", out)
	}
	if code, out := b.run("detach", "mine", "--purge", "-y"); code != 0 || len(b.files()) != 0 {
		t.Errorf("a project of its own is still purged: %d %s", code, out)
	}
}

func TestDamagedRegistryIsKept(t *testing.T) {
	b := newBox(t)
	b.save("first")
	b.run("attach", "mine")
	path := filepath.Join(b.home, ".memgraph", "projects.json")
	damaged := readString(t, path)[:20]
	writeString(t, path, damaged)
	if _, out := b.run("recall", "first"); !strings.Contains(out, "projects.json") {
		t.Errorf("no warning: %s", out)
	}
	for _, args := range [][]string{{"attach", "new"}, {"detach", "mine"}, {"remember", "x", "--project", "mine"}} {
		if code, out := b.run(args...); code == 0 {
			t.Errorf("%v succeeded: %s", args, out)
		}
	}
	b.mcp("memgraph_admin", map[string]any{"command": "attach", "args": map[string]any{"name": "new"}})
	if got := readString(t, path); got != damaged {
		t.Errorf("rewritten as %s", got)
	}
}

func TestSimultaneousAttachesAllSurvive(t *testing.T) {
	b := newBox(t)
	b.save("seed")
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.run("attach", fmt.Sprintf("name-%02d", i), "--memory-dir", filepath.Join(b.home, "m", fmt.Sprint(i)))
		}()
	}
	wg.Wait()
	_, out := b.run("projects", "--json")
	registry := readString(t, filepath.Join(b.home, ".memgraph", "projects.json"))
	for i := range 40 {
		if !strings.Contains(registry, fmt.Sprintf(`"name-%02d"`, i)) {
			t.Fatalf("name-%02d was lost; %s", i, out)
		}
	}
}

func TestAttachDoesNotMoveANameUnasked(t *testing.T) {
	b := newBox(t)
	b.save("first")
	b.run("attach", "mine")
	registry := filepath.Join(b.home, ".memgraph", "projects.json")
	before := readString(t, registry)
	other := filepath.Join(b.home, "code", "other")
	b.newRepo(other)
	b.in(other, "", "remember", "second")
	if code, out := b.in(other, "", "attach", "mine"); code == 0 {
		t.Errorf("moved: %s", out)
	}
	if got := readString(t, registry); !strings.Contains(got, "my-app") || strings.Count(got, `"mine"`) != strings.Count(before, `"mine"`) {
		t.Errorf("registry:\n%s", got)
	}
	if code, out := b.run("attach", "mine"); code != 0 {
		t.Errorf("attaching the same folder again: %s", out)
	}
	if code, out := b.in(other, "", "attach", "mine", "--force"); code != 0 {
		t.Errorf("--force: %s", out)
	}
}

// An unregistered name is matched against the last part of scope names. When
// several end the same way, writing would pick one of them at random.
func TestAmbiguousProjectStopsWrites(t *testing.T) {
	b := newBox(t)
	b.save("seed")
	b.run("projects") // the registry exists before the two folders do
	var kept []string
	for _, scope := range []string{"github.com-a-cli", "github.com-b-cli"} {
		path := filepath.Join(b.home, ".memgraph", "projects", scope, "memory", "memory_1790000000000000001.md")
		writeString(t, path, "---\nname: n\ncreated: 2026-01-01T00:00:00Z\n---\n\nkeep\n")
		kept = append(kept, path)
	}
	for _, args := range [][]string{{"delete", "1790000000000000001"}, {"remember", "x"}} {
		if code, out := b.run(append(args, "--project", "cli")...); code == 0 || !strings.Contains(out, "more than one") {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
	for _, path := range kept {
		if !has(path) {
			t.Errorf("removed %s", path)
		}
	}
}
