package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeString(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// Updating memgraph's section must leave everything around it alone.
func TestUpsertFileSectionKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	heading := "## Memgraph — Skill Discovery"
	section := heading + "\n\n```bash\n# a comment, not a heading\nmemgraph recommend x\n```\n"
	writeString(t, path, "# Mine\n\n"+heading+"\n\nold text\n```sh\n# old comment\nold command\n```\n\n# My top level\n\nkeep this\n\n## My second level\n\nand this\n")
	for range 3 {
		if err := upsertFileSection(path, heading, section); err != nil {
			t.Fatal(err)
		}
	}
	got := readString(t, path)
	for _, want := range []string{"# Mine\n", "# My top level\n\nkeep this", "## My second level\n\nand this", "memgraph recommend x"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"old text", "old comment", "old command"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q was left in:\n%s", gone, got)
		}
	}
	if n := strings.Count(got, "# a comment, not a heading"); n != 1 {
		t.Errorf("the section appears %d times:\n%s", n, got)
	}
	before := got
	if err := upsertFileSection(path, heading, section); err != nil {
		t.Fatal(err)
	}
	if after := readString(t, path); after != before {
		t.Errorf("running again changed the file:\n%q\n%q", before, after)
	}
}

// A file that exists but cannot be read must not be replaced.
func TestUpsertFileSectionKeepsAnUnreadableTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := upsertFileSection(path, "## H", "## H\n"); err == nil {
		t.Error("no error")
	}
}

func TestBridgesNeverReplaceExistingFiles(t *testing.T) {
	bridges := map[string]func(*Config) (string, error){
		".claude/CLAUDE.md":      writeClaudeCodeBridge,
		".opencode/memory.json":  writeOpenCodeBridge,
		".copilot/settings.json": writeCopilotBridge,
	}
	for path, write := range bridges {
		t.Run(path, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cfg := &Config{MemoryDir: "/m"}
			if got, err := write(cfg); err != nil || got != path {
				t.Fatalf("new file: %q %v", got, err)
			}
			if _, err := write(cfg); err != nil {
				t.Errorf("same content again: %v", err)
			}
			writeString(t, path, "my own rules\n")
			if _, err := write(cfg); err == nil {
				t.Error("no error for a file with other content")
			}
			if got := readString(t, path); got != "my own rules\n" {
				t.Errorf("replaced with %q", got)
			}
		})
	}
}

func TestMCPSetupKeepsAgentsFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	writeString(t, path, "# My project\n\nmy own rules\n")
	mcpSetup(&Config{ProjectRoot: root}, map[string]any{"sync_dir": "/skills"})
	got := readString(t, path)
	if !strings.Contains(got, "my own rules") || !strings.Contains(got, "memgraph recommend") {
		t.Errorf("got:\n%s", got)
	}
}
