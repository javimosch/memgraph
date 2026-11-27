package cloudsync

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Finding locates a likely secret without ever carrying it.
type Finding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Rule        string `json:"rule"`
	Fingerprint string `json:"fingerprint"`
}

// Pattern scanning misses secrets. It is a net, not a guarantee.
var secretRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"memgraph-token", regexp.MustCompile(`\bmgc_[A-Za-z0-9_-]{43}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----`)},
	{"aws-access-key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b`)},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"api-key-sk", regexp.MustCompile(`\bsk-(ant-|proj-|or-v1-)?[A-Za-z0-9_-]{32,}`)},
	{"stripe-live-key", regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{20,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"url-password", regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^/\s:@"'<>]+:[^/\s@"'<>$*{}]{6,}@`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{20,}`)},
}

// Scan checks the lines of data that are not already in old.
func Scan(file string, old, data []byte, allowed map[string]bool) []Finding {
	have := map[string]bool{}
	for _, line := range strings.Split(string(old), "\n") {
		have[line] = true
	}
	var out []Finding
	for i, line := range strings.Split(string(data), "\n") {
		if have[line] {
			continue
		}
		for _, rule := range secretRules {
			for _, m := range rule.re.FindAllString(line, -1) {
				sum := sha256.Sum256([]byte(rule.name + "\x00" + m))
				fp := hex.EncodeToString(sum[:8])
				if !allowed[fp] {
					out = append(out, Finding{File: file, Line: i + 1, Rule: rule.name, Fingerprint: fp})
				}
			}
		}
	}
	return out
}
