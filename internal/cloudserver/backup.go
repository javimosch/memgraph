package cloudserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// A backup is a gzipped tar of one git bundle per project, tokens.json and
// manifest.json. The manifest comes last, so a stream that was cut short can
// never pass for a backup. Nothing in it depends on when it was made, so
// unchanged data gives unchanged bytes.
const (
	backupFormat   = 1
	manifestName   = "manifest.json"
	tokensName     = "tokens.json"
	maxBackupEntry = 4 << 30
	maxSmallEntry  = 16 << 20
)

// BackupProject describes one project in a backup.
type BackupProject struct {
	Name   string `json:"name"`
	Head   string `json:"head,omitempty"`   // empty for a project with no pushes
	SHA256 string `json:"sha256,omitempty"` // of the bundle
}

// BackupSummary says what a backup holds.
type BackupSummary struct {
	Format       int             `json:"format"`
	Projects     []BackupProject `json:"projects"`
	TokensSHA256 string          `json:"tokens_sha256"`
	Tokens       int             `json:"tokens"`
}

// adminGit runs git without any user or system configuration.
func (d Data) adminGit(args ...string) (string, error) {
	settings := []string{"-c", "safe.directory=*", "-c", "pack.threads=1", "-c", "pack.windowMemory=32m",
		"-c", "fetch.fsckObjects=true"}
	for _, kv := range gitDurability() {
		settings = append(settings, "-c", kv[0]+"="+kv[1])
	}
	cmd := exec.Command("git", append(settings, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1"}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeEntry(t *tar.Writer, name string, size int64, r io.Reader) error {
	// No times or owners: they would make equal data look different.
	if err := t.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	_, err := io.CopyN(t, r, size)
	return err
}

// bundle writes a project's main branch to path and returns its description.
// It is safe while the server runs: git takes the branch as it is when it
// starts, and a bundle holds everything that commit needs.
func (d Data) bundle(name, path string) (BackupProject, error) {
	p := BackupProject{Name: name}
	refs, err := d.adminGit("-C", d.repo(name), "for-each-ref", "--format=%(refname)", "refs/heads/main")
	if err != nil || refs == "" {
		return p, err // nothing was ever pushed
	}
	if _, err := d.adminGit("-C", d.repo(name), "bundle", "create", "--quiet", path, "refs/heads/main"); err != nil {
		return p, err
	}
	// Read the head from the bundle: a push may have moved the branch since.
	heads, err := d.adminGit("bundle", "list-heads", path)
	if err != nil {
		return p, err
	}
	fields := strings.Fields(heads)
	if len(fields) != 2 || fields[1] != "refs/heads/main" {
		return p, fmt.Errorf("unexpected bundle contents for %s: %q", name, heads)
	}
	p.Head = fields[0]
	p.SHA256, err = fileSHA256(path)
	return p, err
}

// Backup writes a backup of every project and the token file to w. One bundle
// at a time is staged on disk, so it needs free space for the largest project.
func (d Data) Backup(w io.Writer) (*BackupSummary, error) {
	if _, err := os.Stat(d.repos()); err != nil {
		return nil, fmt.Errorf("not a server data directory: %w", err)
	}
	// Tokens first: projects are never removed, so each token's projects will
	// be in the list read afterwards.
	tokens, err := d.Tokens()
	if err != nil {
		return nil, err
	}
	names, err := d.Projects()
	if err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(d.Dir, "staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	gz := gzip.NewWriter(w)
	t := tar.NewWriter(gz)
	summary := &BackupSummary{Format: backupFormat, Projects: []BackupProject{}, Tokens: len(tokens)}

	for _, name := range names {
		path := filepath.Join(staging, name+".bundle")
		p, err := d.bundle(name, path)
		if err != nil {
			return nil, err
		}
		if p.Head != "" {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			info, err := f.Stat()
			if err == nil {
				err = writeEntry(t, "bundles/"+name+".bundle", info.Size(), f)
			}
			f.Close()
			os.Remove(path)
			if err != nil {
				return nil, err
			}
		}
		summary.Projects = append(summary.Projects, p)
	}
	if tokens == nil {
		tokens = []Token{}
	}
	raw, err := json.Marshal(tokens)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	summary.TokensSHA256 = hex.EncodeToString(sum[:])
	if err := writeEntry(t, tokensName, int64(len(raw)), bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	manifest, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	if err := writeEntry(t, manifestName, int64(len(manifest)), bytes.NewReader(manifest)); err != nil {
		return nil, err
	}
	return summary, errors.Join(t.Close(), gz.Close())
}

// unpack extracts a backup into dir, accepting only the entries a backup has.
func unpack(r io.Reader, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not a backup: %w", err)
	}
	t := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := t.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("backup is damaged or incomplete: %w", err)
		}
		limit := int64(maxSmallEntry)
		name, isBundle := strings.CutPrefix(h.Name, "bundles/")
		if isBundle {
			project, ok := strings.CutSuffix(name, ".bundle")
			isBundle = ok && projectName.MatchString(project)
			limit = maxBackupEntry
		}
		if h.Typeflag != tar.TypeReg || seen[h.Name] || seen[manifestName] ||
			!(isBundle || h.Name == tokensName || h.Name == manifestName) || h.Size > limit {
			return fmt.Errorf("backup holds an unexpected entry: %q", h.Name)
		}
		seen[h.Name] = true
		path := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, t)
		if err = errors.Join(err, f.Close()); err != nil {
			return fmt.Errorf("backup is damaged or incomplete: %w", err)
		}
	}
	// Reading to the end checks the gzip checksum.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("backup is damaged or incomplete: %w", err)
	}
	if !seen[manifestName] || !seen[tokensName] {
		return errors.New("backup is incomplete: it has no manifest")
	}
	return nil
}

