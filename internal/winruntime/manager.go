package winruntime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
)

var (
	// ErrUnknownRuntime is returned when a runtime name is not in the catalog.
	ErrUnknownRuntime = errors.New("winruntime: unknown runtime")
	// ErrUnsupportedArch is returned when no asset exists for the target GOARCH.
	ErrUnsupportedArch = errors.New("winruntime: unsupported architecture")
	// ErrChecksumMismatch is returned when a downloaded archive fails SHA256 verification.
	ErrChecksumMismatch = errors.New("winruntime: sha256 checksum mismatch")
)

const metadataFile = ".installed.json"

// Progress reports download/extraction progress during Ensure.
type Progress struct {
	Runtime    Name
	Stage      string // "downloading", "verifying", "extracting"
	Bytes      int64
	TotalBytes int64
}

// Status reports whether a runtime is installed in the managed directory.
type Status struct {
	Name      Name
	Installed bool
	Dir       string
	Version   string
	Arch      string
	Missing   []string
}

type installedMeta struct {
	Name        Name   `json:"name"`
	Version     string `json:"version"`
	Arch        string `json:"arch"`
	SHA256      string `json:"sha256,omitempty"`
	InstalledAt string `json:"installedAt"`
}

// Options configure a Manager.
type Options struct {
	BaseDir    string
	Proxy      string
	HTTPClient *http.Client
	Catalog    map[Name]Spec
	GOARCH     string // defaults to runtime.GOARCH
}

// Manager downloads, verifies and installs pinned Windows native runtimes.
type Manager struct {
	baseDir string
	http    *http.Client
	catalog map[Name]Spec
	goarch  string
}

// New creates a Manager with defaults filled in.
func New(opts Options) *Manager {
	base := opts.BaseDir
	if base == "" {
		base = RuntimesDir()
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = hub.NewHTTPClient(opts.Proxy, 60*time.Second)
	}
	cat := opts.Catalog
	if cat == nil {
		cat = DefaultCatalog()
	}
	arch := opts.GOARCH
	if arch == "" {
		arch = runtime.GOARCH
	}
	return &Manager{baseDir: base, http: hc, catalog: cat, goarch: arch}
}

// BaseDir returns the root directory where runtimes are stored.
func (m *Manager) BaseDir() string { return m.baseDir }

// Arch returns the target GOARCH this Manager resolves assets for.
func (m *Manager) Arch() string { return m.goarch }

// Spec returns the catalog specification for name.
func (m *Manager) Spec(name Name) (Spec, bool) {
	s, ok := m.catalog[name]
	return s, ok
}

// Asset returns the downloadable archive for name on this Manager's GOARCH.
func (m *Manager) Asset(name Name) (Asset, bool) {
	s, ok := m.catalog[name]
	if !ok {
		return Asset{}, false
	}
	a, ok := s.Assets[m.goarch]
	return a, ok && a.URL != ""
}

// AllStatuses returns the install status of every managed native runtime
// in OrderedNames order.
func (m *Manager) AllStatuses() []Status {
	names := OrderedNames()
	out := make([]Status, 0, len(names))
	for _, n := range names {
		out = append(out, m.Status(n))
	}
	return out
}

// Status checks whether name is installed under BaseDir.
func (m *Manager) Status(name Name) Status {
	dir := RuntimeDir(m.baseDir, name)
	st := Status{Name: name, Dir: dir}
	spec, ok := m.catalog[name]
	if !ok {
		return st
	}
	if data, err := os.ReadFile(filepath.Join(dir, metadataFile)); err == nil {
		var meta installedMeta
		if json.Unmarshal(data, &meta) == nil {
			st.Version = meta.Version
			st.Arch = meta.Arch
		}
	}
	for _, f := range spec.RequiredFiles {
		if !isRegularFile(filepath.Join(dir, f)) {
			st.Missing = append(st.Missing, f)
		}
	}
	st.Installed = len(spec.RequiredFiles) > 0 && len(st.Missing) == 0
	return st
}

// Ensure makes sure runtime name is installed under BaseDir, downloading and
// extracting the pinned official archive if needed. Returns the install dir.
func (m *Manager) Ensure(ctx context.Context, name Name, progress func(Progress)) (string, error) {
	spec, ok := m.catalog[name]
	if !ok {
		return "", fmt.Errorf("%s: %w", name, ErrUnknownRuntime)
	}
	if st := m.Status(name); st.Installed {
		return st.Dir, nil
	}
	asset, ok := spec.Assets[m.goarch]
	if !ok || asset.URL == "" {
		return "", fmt.Errorf("%s (%s): %w", name, m.goarch, ErrUnsupportedArch)
	}

	dir := RuntimeDir(m.baseDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("winruntime: mkdir %s: %w", dir, err)
	}

	tmpZip, actualSHA, err := m.downloadArchive(ctx, name, asset, dir, progress)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpZip)

	if progress != nil {
		progress(Progress{Runtime: name, Stage: "verifying"})
	}
	if asset.SHA256 != "" && !strings.EqualFold(actualSHA, strings.TrimSpace(asset.SHA256)) {
		return "", fmt.Errorf("%s: got sha256 %s, want %s: %w", name, actualSHA, asset.SHA256, ErrChecksumMismatch)
	}

	if progress != nil {
		progress(Progress{Runtime: name, Stage: "extracting"})
	}
	if err := extractZipAssets(tmpZip, dir, asset.Extract); err != nil {
		return "", err
	}

	meta := installedMeta{
		Name:        name,
		Version:     spec.Version,
		Arch:        m.goarch,
		SHA256:      actualSHA,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if data, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = writeAtomicFile(filepath.Join(dir, metadataFile), data)
	}
	return dir, nil
}

