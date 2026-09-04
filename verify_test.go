package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractClaimsFindsEachKind(t *testing.T) {
	text := "See ~/ai/machin/README.md and /etc/hosts plus https://example.com/health and github.com repo https://github.com/javimosch/glane."
	claims := extractClaims(text)

	got := map[string]string{}
	for _, c := range claims {
		got[c.Value] = c.Kind
	}
	for value, kind := range map[string]string{
		"~/ai/machin/README.md":      "path",
		"/etc/hosts":                 "path",
		"https://example.com/health": "url",
		"javimosch/glane":            "repo",
	} {
		if got[value] != kind {
			t.Errorf("claim %q: want kind %q, got %q (all: %v)", value, kind, got[value], got)
		}
	}
}

func TestExtractClaimsDoesNotReportPathsInsideURLs(t *testing.T) {
	for _, c := range extractClaims("https://example.com/a/b/c") {
		if c.Kind == "path" {
			t.Fatalf("URL path leaked as a filesystem claim: %+v", c)
		}
	}
}

func TestExtractClaimsDeduplicates(t *testing.T) {
	claims := extractClaims("/etc/hosts and again /etc/hosts")
	if len(claims) != 1 {
		t.Fatalf("want 1 deduplicated claim, got %d: %+v", len(claims), claims)
	}
}

// homeTempDir returns a scratch directory under the user's real home, because
// staleness is only claimable there.
func homeTempDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	dir, err := os.MkdirTemp(home, ".memgraph-verify-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestCheckPathStaleOnlyWhenParentExists(t *testing.T) {
	dir := homeTempDir(t)
	present := filepath.Join(dir, "here.txt")
	if err := os.WriteFile(present, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	checker := newClaimChecker(false)

	if got := checker.check(Claim{Kind: "path", Value: present}); got.Verdict != verdictFresh {
		t.Errorf("existing file: want fresh, got %s", got.Verdict)
	}

	// Parent exists, leaf does not: positive evidence of staleness.
	missing := filepath.Join(dir, "gone.txt")
	if got := checker.check(Claim{Kind: "path", Value: missing}); got.Verdict != verdictStale {
		t.Errorf("missing file in an existing dir: want stale, got %s", got.Verdict)
	}

	// Neither exists: could be another host. Must not claim staleness.
	remote := filepath.Join(dir, "no-such-dir", "file.txt")
	if got := checker.check(Claim{Kind: "path", Value: remote}); got.Verdict != verdictUnknown {
		t.Errorf("path on an absent branch: want unknown, got %s", got.Verdict)
	}
}

func TestCheckURLTreatsAuthWallsAsAlive(t *testing.T) {
	cases := map[int]string{
		200: verdictFresh,
		401: verdictFresh, // grange/cuzz front doors answer 401 to HEAD
		403: verdictFresh,
		404: verdictStale,
		410: verdictStale,
		500: verdictUnknown,
	}
	for status, want := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		got := newClaimChecker(true).check(Claim{Kind: "url", Value: server.URL})
		if got.Verdict != want {
			t.Errorf("status %d: want %s, got %s (%s)", status, want, got.Verdict, got.Detail)
		}
		server.Close()
	}
}

func TestCheckURLUnreachableIsUnknownNotStale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := server.URL
	server.Close() // nothing is listening now

	got := newClaimChecker(true).check(Claim{Kind: "url", Value: dead})
	if got.Verdict != verdictUnknown {
		t.Fatalf("a network blip must never read as stale: got %s (%s)", got.Verdict, got.Detail)
	}
}

func TestNetworkClaimsSkippedWithoutNet(t *testing.T) {
	got := newClaimChecker(false).check(Claim{Kind: "url", Value: "https://example.com"})
	if got.Verdict != verdictUnknown || !strings.Contains(got.Detail, "--net") {
		t.Fatalf("offline default must skip and say so: %+v", got)
	}
}

func TestWorstVerdictPrefersTheStrongestEvidence(t *testing.T) {
	cases := []struct {
		claims []Claim
		want   string
	}{
		{nil, verdictNoClaims},
		{[]Claim{{Verdict: verdictFresh}}, verdictFresh},
		{[]Claim{{Verdict: verdictFresh}, {Verdict: verdictUnknown}}, verdictUnknown},
		{[]Claim{{Verdict: verdictUnknown}, {Verdict: verdictStale}}, verdictStale},
	}
	for _, tc := range cases {
		if got := worstVerdict(tc.claims); got != tc.want {
			t.Errorf("worstVerdict(%+v) = %s, want %s", tc.claims, got, tc.want)
		}
	}
}

