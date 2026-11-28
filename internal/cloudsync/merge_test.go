package cloudsync

import (
	"fmt"
	"strings"
	"testing"
)

// TestMergeRules covers every row of the table in docs/cloud-sync.md.
func TestMergeRules(t *testing.T) {
	const none = ""
	cases := []struct {
		row           int
		b, l, r, want string
		kind          string
	}{
		{1, none, "x", none, "x", ""},
		{2, none, none, "x", "x", ""},
		{3, none, "x", "x", "x", ""},
		{4, none, "x", "y", "y", BothChanged},
		{5, "a", "a", "a", "a", ""},
		{6, "a", "x", "a", "x", ""},
		{7, "a", "a", "y", "y", ""},
		{8, "a", "x", "x", "x", ""},
		{9, "a", "x", "y", "y", BothChanged},
		{10, "a", none, "a", none, ""},
		{11, "a", "a", none, none, ""},
		{12, "a", none, none, none, ""},
		{13, "a", "x", none, "x", EditedDelete},
		{14, "a", none, "y", "y", EditedDelete},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint("row", c.row), func(t *testing.T) {
			base, local, remote := Manifest{}, Files{}, Files{}
			if c.b != none {
				base["memory_1.md"] = BlobHash([]byte(c.b))
			}
			if c.l != none {
				local["memory_1.md"] = []byte(c.l)
			}
			if c.r != none {
				remote["memory_1.md"] = []byte(c.r)
			}
			merged, conflicts := MergeFiles(base, local, remote)
			got, present := merged["memory_1.md"]
			if string(got) != c.want || present != (c.want != none) {
				t.Fatalf("merged = %q (present %v), want %q", got, present, c.want)
			}
			if c.kind == "" {
				if len(conflicts) != 0 {
					t.Fatalf("unexpected conflicts %+v", conflicts)
				}
				return
			}
			if len(conflicts) != 1 || conflicts[0].Kind != c.kind {
				t.Fatalf("conflicts = %+v, want %s", conflicts, c.kind)
			}
			if c.kind == BothChanged && string(conflicts[0].Loser) != c.l {
				t.Fatalf("loser = %q, want the local %q", conflicts[0].Loser, c.l)
			}
		})
	}
}

func TestBlobHashMatchesGit(t *testing.T) {
	// echo hello | git hash-object --stdin
	if got := BlobHash([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Fatal(got)
	}
}

func TestMergeLedger(t *testing.T) {
	one := `{"ts":"2026-01-01T00:00:00Z","memory_id":"1"}`
	two := `{"ts":"2026-01-02T00:00:00Z","memory_id":"2"}`
	three := `{"ts":"2026-01-03T00:00:00Z","memory_id":"3"}`
	got := string(MergeLedger([]byte(one+"\n"+three+"\n"), []byte(one+"\n"+two+"\nnot json\n")))
	want := one + "\n" + two + "\nnot json\n" + three + "\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := string(MergeLedger([]byte(got), []byte(got))); again != got {
		t.Fatalf("merge is not stable:\n%s", again)
	}
}

func TestPublicLedgerHidesLocalMemories(t *testing.T) {
	data := []byte(`{"memory_id":"public","snapshot":{"content":"ok"}}
{"memory_id":"listed","snapshot":{"content":"hidden-by-id"}}
{"memory_id":"gone-before-first-sync","snapshot":{"content":"hidden-by-flag","local":true}}
truncated {"memory_id":"x","snapshot":{"content":"hidden-unparseable"
`)
	got := string(PublicLedger(data, map[string]bool{"listed": true}))
	if !strings.Contains(got, "public") || strings.Contains(got, "hidden") {
		t.Fatalf("got %s", got)
	}
}

func TestIsLocalOnly(t *testing.T) {
	for content, want := range map[string]bool{
		"---\nname: a\nlocal: true\n---\nbody": true,
		"---\nname: a\n---\nlocal: true\n":     false,
		"---\nlocal: false\n---\n":             false,
		"local: true":                          false,
	} {
		if IsLocalOnly([]byte(content)) != want {
			t.Errorf("IsLocalOnly(%q) != %v", content, want)
		}
	}
}

func TestScan(t *testing.T) {
	// Assembled at run time so this file holds nothing that looks like a secret.
	fake := "gh" + "p_" + strings.Repeat("a1", 18)
	dbURL := "postgres://admin:" + "hunter2hunter2" + "@db.example.invalid/x"
	data := []byte("fine line\ntoken " + fake + "\n" + dbURL + "\n")
	found := Scan("memory_1.md", nil, data, nil)
	if len(found) != 2 || found[0].Line != 2 || found[0].Rule != "github-token" || found[1].Rule != "url-password" {
		t.Fatalf("findings %+v", found)
	}
	for _, f := range found {
		if s := fmt.Sprintf("%+v", f); strings.Contains(s, fake) || strings.Contains(s, "hunter2") {
			t.Fatal("finding carries the secret")
		}
	}
	if left := Scan("memory_1.md", nil, data, map[string]bool{found[0].Fingerprint: true}); len(left) != 1 {
		t.Fatalf("allow list ignored: %+v", left)
	}
	if left := Scan("memory_1.md", data, data, nil); len(left) != 0 {
		t.Fatalf("unchanged lines rescanned: %+v", left)
	}
	clean := "see https://example.invalid/a:b and git@github.com:o/r.git, user:${PASSWORD}@host, sk-short"
	if found := Scan("m", nil, []byte(clean), nil); len(found) != 0 {
		t.Fatalf("false positives: %+v", found)
	}
}

func TestPublicLedgerWithholdsMarkedHistory(t *testing.T) {
	data := []byte(`{"action":"delete","memory_id":"x","snapshot":{"content":"hidden-delete"}}
{"action":"supersede","memory_id":"x","reason":"hidden-reason"}
{"action":"delete","memory_id":"y","snapshot":{"content":"kept"}}
{"action":"withhold","memory_id":"x"}
`)
	got := string(PublicLedger(data, nil))
	if strings.Contains(got, "hidden") || strings.Contains(got, `"x"`) || !strings.Contains(got, "kept") {
		t.Fatalf("got %s", got)
	}
}
