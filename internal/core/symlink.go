package core

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Hooks swapped in unit tests to exercise fallback tiers on any OS.
var (
	trySymlink  = os.Symlink
	tryHardlink = os.Link
	tryDirLink  = createDirLink
)

// SymlinkOrFallback creates a symlink at link pointing to target (just like
// os.Symlink). When symlink creation fails (e.g. non-admin Windows without
// Developer Mode), it falls back cleanly:
//   - For files: hardlink on the same volume, else atomic file copy.
//   - For directories: NTFS junction on Windows, else recursive directory copy.
func SymlinkOrFallback(target, link string) error {
	symErr := trySymlink(target, link)
	if symErr == nil {
		return nil
	}
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(link), target)
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return symErr
	}
	if st.IsDir() {
		if err := tryDirLink(resolved, link); err == nil {
			return nil
		}
		return copyDir(resolved, link)
	}
	if err := tryHardlink(resolved, link); err == nil {
		return nil
	}
	return copyFileAtomic(resolved, link)
}

func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(st.Mode().Perm()); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := ReplaceFile(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return SymlinkOrFallback(linkTarget, out)
		}
		return copyFileAtomic(path, out)
	})
}
