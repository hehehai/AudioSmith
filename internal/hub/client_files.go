package hub

// Finishing downloaded files: recording finished chunks, checking a
// blob's hash, atomic writes and the snapshot's links into the blobs.

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
)

// taskDone records a finished task and finishes its file after the last.
func (c *Client) taskDone(ctx context.Context, t task) error {
	fs := t.file
	fs.mu.Lock()
	fs.left--
	last := fs.left == 0
	var err error
	if fs.parts != nil {
		fs.parts.Done[t.index] = true
		err = writeParts(fs.path+".parts", fs.parts)
	}
	fs.mu.Unlock()
	if err != nil {
		return err
	}
	if last {
		return c.finishFile(ctx, fs)
	}
	return nil
}

// finishFile checks a complete .incomplete blob and moves it in place.
func (c *Client) finishFile(ctx context.Context, fs *fileState) error {
	if err := fs.f.Sync(); err != nil {
		return err
	}
	// Close the handle before removing or renaming the .incomplete file:
	// Windows rejects Rename/Remove while any handle to the file is open.
	if err := fs.f.Close(); err != nil {
		return err
	}
	ok, err := checkBlob(ctx, fs.path, fs.remoteFile)
	if err != nil {
		return err
	}
	if !ok {
		os.Remove(fs.path)
		os.Remove(fs.path + ".parts")
		return fmt.Errorf("%s: %w", fs.Path, ErrChecksum)
	}
	if err := core.ReplaceFile(fs.path, strings.TrimSuffix(fs.path, ".incomplete")); err != nil {
		return err
	}
	os.Remove(fs.path + ".parts")
	// The blob is in place: another app waiting for it may go on.
	fs.lock.Release()
	return nil
}

// checkBlob hashes a file: sha256 for LFS files, the git blob id for others.
func checkBlob(ctx context.Context, path string, f remoteFile) (bool, error) {
	fh, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() != f.Size {
		return false, nil
	}
	var h hash.Hash
	want := f.blob
	if f.lfs || f.SHA256 != "" {
		h = sha256.New()
		want = f.SHA256
	} else {
		h = sha1.New()
		fmt.Fprintf(h, "blob %d\x00", f.Size)
	}
	if want == "" {
		return true, nil
	}
	if _, err := io.Copy(h, ctxReader{ctx, fh}); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == strings.ToLower(want), nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func writeParts(path string, p *partsFile) error {
	data, _ := json.Marshal(p)
	return writeAtomic(path, data)
}

// writeAtomic writes data to path through a temporary file and a rename.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return core.ReplaceFile(tmp.Name(), path)
}

// linkSnapshot makes snapshots/<commit>/<path> → ../../blobs/<blob> for
// every file. A new snapshot is built beside its place and renamed in, so
// it is never half there; one that exists (made by another tool) gets the
// links it lacks.
func linkSnapshot(repoDir, commit string, files []remoteFile) error {
	snaps := filepath.Join(repoDir, "snapshots")
	final := filepath.Join(snaps, commit)
	_, err := os.Stat(final)
	exists := err == nil
	dir := final
	if !exists {
		dir = final + ".incomplete"
		os.RemoveAll(dir)
	}
	for _, f := range files {
		link := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return err
		}
		depth := strings.Count(f.Path, "/") + 2 // up from the file's directory to repoDir
		target := strings.Repeat("../", depth) + "blobs/" + f.blob
		if cur, err := os.Readlink(link); err == nil && cur == target {
			continue
		}
		os.Remove(link)
		if err := core.SymlinkOrFallback(target, link); err != nil {
			return err
		}
	}
	if !exists {
		return os.Rename(dir, final)
	}
	return nil
}