// Remove deletes the managed runtime directory for name.
func (m *Manager) Remove(name Name) error {
	return os.RemoveAll(RuntimeDir(m.baseDir, name))
}

func (m *Manager) downloadArchive(ctx context.Context, name Name, asset Asset, dir string, progress func(Progress)) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "AudioSmith (winruntime)")
	resp, err := m.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("winruntime: %s: HTTP %d", name, resp.StatusCode)
	}

	total := resp.ContentLength
	if total <= 0 {
		total = asset.Size
	}

	tmp, err := os.CreateTemp(dir, ".download-*.zip")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	h := sha256.New()
	var written int64
	buf := make([]byte, 64*1024)
	lastReport := time.Time{}

	for {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", "", err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				cleanup()
				return "", "", werr
			}
			_, _ = h.Write(buf[:n])
			written += int64(n)
			if progress != nil && (lastReport.IsZero() || time.Since(lastReport) >= 100*time.Millisecond) {
				progress(Progress{Runtime: name, Stage: "downloading", Bytes: written, TotalBytes: total})
				lastReport = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			cleanup()
			return "", "", rerr
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", "", err
	}
	if progress != nil {
		progress(Progress{Runtime: name, Stage: "downloading", Bytes: written, TotalBytes: max(total, written)})
	}
	return tmpPath, hex.EncodeToString(h.Sum(nil)), nil
}

func extractZipAssets(zipPath, destDir string, specs []ExtractSpec) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("winruntime: open zip: %w", err)
	}
	defer zr.Close()

	type zipEntry struct {
		norm string
		base string
		file *zip.File
	}
	entries := make([]zipEntry, 0, len(zr.File))
	byPath := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`) {
			continue
		}
		norm, base, ok := normalizeArchiveEntry(f.Name)
		if !ok {
			continue
		}
		entries = append(entries, zipEntry{norm: norm, base: base, file: f})
		byPath[norm] = f
	}

	cleanDestDir := filepath.Clean(destDir)
	for _, spec := range specs {
		if isGlobPattern(spec.ArchivePath) {
			pat, _, ok := normalizeArchiveEntry(spec.ArchivePath)
			if !ok {
				return fmt.Errorf("winruntime: unsafe extract pattern %q", spec.ArchivePath)
			}
			if _, err := path.Match(pat, ""); err != nil {
				return fmt.Errorf("winruntime: invalid extract pattern %q: %w", spec.ArchivePath, err)
			}
			matched := 0
			for _, e := range entries {
				ok, err := path.Match(pat, e.norm)
				if err != nil {
					return fmt.Errorf("winruntime: invalid extract pattern %q: %w", spec.ArchivePath, err)
				}
				if !ok {
					continue
				}
				if !isSafeFilename(e.base) {
					return fmt.Errorf("winruntime: unsafe archive filename %q", e.file.Name)
				}
				dst := filepath.Join(cleanDestDir, e.base)
				if filepath.Dir(dst) != cleanDestDir {
					return fmt.Errorf("winruntime: unsafe destination path %q", dst)
				}
				if err := extractSingleZipFile(e.file, dst); err != nil {
					return err
				}
				matched++
			}
			if matched == 0 && !spec.Optional {
				return fmt.Errorf("winruntime: archive missing %s", spec.ArchivePath)
			}
			continue
		}

		if !isSafeFilename(spec.DestName) {
			return fmt.Errorf("winruntime: unsafe destination filename %q", spec.DestName)
		}
		want, _, ok := normalizeArchiveEntry(spec.ArchivePath)
		if !ok {
			return fmt.Errorf("winruntime: unsafe archive path %q", spec.ArchivePath)
		}
		zf, ok := byPath[want]
		if !ok {
			if spec.Optional {
				continue
			}
			return fmt.Errorf("winruntime: archive missing %s", spec.ArchivePath)
		}
		dst := filepath.Join(cleanDestDir, spec.DestName)
		if filepath.Dir(dst) != cleanDestDir {
			return fmt.Errorf("winruntime: unsafe destination path %q", dst)
		}
		if err := extractSingleZipFile(zf, dst); err != nil {
			return err
		}
	}
	return nil
}

func isGlobPattern(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// normalizeArchiveEntry validates a ZIP entry or pattern against zip-slip
// (rejecting absolute paths, drive letters, and ".." segments) and returns its
// lowercased normalized slash path plus its original-case base filename.
func normalizeArchiveEntry(raw string) (norm, base string, ok bool) {
	s := strings.ReplaceAll(raw, `\`, "/")
	if s == "" || strings.HasPrefix(s, "/") || strings.Contains(s, ":") {
		return "", "", false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return "", "", false
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+s), "/")
	if cleaned == "" || cleaned == "." {
		return "", "", false
	}
	b := path.Base(cleaned)
	if !isSafeFilename(b) {
		return "", "", false
	}
	return strings.ToLower(cleaned), b, true
}

func extractSingleZipFile(zf *zip.File, destPath string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".extract-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return core.ReplaceFile(tmpPath, destPath)
}

func writeAtomicFile(destPath string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".meta-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return core.ReplaceFile(tmpPath, destPath)
}

func isSafeFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}