// vacant reports whether a restore may create dir: absent or empty.
func vacant(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty; restore only creates a new data directory", dir)
	}
	return nil
}

// Restore creates the data directory from a backup. It never writes into an
// existing one, and creates nothing unless every project checks out.
func (d Data) Restore(r io.Reader, withTokens bool) (*BackupSummary, error) {
	target, err := filepath.Abs(d.Dir)
	if err != nil {
		return nil, err
	}
	if err := vacant(target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return nil, err
	}
	// Built next to the target, then renamed into place.
	work, err := os.MkdirTemp(filepath.Dir(target), ".restore-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	files, built := filepath.Join(work, "files"), Data{Dir: filepath.Join(work, "data")}
	if err := unpack(r, files); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(files, manifestName))
	if err != nil {
		return nil, err
	}
	var summary BackupSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, fmt.Errorf("bad manifest: %w", err)
	}
	if summary.Format != backupFormat {
		return nil, fmt.Errorf("backup format %d is not supported", summary.Format)
	}
	if err := built.init(); err != nil {
		return nil, err
	}
	var listed []string
	for _, p := range summary.Projects {
		if !projectName.MatchString(p.Name) || slices.Contains(listed, p.Name) {
			return nil, fmt.Errorf("bad project in manifest: %q", p.Name)
		}
		listed = append(listed, p.Name)
		repo := built.repo(p.Name)
		if err := built.initRepo(repo); err != nil {
			return nil, err
		}
		path := filepath.Join(files, "bundles", p.Name+".bundle")
		if p.Head == "" {
			continue
		}
		if sum, err := fileSHA256(path); err != nil || sum != p.SHA256 {
			return nil, fmt.Errorf("bundle of %s is missing or does not match the manifest", p.Name)
		}
		if _, err := built.adminGit("-C", repo, "fetch", "--quiet", "--no-tags", path, "refs/heads/main:refs/heads/main"); err != nil {
			return nil, err
		}
		if _, err := built.adminGit("-C", repo, "fsck", "--full", "--strict", "--no-progress"); err != nil {
			return nil, err
		}
		head, err := built.adminGit("-C", repo, "rev-parse", "refs/heads/main")
		if err != nil || head != p.Head {
			return nil, fmt.Errorf("%s restored to %s, not %s", p.Name, head, p.Head)
		}
		os.Remove(filepath.Join(repo, "FETCH_HEAD"))
	}
	bundles, err := os.ReadDir(filepath.Join(files, "bundles"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, b := range bundles {
		if !slices.Contains(listed, strings.TrimSuffix(b.Name(), ".bundle")) {
			return nil, fmt.Errorf("backup holds a bundle the manifest does not list: %s", b.Name())
		}
	}
	rawTokens, err := os.ReadFile(filepath.Join(files, tokensName))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(rawTokens); hex.EncodeToString(sum[:]) != summary.TokensSHA256 {
		return nil, errors.New("tokens do not match the manifest")
	}
	var tokens []Token
	if err := json.Unmarshal(rawTokens, &tokens); err != nil {
		return nil, fmt.Errorf("bad tokens: %w", err)
	}
	if !withTokens {
		tokens, summary.Tokens = []Token{}, 0
	}
	if err := built.saveTokens(tokens); err != nil {
		return nil, err
	}
	// Removing succeeds only if the target is still empty.
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(built.Dir, target); err != nil {
		return nil, err
	}
	return &summary, syncDir(filepath.Dir(target))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
