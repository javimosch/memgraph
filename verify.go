package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Verdicts. A claim is only ever "stale" when the evidence is positive:
// something local said the target should be here and it is not. Every
// ambiguous outcome — an unreachable host, a timeout, a path whose whole
// neighbourhood is absent (a remote box, another machine) — is "unknown".
// A network blip must never demote a true memory.
const (
	verdictFresh    = "fresh"
	verdictStale    = "stale"
	verdictUnknown  = "unknown"
	verdictNoClaims = "no_claims"
)

var (
	urlRe      = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `\)\]},;]+`)
	pathRe     = regexp.MustCompile(`(?:^|[\s(\[<"'` + "`" + `])(~?/[A-Za-z0-9._+\-]+(?:/[A-Za-z0-9._+\-]+)+/?)`)
	repoRe     = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9._\-]+)/([A-Za-z0-9._\-]+)/?$`)
	datePathRe = regexp.MustCompile(`^/\d+(/\d+)+$`)
)

// extractClaims pulls externally checkable assertions out of a memory's text.
// URLs are consumed first and blanked out, so a path inside a URL is not also
// reported as a filesystem path.
func extractClaims(text string) []Claim {
	var claims []Claim
	seen := map[string]bool{}

	add := func(kind, value string) {
		key := kind + "\x00" + value
		if value == "" || seen[key] {
			return
		}
		seen[key] = true
		claims = append(claims, Claim{Kind: kind, Value: value})
	}

	stripped := urlRe.ReplaceAllStringFunc(text, func(m string) string {
		raw := strings.TrimRight(m, ".,;:!?")
		if repo := repoRe.FindStringSubmatch(strings.TrimSuffix(raw, ".git")); repo != nil {
			add("repo", repo[1]+"/"+repo[2])
		} else {
			add("url", raw)
		}
		return strings.Repeat(" ", len(m))
	})

	for _, m := range pathRe.FindAllStringSubmatch(stripped, -1) {
		// Sentence punctuation is inside the segment charset ("." is legal in
		// a filename), so a path ending a sentence arrives as "/tmp/amk." and
		// would report a file that was never claimed to exist.
		p := strings.TrimRight(m[1], "/")
		p = strings.TrimRight(p, ".,;:!?")
		if isNoisePath(p) {
			continue
		}
		add("path", p)
	}
	return claims
}

// isNoisePath rejects the prose that looks like a path but never was one:
// bare option-style slashes, and the single-segment roots that carry no
// information ("/home", "/opt").
func isNoisePath(p string) bool {
	if len(p) < 5 {
		return true
	}
	body := strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/")
	if strings.Count(body, "/") < 1 {
		return true
	}
	// "and/or", "read/write" style prose never starts with ~ or /, so the
	// remaining risk is date-like or version-like runs.
	if datePathRe.MatchString(p) {
		return true
	}
	// A trailing dash or underscore means the match was cut short by a
	// placeholder the charset does not cover — "chromium-<version>",
	// "scan-and-fill-$TAG". The real path is unknown, so claim nothing.
	if strings.HasSuffix(p, "-") || strings.HasSuffix(p, "_") {
		return true
	}
	return false
}

// remoteMarkers are the generic signs that a memory describes work done on
// another machine. A path in such a memory names a filesystem this process
// cannot see, so it is unverifiable from here — not missing.
var remoteMarkers = regexp.MustCompile(`(?i)\b(ssh|scp|rsync|sshfs|rcx|remotecmd|pct exec|lxc-attach|docker exec|kubectl exec|systemctl --host)\b|[A-Za-z0-9._-]+@[A-Za-z0-9.-]+|\b\d{1,3}(\.\d{1,3}){3}\b`)

func hasRemoteContext(text string) bool {
	return remoteMarkers.MatchString(text)
}

// verifyMemoryContent is the single entry point both the CLI and the MCP tool
// use: extract, discount what cannot be checked from here, then resolve.
func verifyMemoryContent(checker *claimChecker, content string) []Claim {
	claims := extractClaims(content)
	if hasRemoteContext(content) {
		for i := range claims {
			if claims[i].Kind == "path" {
				claims[i].Verdict = verdictUnknown
				claims[i].Detail = "memory describes another machine; its filesystem cannot be checked from here"
			}
		}
	}
	// Claims already decided above are passed through untouched.
	var pending []Claim
	var indexes []int
	for i, claim := range claims {
		if claim.Verdict == "" {
			pending = append(pending, claim)
			indexes = append(indexes, i)
		}
	}
	for i, resolved := range checker.checkAll(pending) {
		claims[indexes[i]] = resolved
	}
	return claims
}

// claimChecker resolves claims, memoizing by claim so a URL that appears in
// forty memories is fetched once.
type claimChecker struct {
	net    bool
	client *http.Client
	mu     sync.Mutex
	cache  map[string]Claim
	sem    chan struct{}
	home   string
	ghOnce sync.Once
	ghPath string
}

func newClaimChecker(useNet bool) *claimChecker {
	home, _ := os.UserHomeDir()
	return &claimChecker{
		net:    useNet,
		client: &http.Client{Timeout: 8 * time.Second},
		cache:  map[string]Claim{},
		sem:    make(chan struct{}, 8),
		home:   home,
	}
}

func (c *claimChecker) gh() string {
	c.ghOnce.Do(func() {
		if p, err := exec.LookPath("gh"); err == nil {
			c.ghPath = p
		}
	})
	return c.ghPath
}

// checkAll resolves a memory's claims concurrently.
func (c *claimChecker) checkAll(claims []Claim) []Claim {
	out := make([]Claim, len(claims))
	var wg sync.WaitGroup
	for i, claim := range claims {
		key := claim.Kind + "\x00" + claim.Value
		c.mu.Lock()
		cached, ok := c.cache[key]
		c.mu.Unlock()
		if ok {
			out[i] = cached
			continue
		}
		wg.Add(1)
		go func(i int, claim Claim, key string) {
			defer wg.Done()
			c.sem <- struct{}{}
			defer func() { <-c.sem }()
			resolved := c.check(claim)
			c.mu.Lock()
			c.cache[key] = resolved
			c.mu.Unlock()
			out[i] = resolved
		}(i, claim, key)
	}
	wg.Wait()
	return out
}

func (c *claimChecker) check(claim Claim) Claim {
	switch claim.Kind {
	case "path":
		return c.checkPath(claim)
	case "url":
		if !c.net {
			claim.Verdict = verdictUnknown
			claim.Detail = "network check skipped (pass --net)"
			return claim
		}
		return c.checkURL(claim)
	case "repo":
		if !c.net {
			claim.Verdict = verdictUnknown
			claim.Detail = "network check skipped (pass --net)"
			return claim
		}
		return c.checkRepo(claim)
	}
	claim.Verdict = verdictUnknown
	return claim
}

// checkPath is the rule that keeps this command honest. A missing file is only
// evidence of staleness when its parent directory exists locally: that means we
// are looking at the right machine and the thing is genuinely gone. If the
// parent is absent too, the path most likely belongs to another host (rbm21,
// dk1, a container) and we say so rather than guessing.
func (c *claimChecker) checkPath(claim Claim) Claim {
	p := claim.Value
	if strings.HasPrefix(p, "~") {
		if c.home == "" {
			claim.Verdict = verdictUnknown
			claim.Detail = "cannot resolve home directory"
			return claim
		}
		p = filepath.Join(c.home, strings.TrimPrefix(p, "~"))
	}
	if _, err := os.Stat(p); err == nil {
		claim.Verdict = verdictFresh
		return claim
	}
	if !c.underOwnHome(p) {
		claim.Verdict = verdictUnknown
		claim.Detail = "outside this user's home — system paths usually describe a server, not this machine"
		return claim
	}
	parent := filepath.Dir(p)
	if info, err := os.Stat(parent); err == nil && info.IsDir() && isSpecificDir(parent) {
		claim.Verdict = verdictStale
		claim.Detail = "missing, but " + parent + " exists locally"
		return claim
	}
	claim.Verdict = verdictUnknown
	claim.Detail = "absent locally and so is " + parent + " (likely another host)"
	return claim
}

// underOwnHome is the boundary of what this process can honestly judge.
func (c *claimChecker) underOwnHome(p string) bool {
	if c.home == "" {
		return false
	}
	rel, err := filepath.Rel(c.home, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isSpecificDir rejects the top-level directories that exist on every Linux
// box — /tmp, /opt, /var, /home. Their presence is no evidence that this is
// the machine the memory described, so a missing child under one of them is
// unknown rather than stale. Two segments deep, the directory is specific
// enough to be a real signal.
func isSpecificDir(dir string) bool {
	trimmed := strings.Trim(filepath.Clean(dir), "/")
	if trimmed == "" || trimmed == "." {
		return false
	}
	return strings.Count(trimmed, "/") >= 1
}

// checkURL treats auth walls as proof of life. A front door that answers 401 or
// 403 is running; only a definitive 404/410 is evidence the thing is gone.
func (c *claimChecker) checkURL(claim Claim) Claim {
	status, err := c.probe(http.MethodHead, claim.Value)
	if err == nil && (status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented) {
		status, err = c.probe(http.MethodGet, claim.Value)
	}
	if err != nil {
		claim.Verdict = verdictUnknown
		claim.Detail = "unreachable: " + condense(err.Error())
		return claim
	}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		claim.Verdict = verdictStale
		claim.Detail = http.StatusText(status)
	case status < 400 || status == http.StatusUnauthorized || status == http.StatusForbidden:
		claim.Verdict = verdictFresh
		claim.Detail = http.StatusText(status)
	default:
		claim.Verdict = verdictUnknown
		claim.Detail = http.StatusText(status)
	}
	return claim
}

func (c *claimChecker) probe(method, target string) (int, error) {
	if _, err := url.Parse(target); err != nil {
		return 0, err
	}
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "memgraph-verify/"+Version)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// checkRepo prefers gh, because an anonymous 404 cannot tell a deleted repo
// from a private one — and this estate has many private repos.
func (c *claimChecker) checkRepo(claim Claim) Claim {
	if gh := c.gh(); gh != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, gh, "repo", "view", claim.Value, "--json", "name")
		if err := cmd.Run(); err == nil {
			claim.Verdict = verdictFresh
			claim.Detail = "gh repo view"
			return claim
		} else if ctx.Err() == nil {
			claim.Verdict = verdictStale
			claim.Detail = "gh repo view failed (not found or no access)"
			return claim
		}
		claim.Verdict = verdictUnknown
		claim.Detail = "gh repo view timed out"
		return claim
	}

	status, err := c.probe(http.MethodHead, "https://github.com/"+claim.Value)
	if err != nil {
		claim.Verdict = verdictUnknown
		claim.Detail = "unreachable: " + condense(err.Error())
		return claim
	}
	if status == http.StatusNotFound {
		claim.Verdict = verdictUnknown
		claim.Detail = "404 anonymously — may be private; install gh to resolve"
		return claim
	}
	if status < 400 {
		claim.Verdict = verdictFresh
		return claim
	}
	claim.Verdict = verdictUnknown
	claim.Detail = http.StatusText(status)
	return claim
}

// worstVerdict collapses a memory's claims: any stale wins, then any unknown.
func worstVerdict(claims []Claim) string {
	if len(claims) == 0 {
		return verdictNoClaims
	}
	verdict := verdictFresh
	for _, claim := range claims {
		if claim.Verdict == verdictStale {
			return verdictStale
		}
		if claim.Verdict == verdictUnknown {
			verdict = verdictUnknown
		}
	}
	return verdict
}

func condense(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
