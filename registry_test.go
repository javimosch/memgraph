package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newTestRegistry returns an empty registry rooted in a temp HOME so save()
// and purgeProjectDir() resolve paths under the test's own ~/.memgraph.
func newTestRegistry(t *testing.T) (*ProjectRegistry, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".memgraph", "projects"), 0755); err != nil {
		t.Fatal(err)
	}
	return &ProjectRegistry{Projects: map[string]ProjectEntry{}}, home
}

func TestUnregisterKeepsMemoryFiles(t *testing.T) {
	reg, home := newTestRegistry(t)
	memDir := filepath.Join(home, ".memgraph", "projects", "scope-a", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestMemory(t, memDir, "1", "fleet runs on rbm21")
	reg.register("fleet", memDir, "scope-a")

	if !reg.unregister("fleet") {
		t.Fatal("expected fleet to unregister")
	}
	if reg.lookup("fleet") != "" {
		t.Error("alias should be gone")
	}
	if !dirExists(memDir) {
		t.Error("detach without --purge must keep the memory dir")
	}
}

func TestPurgeProjectDirOnlyDeletesInsideTheStore(t *testing.T) {
	_, home := newTestRegistry(t)
	inside := filepath.Join(home, ".memgraph", "projects", "scope-b", "memory")
	outside := filepath.Join(home, "elsewhere", "memory")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeTestMemory(t, dir, "1", "x")
	}

	if err := purgeProjectDir(ProjectEntry{Path: inside}); err != nil {
		t.Fatalf("purge inside the store should work: %v", err)
	}
	if dirExists(inside) {
		t.Error("memory dir should be deleted")
	}
	if dirExists(filepath.Dir(inside)) {
		t.Error("empty scope dir should be removed too")
	}

	if err := purgeProjectDir(ProjectEntry{Path: outside}); err == nil {
		t.Error("purge must refuse paths outside the projects store")
	}
	if !dirExists(outside) {
		t.Error("a refused purge must leave the directory untouched")
	}
}

