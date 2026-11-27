// Package cloudserver serves memgraph cloud projects over git smart HTTP. git
// does storage and transfer; this package adds access control, size limits,
// push rules and upkeep. See docs/cloud-server.md.
package cloudserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/javimosch/memgraph/internal/fsx"
)

// TokenPrefix marks a server token, so secret scanners can recognise one.
const TokenPrefix = "mgc_"

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Token is what the server keeps about one machine's token. The token itself
// is shown once and never stored.
type Token struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	SHA256   string    `json:"sha256"`
	Projects []string  `json:"projects"`
	Created  time.Time `json:"created"`
}

// Data is a server data directory. Its admin methods work on the files
// directly and are safe while the server runs. Run them as the server's user.
type Data struct{ Dir string }

func (d Data) repos() string              { return filepath.Join(d.Dir, "repos") }
func (d Data) repo(project string) string { return filepath.Join(d.repos(), project+".git") }
func (d Data) tokensPath() string         { return filepath.Join(d.Dir, "tokens.json") }

// init creates the directory layout, readable by its owner only.
func (d Data) init() error {
	for _, dir := range []string{d.Dir, d.repos()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// locked serializes admin changes.
func (d Data) locked(change func() error) error {
	if err := d.init(); err != nil {
		return err
	}
	unlock, err := fsx.Lock(filepath.Join(d.Dir, "admin"))
	if err != nil {
		return err
	}
	defer unlock()
	return change()
}

func (d Data) hasProject(name string) bool {
	if !projectName.MatchString(name) {
		return false
	}
	info, err := os.Stat(d.repo(name))
	return err == nil && info.IsDir()
}

// CreateProject adds an empty project.
func (d Data) CreateProject(name string) error {
	if !projectName.MatchString(name) {
		return errors.New("project names use a-z, 0-9 and '-', up to 63 characters")
	}
	return d.locked(func() error {
		if d.hasProject(name) {
			return fmt.Errorf("project %q already exists", name)
		}
		// Built aside and renamed, so a running server never sees half a repo.
		staging, err := os.MkdirTemp(d.Dir, "staging-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		repo := filepath.Join(staging, "repo.git")
		cmd := exec.Command("git", "init", "--quiet", "--bare", "--template=", "--initial-branch=main", repo)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + staging, "GIT_CONFIG_NOSYSTEM=1"}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %v: %s", err, out)
		}
		return os.Rename(repo, d.repo(name))
	})
}

// Projects lists the project names.
func (d Data) Projects() ([]string, error) {
	entries, err := os.ReadDir(d.repos())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".git"); ok && e.IsDir() && projectName.MatchString(name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// Tokens lists the stored tokens.
func (d Data) Tokens() ([]Token, error) {
	raw, err := os.ReadFile(d.tokensPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tokens []Token
	if err := json.Unmarshal(raw, &tokens); err != nil {
		return nil, fmt.Errorf("%s: %w", d.tokensPath(), err)
	}
	return tokens, nil
}

func (d Data) saveTokens(tokens []Token) error {
	raw, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFileAtomic(d.tokensPath(), raw, 0600)
}

func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// CreateToken makes a token for the given projects and returns the secret,
// which cannot be recovered later.
func (d Data) CreateToken(name string, projects []string) (secret string, token Token, err error) {
	if strings.TrimSpace(name) == "" {
		return "", token, errors.New("a token needs a name, such as the machine that uses it")
	}
	if len(projects) == 0 {
		return "", token, errors.New("a token needs at least one project")
	}
	raw, err := randomBytes(32)
	if err != nil {
		return "", token, err
	}
	id, err := randomBytes(4)
	if err != nil {
		return "", token, err
	}
	secret = TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	slices.Sort(projects)
	token = Token{ID: hex.EncodeToString(id), Name: name, SHA256: hashToken(secret),
		Projects: slices.Compact(projects), Created: time.Now().UTC().Truncate(time.Second)}
	err = d.locked(func() error {
		for _, p := range token.Projects {
			if !d.hasProject(p) {
				return fmt.Errorf("no project %q; create it first", p)
			}
		}
		tokens, err := d.Tokens()
		if err != nil {
			return err
		}
		return d.saveTokens(append(tokens, token))
	})
	if err != nil {
		return "", Token{}, err
	}
	return secret, token, nil
}

// RevokeToken removes a token. It applies from the next request.
func (d Data) RevokeToken(id string) error {
	return d.locked(func() error {
		tokens, err := d.Tokens()
		if err != nil {
			return err
		}
		kept := slices.DeleteFunc(slices.Clone(tokens), func(t Token) bool { return t.ID == id })
		if len(kept) == len(tokens) {
			return fmt.Errorf("no token with id %q", id)
		}
		return d.saveTokens(kept)
	})
}

// authorize returns the projects a secret may use.
func (d Data) authorize(secret string) ([]string, bool) {
	tokens, err := d.Tokens()
	if err != nil {
		return nil, false
	}
	want := []byte(hashToken(secret))
	for _, t := range tokens {
		if subtle.ConstantTimeCompare(want, []byte(t.SHA256)) == 1 {
			return t.Projects, true
		}
	}
	return nil, false
}