func TestMarkMemoryRoundTripsThroughFrontmatter(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{MemoryDir: dir, GlobalConfig: GlobalConfig{}}
	memory := Memory{
		ID: "123", Name: "Memory 123", Description: "d", Type: "project",
		Created: time.Now().UTC(), Content: "the binary lives at /nope/gone",
	}
	path := filepath.Join(dir, "memory_123.md")
	if err := os.WriteFile(path, []byte(formatMemoryFile(memory)), 0644); err != nil {
		t.Fatal(err)
	}

	if !markMemory(cfg, "123", true) {
		t.Fatal("first mark should report a change")
	}
	raw, _ := os.ReadFile(path)
	reparsed := parseMemory(string(raw), "memory_123.md")
	if !reparsed.Stale {
		t.Error("stale flag did not survive the round trip")
	}
	if reparsed.Verified.IsZero() {
		t.Error("verified timestamp was not written")
	}
	if strings.TrimSpace(reparsed.Content) != memory.Content {
		t.Errorf("marking rewrote the body: %q", reparsed.Content)
	}

	// Rewriting must be byte-stable: a `verify --mark` cron rewrites the same
	// files forever, and the body must not grow a newline each pass.
	first, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(formatMemoryFile(parseMemory(string(first), "memory_123.md"))), 0644); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("format/parse is not idempotent:\n first: %q\nsecond: %q", first, second)
	}

	if markMemory(cfg, "123", true) {
		t.Error("re-marking an unchanged memory should be a no-op")
	}
	if !markMemory(cfg, "123", false) {
		t.Fatal("clearing the flag should report a change")
	}
	raw, _ = os.ReadFile(path)
	if parseMemory(string(raw), "memory_123.md").Stale {
		t.Error("stale flag was not cleared when the memory came back")
	}
}

func TestExtractClaimsTrimsSentencePunctuation(t *testing.T) {
	claims := extractClaims("the cache is at /var/cache/apt. Also see ~/ai/notes.md,")
	values := map[string]bool{}
	for _, c := range claims {
		values[c.Value] = true
	}
	if !values["/var/cache/apt"] || values["/var/cache/apt."] {
		t.Errorf("trailing period not trimmed: %+v", claims)
	}
	if !values["~/ai/notes.md"] || values["~/ai/notes.md,"] {
		t.Errorf("trailing comma not trimmed: %+v", claims)
	}
}

func TestMissingPathUnderAUniversalRootIsUnknown(t *testing.T) {
	checker := newClaimChecker(false)
	// /opt and /tmp exist on every box, so a missing child proves nothing
	// about being on the machine the memory described.
	for _, value := range []string{"/opt/no-such-service", "/tmp/no-such-thing"} {
		if got := checker.check(Claim{Kind: "path", Value: value}); got.Verdict != verdictUnknown {
			t.Errorf("%s: want unknown, got %s (%s)", value, got.Verdict, got.Detail)
		}
	}
}

func TestPathsOutsideOwnHomeAreNeverStale(t *testing.T) {
	checker := newClaimChecker(false)
	// These exist on every server and almost always describe a remote box.
	for _, value := range []string{
		"/etc/systemd/system/traefik.service",
		"/usr/local/bin/no-such-tool",
		"/var/log/some-service.log",
	} {
		got := checker.check(Claim{Kind: "path", Value: value})
		if got.Verdict == verdictStale {
			t.Errorf("%s: a server path must not read as stale (%s)", value, got.Detail)
		}
	}
}

func TestRemoteContextDiscountsPathClaims(t *testing.T) {
	dir := homeTempDir(t)
	missing := filepath.Join(dir, "gone.txt")
	checker := newClaimChecker(false)

	local := verifyMemoryContent(checker, "the build output is at "+missing)
	if len(local) != 1 || local[0].Verdict != verdictStale {
		t.Fatalf("a local claim should still be checkable: %+v", local)
	}

	for _, remote := range []string{
		"ssh rbm21 then look at " + missing,
		"rcx dk1 \"ls " + missing + "\"",
		"on 192.168.1.117 the file is " + missing,
		"root@pve2 keeps it at " + missing,
	} {
		claims := verifyMemoryContent(checker, remote)
		for _, c := range claims {
			if c.Kind == "path" && c.Verdict == verdictStale {
				t.Errorf("remote context %q: path must be unknown, got stale", remote)
			}
		}
	}
}

func TestHasRemoteContextIgnoresOrdinaryProse(t *testing.T) {
	for _, text := range []string{
		"the parser lives in ~/ai/machin/src",
		"version 1.9.1 shipped",
	} {
		if hasRemoteContext(text) {
			t.Errorf("false remote-context detection on %q", text)
		}
	}
}

func TestTruncatedTemplatePathsAreNotClaims(t *testing.T) {
	// "~/.cache/ms-playwright/chromium-<version>" gets cut at the placeholder;
	// the resulting prefix names nothing and must not be reported.
	for _, text := range []string{
		"binaries land in ~/.cache/ms-playwright/chromium-<version>",
		"the artifact is ~/ai/scan-and-fill/dist/scan-and-fill-$TAG",
	} {
		for _, c := range extractClaims(text) {
			if c.Kind == "path" {
				t.Errorf("template fragment reported as a path claim: %q", c.Value)
			}
		}
	}
}