func TestRenamePreservesEntryAndRejectsTakenNames(t *testing.T) {
	reg, home := newTestRegistry(t)
	memDir := filepath.Join(home, ".memgraph", "projects", "scope-c", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	reg.register("old", memDir, "scope-c")
	reg.register("taken", memDir, "scope-c")
	created := reg.Projects["old"].Created

	if reg.rename("ghost", "new") {
		t.Error("renaming an unknown name should fail")
	}
	if reg.rename("old", "taken") {
		t.Error("rename must not overwrite a registered name")
	}
	if !reg.rename("old", "armada") {
		t.Fatal("rename should succeed")
	}
	if reg.lookup("old") != "" {
		t.Error("old alias should be gone")
	}
	entry := reg.Projects["armada"]
	if entry.Path != memDir || entry.Remote != "scope-c" || !entry.Created.Equal(created) {
		t.Errorf("rename must preserve path, remote and created: %+v", entry)
	}
}

func TestScopeForMemoryDir(t *testing.T) {
	_, home := newTestRegistry(t)

	// Conventional layout: <global>/projects/<scope>/memory → <scope>
	memDir := filepath.Join(home, ".memgraph", "projects", "-home-user-ai-fleet", "memory")
	if got := scopeForMemoryDir(memDir); got != "-home-user-ai-fleet" {
		t.Errorf("scope should be the parent dir name, got %q", got)
	}

	// Custom dirs key on their parent too — never on the caller's cwd.
	if got := scopeForMemoryDir(filepath.Join(home, "stores", "fleet-mem")); got != "stores" {
		t.Errorf("custom memory dir should key on its parent, got %q", got)
	}
}

func TestRegistryWarnings(t *testing.T) {
	reg, home := newTestRegistry(t)
	mkScope := func(scope string) string {
		dir := filepath.Join(home, ".memgraph", "projects", scope, "memory")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	good := mkScope("scope-ok")
	bad := mkScope("scope-real")

	reg.register("ok", good, "scope-ok")
	if w := reg.registryWarnings(); len(w) != 0 {
		t.Fatalf("consistent entries must not warn, got %v", w)
	}

	// A recorded remote whose scope dir exists on disk is a real
	// conflict: repo-local writes land there while --project resolves the
	// registered path (#22).
	mkScope("scope-unrelated")
	reg.register("runpod", bad, "scope-unrelated")
	joined := strings.Join(reg.registryWarnings(), "\n")
	if !strings.Contains(joined, `"runpod"`) || !strings.Contains(joined, "scope-unrelated") {
		t.Errorf("expected a live-scope conflict warning, got %v", joined)
	}

	// An alias shadowing a real scope dir while pointing elsewhere.
	reg.register("ghost", bad, "scope-real") // consistent remote — no mismatch
	mkScope("ghost")                         // but a scope dir with this name exists
	found := false
	for _, w := range reg.registryWarnings() {
		if strings.Contains(w, `"ghost"`) && strings.Contains(w, "scope dir") {
			found = true
		}
	}
	if !found {
		t.Error("expected a scope-dir collision warning for 'ghost'")
	}

	// A remote naming a scope with no memory dir on disk cannot misroute
	// anything — Path alone drives resolution. That's drift, not a
	// warning (#22).
	outside := filepath.Join(home, "elsewhere", "memory")
	reg.register("ext", outside, "github.com-x-y")
	for _, w := range reg.registryWarnings() {
		if strings.Contains(w, `"ext"`) {
			t.Errorf("stale remote with no live scope dir must not warn, got %v", w)
		}
	}
	found = false
	for _, d := range reg.registryDrift() {
		if strings.Contains(d, `"ext"`) && strings.Contains(d, "github.com-x-y") {
			found = true
		}
	}
	if !found {
		t.Error("expected a drift note for outside-store entry with stale remote")
	}
}

func TestRegistryRepair(t *testing.T) {
	reg, home := newTestRegistry(t)
	mkScope := func(scope string) string {
		dir := filepath.Join(home, ".memgraph", "projects", scope, "memory")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	real := mkScope("scope-real")
	mkScope("github.com-x-live") // live remote scope — a conflict, not drift

	reg.register("ok", real, "scope-real")
	reg.register("drifty", real, "github.com-x-gone") // no such scope dir
	reg.register("conflict", real, "github.com-x-live")

	res := reg.repairRegistry()
	if len(res.Repaired) != 1 || res.Repaired[0] != "drifty" {
		t.Fatalf("expected drifty repaired, got %+v", res)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "conflict" {
		t.Fatalf("a live remote scope must be skipped for a manual decision, got %+v", res)
	}
	if got := reg.Projects["drifty"].Remote; got != "scope-real" {
		t.Errorf("repaired remote = %q, want the memory dir's scope %q", got, "scope-real")
	}
	if got := reg.Projects["conflict"].Remote; got != "github.com-x-live" {
		t.Errorf("skipped entry must keep its remote, got %q", got)
	}
	if d := reg.registryDrift(); len(d) != 0 {
		t.Errorf("repair should leave no drift, got %v", d)
	}
	if w := reg.registryWarnings(); len(w) != 1 {
		t.Errorf("the live-scope conflict must still warn after repair, got %v", w)
	}
}

func TestAttachMemoryDirDerivesScopeFromDir(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	home := t.TempDir()
	memDir := filepath.Join(home, ".memgraph", "projects", "-home-user-ai-fleet", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestMemory(t, memDir, "1", "fleet runs on rbm21")

	run := func(args ...string) (string, error) {
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// The repo this test runs in has its own git remote — the registered
	// remote must come from the memory dir, not from it.
	out, err := run("attach", "fleet", "--memory-dir", memDir, "--json")
	if err != nil {
		t.Fatalf("attach failed: %v %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(home, ".memgraph", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved ProjectRegistry
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	entry, ok := saved.Projects["fleet"]
	if !ok {
		t.Fatal("fleet should be registered")
	}
	if entry.Path != memDir {
		t.Errorf("path = %q, want %q", entry.Path, memDir)
	}
	if entry.Remote != "-home-user-ai-fleet" {
		t.Errorf("remote = %q, want the memory dir's scope %q", entry.Remote, "-home-user-ai-fleet")
	}
}

func TestAttachParsesAfterGlobalFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	home := t.TempDir()
	memDir := filepath.Join(home, ".memgraph", "projects", "scope-flags", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}

	// A global flag before the command must not shift positional args —
	// 'attach' used to be registered as the project name itself.
	cmd := exec.Command("go", "run", ".", "--json", "attach", "flaggy", "--memory-dir", memDir)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("attach failed: %v %s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(home, ".memgraph", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved ProjectRegistry
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Projects["attach"]; ok {
		t.Error("the literal command name must never be registered as a project")
	}
	if _, ok := saved.Projects["flaggy"]; !ok {
		t.Error("flaggy should be registered")
	}
}

func TestRegistryWarningsOnLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	home := t.TempDir()
	memDir := filepath.Join(home, ".memgraph", "projects", "scope-real", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestMemory(t, memDir, "1", "x")

	writeRegistry := func(remote string) {
		data, _ := json.Marshal(map[string]any{
			"projects": map[string]any{
				"runpod": map[string]any{
					"path":    memDir,
					"remote":  remote,
					"created": "2026-01-01T00:00:00Z",
				},
			},
		})
		if err := os.WriteFile(filepath.Join(home, ".memgraph", "projects.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	run := func(args ...string) (string, string, error) {
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	// A remote naming a scope with no dir on disk is drift: reported in
	// the projects listing, silent on every other command (#22).
	writeRegistry("github.com-x-unrelated")

	stdout, _, err := run("projects", "--json")
	if err != nil {
		t.Fatalf("projects failed: %v %s", err, stdout)
	}
	if !strings.Contains(stdout, `"drift"`) || !strings.Contains(stdout, "github.com-x-unrelated") {
		t.Errorf("expected the stale remote under drift, got %s", stdout)
	}

	_, stderr, err := run("status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if strings.Contains(stderr, "memgraph:") {
		t.Errorf("drift must stay silent outside the projects listing, got stderr %q", stderr)
	}

	// --repair normalizes the stale remote to the memory dir's real scope.
	stdout, _, err = run("projects", "--repair", "--json")
	if err != nil {
		t.Fatalf("repair failed: %v %s", err, stdout)
	}
	if !strings.Contains(stdout, `"repaired":["runpod"]`) {
		t.Errorf("expected runpod repaired, got %s", stdout)
	}
	data, err := os.ReadFile(filepath.Join(home, ".memgraph", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved ProjectRegistry
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if got := saved.Projects["runpod"].Remote; got != "scope-real" {
		t.Errorf("repaired remote = %q, want scope-real", got)
	}

	// A remote whose scope dir DOES exist is a real conflict: one summary
	// line on stderr for other commands, full detail in projects, and
	// --repair refuses to pick a side (#22).
	liveDir := filepath.Join(home, ".memgraph", "projects", "github.com-x-unrelated", "memory")
	if err := os.MkdirAll(liveDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeRegistry("github.com-x-unrelated")

	_, stderr, err = run("status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "1 registry entry needs attention") {
		t.Errorf("expected exactly one summary line on stderr, got %q", stderr)
	}

	stdout, _, err = run("projects", "--json")
	if err != nil {
		t.Fatalf("projects failed: %v %s", err, stdout)
	}
	if !strings.Contains(stdout, `"warnings"`) || !strings.Contains(stdout, "github.com-x-unrelated") {
		t.Errorf("expected the conflict under warnings, got %s", stdout)
	}

	stdout, _, err = run("projects", "--repair", "--json")
	if err != nil {
		t.Fatalf("repair failed: %v %s", err, stdout)
	}
	if !strings.Contains(stdout, `"skipped":["runpod"]`) {
		t.Errorf("a live remote scope must be skipped, got %s", stdout)
	}
}

func TestDetachAndRenameEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test disabled in short mode")
	}
	home := t.TempDir()
	memDir := filepath.Join(home, ".memgraph", "projects", "scope-e2e", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestMemory(t, memDir, "1", "fleet runs on rbm21")

	writeRegistry := func(name string) {
		data, _ := json.Marshal(map[string]any{
			"projects": map[string]any{
				name: map[string]any{
					"path":    memDir,
					"remote":  "scope-e2e",
					"created": "2026-01-01T00:00:00Z",
				},
			},
		})
		if err := os.WriteFile(filepath.Join(home, ".memgraph", "projects.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeRegistry("fleet")

	run := func(args ...string) (string, error) {
		cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run("detach", "ghost", "--json"); err == nil || !strings.Contains(out, "not in registry") {
		t.Fatalf("detaching an unknown project should fail: %v %s", err, out)
	}

	out, err := run("detach", "fleet", "--json")
	if err != nil || !strings.Contains(out, `"detached"`) {
		t.Fatalf("detach failed: %v %s", err, out)
	}
	if !dirExists(memDir) {
		t.Fatal("detach must keep the memory dir")
	}

	writeRegistry("fleet")
	out, err = run("rename", "fleet", "armada", "--json")
	if err != nil || !strings.Contains(out, `"renamed"`) {
		t.Fatalf("rename failed: %v %s", err, out)
	}

	// The renamed alias is what resolves now. --json is non-interactive, so
	// --purge deletes without prompting.
	out, err = run("detach", "armada", "--purge", "--json")
	if err != nil || !strings.Contains(out, `"purged":true`) {
		t.Fatalf("purge failed: %v %s", err, out)
	}
	if dirExists(memDir) {
		t.Fatal("purge should delete the memory dir")
	}
}
