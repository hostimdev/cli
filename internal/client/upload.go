package client

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/hostimdev/cli/api"
)

// MaxUploadSize is the largest source archive the API accepts, after gzip.
const MaxUploadSize int64 = 500 << 20

// maxUploadSize is a variable so tests can lower the cap without writing 500 MiB.
var maxUploadSize = MaxUploadSize

var errTooLarge = errors.New("source archive is too large")

type sourceEntry struct {
	rel  string
	size int64
	link string
}

// Pack writes a deterministic .tar.gz of dir to a temporary file and returns
// its path, hex SHA-256 and size. The same tree always gives the same hash, so
// a re-deploy is recognised as an existing upload.
func Pack(dir string) (path, sha256Hex string, size int64, err error) {
	if dir == "" {
		dir = "."
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", 0, err
	}

	entries, err := collectFiles(dir)
	if err != nil {
		return "", "", 0, err
	}

	tmp, err := os.CreateTemp("", "hostim-source-*.tar.gz")
	if err != nil {
		return "", "", 0, err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	lw := &limitWriter{w: io.MultiWriter(tmp, hash), max: maxUploadSize}
	gz := gzip.NewWriter(lw)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		if err := writeEntry(tw, dir, e); err != nil {
			if errors.Is(err, errTooLarge) {
				return "", "", 0, tooLargeError(entries)
			}
			return "", "", 0, err
		}
	}
	if err := tw.Close(); err != nil {
		if errors.Is(err, errTooLarge) {
			return "", "", 0, tooLargeError(entries)
		}
		return "", "", 0, err
	}
	if err := gz.Close(); err != nil {
		if errors.Is(err, errTooLarge) {
			return "", "", 0, tooLargeError(entries)
		}
		return "", "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", "", 0, err
	}

	ok = true
	return tmpName, hex.EncodeToString(hash.Sum(nil)), lw.n, nil
}

// Upload packs dir and stores it through the API, returning the archive's
// sha256. progress, when set, is called with short status lines.
func Upload(ctx context.Context, c *api.ClientWithResponses, project, dir string, progress func(string)) (string, error) {
	if progress != nil {
		progress("Packing source...")
	}
	path, sha, size, err := Pack(dir)
	if err != nil {
		return "", err
	}
	defer os.Remove(path)

	if progress != nil {
		progress(fmt.Sprintf("Uploading source (%.1f MiB)...", float64(size)/(1<<20)))
	}
	resp, err := c.CreateUploadWithResponse(ctx, project, api.UploadRequest{Sha256: sha, Size: size})
	if err != nil {
		return "", err
	}
	if err := Check(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	up := resp.JSON200
	if up == nil {
		return "", ErrEmptyResponse
	}
	if up.Exists {
		if progress != nil {
			progress("Source archive already stored, skipping upload.")
		}
		return sha, nil
	}
	if up.UploadUrl == nil || *up.UploadUrl == "" {
		return "", fmt.Errorf("the API did not return an upload URL")
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, *up.UploadUrl, f)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer put.Body.Close()
	if put.StatusCode < 200 || put.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(put.Body, 64<<10))
		return "", fmt.Errorf("upload failed: %s: %s", put.Status, strings.TrimSpace(string(body)))
	}
	return sha, nil
}

// HasDockerfile reports whether dir contains dockerfile, defaulting to
// "Dockerfile" when the path is empty.
func HasDockerfile(dir, dockerfile string) bool {
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(dockerfile)))
	return err == nil && !info.IsDir()
}

func collectFiles(dir string) ([]sourceEntry, error) {
	var entries []sourceEntry

	if paths, ok := gitFiles(dir); ok {
		for _, rel := range paths {
			full := filepath.Join(dir, filepath.FromSlash(rel))
			info, err := os.Lstat(full)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			addEntry(dir, full, rel, info, &entries)
		}
	} else {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == dir {
				return nil
			}
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			addEntry(dir, path, rel, info, &entries)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	pm, err := dockerignoreMatcher(dir)
	if err != nil {
		return nil, err
	}
	if pm != nil {
		kept := entries[:0]
		for _, e := range entries {
			if isDockerfile(e.rel) || e.rel == ".dockerignore" {
				kept = append(kept, e)
				continue
			}
			matched, err := pm.MatchesOrParentMatches(e.rel)
			if err != nil {
				return nil, err
			}
			if !matched {
				kept = append(kept, e)
			}
		}
		entries = kept
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	return entries, nil
}

func addEntry(root, full, rel string, info fs.FileInfo, entries *[]sourceEntry) {
	if skipRel(rel) {
		return
	}
	e := sourceEntry{rel: rel, size: info.Size()}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		link, ok := symlinkInside(root, full)
		if !ok {
			return
		}
		e.link = link
		e.size = 0
	case mode.IsRegular():
	default:
		return
	}
	*entries = append(*entries, e)
}

// skipRel drops paths the platform must never see, wherever they are in the tree.
func skipRel(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == ".git" || p == "node_modules" {
			return true
		}
	}
	base := parts[len(parts)-1]
	if base == ".env" {
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		switch base {
		case ".env.example", ".env.sample", ".env.template":
			return false
		}
		return true
	}
	return false
}

func isDockerfile(rel string) bool {
	base := filepath.Base(rel)
	return base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") ||
		base == "Containerfile" || strings.HasPrefix(base, "Containerfile.")
}

// gitFiles lists tracked, modified and untracked non-ignored files relative to
// dir, or reports false when dir is not a git work tree or git is missing.
func gitFiles(dir string) ([]string, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false
	}
	out, err = exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, filepath.ToSlash(p))
	}
	return paths, true
}

func dockerignoreMatcher(dir string) (*patternmatcher.PatternMatcher, error) {
	f, err := os.Open(filepath.Join(dir, ".dockerignore"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	patterns, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return patternmatcher.New(patterns)
}

// symlinkInside returns the link target when it stays inside root.
func symlinkInside(root, path string) (string, bool) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(path), target)
	}
	rel, err := filepath.Rel(root, filepath.Clean(resolved))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return target, true
}

func writeEntry(tw *tar.Writer, root string, e sourceEntry) error {
	h := &tar.Header{
		Name:    e.rel,
		Mode:    0o644,
		ModTime: time.Unix(0, 0).UTC(),
		Uid:     0,
		Gid:     0,
	}
	full := filepath.Join(root, filepath.FromSlash(e.rel))
	if e.link != "" {
		h.Typeflag = tar.TypeSymlink
		h.Linkname = e.link
		h.Mode = 0o777
		return tw.WriteHeader(h)
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o111 != 0 {
		h.Mode = 0o755
	}
	h.Typeflag = tar.TypeReg
	h.Size = info.Size()
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func tooLargeError(entries []sourceEntry) error {
	sorted := append([]sourceEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].size > sorted[j].size })
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}
	var lines []string
	for _, e := range sorted {
		lines = append(lines, fmt.Sprintf("  %8.1f MiB  %s", float64(e.size)/(1<<20), e.rel))
	}
	return fmt.Errorf("source is larger than the %d MiB upload limit (compressed); add the largest files to .dockerignore:\n%s",
		maxUploadSize>>20, strings.Join(lines, "\n"))
}

type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.n+int64(len(p)) > w.max {
		return 0, errTooLarge
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}
