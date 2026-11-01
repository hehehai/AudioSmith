package family

// "Move models" (spec §15.2 step 5): AudioSmith's models go from its own
// model folder to the family's, repo by repo, each under the repo's
// exclusive lock in both folders:
//
//   - the family's folder has the snapshot already (repo@commit
//     installed): nothing is copied, AudioSmith's copy goes;
//   - else on the same volume the repo directory is renamed (when the
//     target has none and nobody else uses the source), or its files are
//     hard-linked;
//   - else (another volume) each file is copied, checked (size and
//     SHA-256 against the source) and the source deleted.
//
// The source stays when another app uses it there (its claims in that
// folder). mlx-audio/ copies are not moved: the source's goes with its
// repo, a stale one in the target (links to nowhere) is removed; the
// engine makes them again. A cancelled move stops between files: what is
// moved stays in the target, the repo being moved stays in the source.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	actnow "github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
)

// OpMove names the move in lock owner files.
const OpMove = "move"

// MoveLockWait is how long the move waits for a repo another app holds
// (downloading or deleting it) before it skips it.
var MoveLockWait = 30 * time.Second

// Test hooks: a rename or link across volumes fails (EXDEV), and the
// move copies instead.
var (
	renameDir = os.Rename
	linkFile  = os.Link
)

// MoveProgress is reported as the move goes.
type MoveProgress struct {
	Repo              string // the repo being moved
	Done, Total       int    // repos
	Bytes, TotalBytes int64
	// WaitingFor names the app holding the repo's lock while the move
	// waits for it ("" when unknown or not waiting; Waiting tells).
	WaitingFor string
	Waiting    bool
}

// MoveSkip is a repo the move left where it was.
type MoveSkip struct {
	Ref models.Ref
	By  string // the app holding its lock, when that was why
	Err error
}

// MoveResult says what the move did.
type MoveResult struct {
	Moved  []models.Ref // in the target now (moved, or there already)
	Reused []models.Ref // of Moved, those the target had already
	Kept   []models.Ref // of Moved, those whose source stays: another app uses it there
	// Skipped repos stay in the source: their lock was held too long, or
	// they could not be moved.
	Skipped []MoveSkip
}

// ErrRepoBusy: another app held a repo's lock longer than MoveLockWait.
var ErrRepoBusy = errors.New("another app is using the model")

// MoveModels moves the snapshots of claims (AudioSmith's models in from,
// installed or not) to the folder to. It returns ctx's error when
// cancelled, with what was moved by then.
func (f *Family) MoveModels(ctx context.Context, from, to string, claims []models.Claim, progress func(MoveProgress)) (MoveResult, error) {
	var res MoveResult
	if from == "" || to == "" || filepath.Clean(from) == filepath.Clean(to) || models.SameHub(from, to) {
		return res, errors.New("the model folders are the same")
	}
	if rel, err := filepath.Rel(from, to); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return res, errors.New("the new model folder is inside the current one")
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return res, err
	}
	var todo []models.Claim
	var p MoveProgress
	for _, c := range claims {
		if slices.ContainsFunc(todo, func(x models.Claim) bool { return x.Ref() == c.Ref() }) {
			continue
		}
		st := models.Check(from, c)
		if st.State != models.Installed {
			continue
		}
		todo = append(todo, c)
		p.TotalBytes += st.Bytes
	}
	p.Total = len(todo)
	report := func() {
		if progress != nil {
			progress(p)
		}
	}
	report()
	for _, c := range todo {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		size := models.Check(from, c).Bytes
		before := p.Bytes
		p.Repo = c.Repo
		report()
		out, err := f.moveOne(ctx, from, to, c, func(waiting bool, by string) {
			p.Waiting, p.WaitingFor = waiting, by
			report()
		}, func(n int64) {
			p.Bytes = min(p.Bytes+n, before+size)
			report()
		})
		p.Waiting, p.WaitingFor = false, ""
		if err != nil && ctx.Err() != nil {
			return res, ctx.Err()
		}
		var busy *busyRepo
		switch {
		case errors.As(err, &busy):
			res.Skipped = append(res.Skipped, MoveSkip{Ref: c.Ref(), By: busy.by, Err: ErrRepoBusy})
			f.logf("family: move %s: skipped, held by %s", c.Ref(), busy.by)
		case err != nil:
			res.Skipped = append(res.Skipped, MoveSkip{Ref: c.Ref(), Err: err})
			f.logf("family: move %s: %v", c.Ref(), err)
		default:
			res.Moved = append(res.Moved, c.Ref())
			if out.reused {
				res.Reused = append(res.Reused, c.Ref())
			}
			if out.kept {
				res.Kept = append(res.Kept, c.Ref())
			}
		}
		p.Done++
		p.Bytes = before + size
		report()
	}
	return res, nil
}

type moveOutcome struct{ reused, kept bool }

type busyRepo struct{ by string }

func (b *busyRepo) Error() string { return "held by " + b.by }

// moveOne moves one snapshot holding both repo locks.
func (f *Family) moveOne(ctx context.Context, from, to string, c models.Claim, waiting func(bool, string), copied func(int64)) (moveOutcome, error) {
	var out moveOutcome
	ref := c.Ref()
	dst, err := f.lockRepo(ctx, to, c.Repo, waiting)
	if err != nil {
		return out, err
	}
	defer dst.Release()
	src, err := f.lockRepo(ctx, from, c.Repo, waiting)
	if err != nil {
		return out, err
	}
	defer src.Release()
	// Another app's claim on the source (in that folder) keeps it.
	d, err := f.Store(from).Decide(ref)
	keep := err != nil || len(d.Users) > 0 || len(d.LoadedBy) > 0 || d.Unsure
	out.kept = keep
	if models.Check(to, c).State == models.Installed {
		out.reused = true
	} else {
		if models.Check(from, c).State != models.Installed {
			return out, fmt.Errorf("%s is not complete in %s", ref, from)
		}
		renamed := false
		if !keep && soleSnapshot(from, ref) {
			if _, err := os.Lstat(models.RepoDir(to, c.Repo)); errors.Is(err, fs.ErrNotExist) {
				renamed = renameDir(models.RepoDir(from, c.Repo), models.RepoDir(to, c.Repo)) == nil
			}
		}
		if renamed {
			copied(models.Check(to, c).Bytes)
		} else if err := copySnapshot(ctx, from, to, c, f.BundleID, copied); err != nil {
			return out, err
		}
		if models.Check(to, c).State != models.Installed {
			if renamed {
				_ = renameDir(models.RepoDir(to, c.Repo), models.RepoDir(from, c.Repo))
			}
			return out, fmt.Errorf("%s is not complete after the move", ref)
		}
		if renamed {
			_ = os.RemoveAll(models.EngineCopy(from, c.Repo))
			removeStaleCopy(to, c.Repo)
			return out, nil
		}
	}
	if !keep {
		if _, err := models.DeleteSnapshot(from, ref); err != nil {
			return out, err
		}
	}
	removeStaleCopy(to, c.Repo)
	return out, nil
}

// lockRepo takes repo's lock in hub exclusively, waiting up to
// MoveLockWait when another app holds it.
func (f *Family) lockRepo(ctx context.Context, hub, repo string, waiting func(bool, string)) (*models.HeldLock, error) {
	who := models.Owner{BundleID: f.BundleID, Op: OpMove}
	l, err := models.TryLockRepo(hub, repo, actnow.Exclusive, who)
	if err == nil || !errors.Is(err, actnow.ErrLocked) {
		return l, err
	}
	by := f.holderName(err)
	waiting(true, by)
	wctx, cancel := context.WithTimeout(ctx, MoveLockWait)
	defer cancel()
	l, err = models.LockRepo(wctx, hub, repo, actnow.Exclusive, who)
	waiting(false, "")
	if err != nil && ctx.Err() == nil {
		return nil, &busyRepo{by: by}
	}
	return l, err
}

// holderName names the app a lock's owner file says holds it ("" when
// unknown).
func (f *Family) holderName(err error) string {
	var be *models.BusyError
	if !errors.As(err, &be) || be.Owner == nil || be.Owner.BundleID == "" {
		return ""
	}
	for _, a := range f.Found() {
		if a.BundleID == be.Owner.BundleID && a.Name != "" {
			return a.Name
		}
	}
	if app, _, err := actnow.ParseBundleID(be.Owner.BundleID); err == nil {
		return app.Name
	}
	return ""
}

// soleSnapshot reports whether ref is the only snapshot of its repo in
// hub (then the whole repo directory may be renamed).
func soleSnapshot(hub string, ref models.Ref) bool {
	ents, err := os.ReadDir(filepath.Join(models.RepoDir(hub, ref.Repo), "snapshots"))
	return err == nil && len(ents) == 1 && ents[0].Name() == ref.Revision
}

// copySnapshot makes ref's snapshot in to from the one in from: each
// blob linked (same volume) or copied and checked, the snapshot built as
// <commit>.incomplete and renamed when complete, the refs naming the
// commit added where to has none.
func copySnapshot(ctx context.Context, from, to string, c models.Claim, bundleID string, copied func(int64)) error {
	ref := c.Ref()
	srcRepo, dstRepo := models.RepoDir(from, c.Repo), models.RepoDir(to, c.Repo)
	srcSnap, final := models.SnapshotDir(from, ref), models.SnapshotDir(to, ref)
	tmp := final + ".incomplete"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dstRepo, "blobs"), 0o755); err != nil {
		return err
	}
	srcBlobs := filepath.Join(srcRepo, "blobs")
	err := filepath.WalkDir(srcSnap, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcSnap, p)
		target := filepath.Join(tmp, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			abs := link
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(filepath.Dir(p), link)
			}
			if r, err := filepath.Rel(srcBlobs, abs); err == nil && !strings.Contains(r, string(filepath.Separator)) && r != ".." && r != "." {
				blob := filepath.Join(dstRepo, "blobs", r)
				if err := transferBlob(ctx, to, c.Repo, abs, blob, bundleID, copied); err != nil {
					return err
				}
				rl, err := filepath.Rel(filepath.Dir(filepath.Join(final, rel)), blob)
				if err != nil {
					return err
				}
				return os.Symlink(rl, target)
			}
			return transferFile(ctx, abs, target, copied) // a link elsewhere: its content
		case d.Type().IsRegular():
			return transferFile(ctx, p, target, copied)
		}
		return nil
	})
	if err == nil {
		if err = os.RemoveAll(final); err == nil { // an unfinished one (we hold the lock)
			err = os.Rename(tmp, final)
		}
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	copyRefs(srcRepo, dstRepo, ref.Revision)
	return nil
}

// transferBlob puts the blob src at dst (a blob of repo in hub), unless
// dst has it already (blobs are named by their content).
func transferBlob(ctx context.Context, hub, repo, src, dst, bundleID string, copied func(int64)) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() {
		return nil
	}
	if name := filepath.Base(dst); blobName(name) {
		l, err := models.LockBlob(ctx, hub, repo, name, models.Owner{BundleID: bundleID, Op: OpMove})
		if err != nil {
			return err
		}
		defer l.Release()
	}
	return transferFile(ctx, src, dst, copied)
}

// blobName reports whether name can have a blob lock (hex, - or _).
func blobName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// transferFile links src at dst, or copies it there through dst
// ".incomplete" and checks the copy (size and SHA-256).
func transferFile(ctx context.Context, src, dst string, copied func(int64)) error {
	part := dst + ".incomplete"
	_ = os.Remove(part)
	if linkFile(src, part) == nil {
		if err := os.Rename(part, dst); err != nil {
			return err
		}
		if fi, err := os.Stat(dst); err == nil {
			copied(fi.Size())
		}
		return nil
	}
	if err := copyChecked(ctx, src, part, copied); err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, dst)
}

// copyChecked copies src to dst, then reads dst back: its size and
// SHA-256 must be the source's.
func copyChecked(ctx context.Context, src, dst string, copied func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(out, io.TeeReader(&ctxReader{ctx: ctx, r: in, n: copied}, h))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != fi.Size() {
		return fmt.Errorf("%s: copied %d of %d bytes", dst, n, fi.Size())
	}
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	back, err := os.Open(dst)
	if err != nil {
		return err
	}
	defer back.Close()
	h2 := sha256.New()
	if _, err := io.Copy(h2, &ctxReader{ctx: ctx, r: back}); err != nil {
		return err
	}
	if string(h.Sum(nil)) != string(h2.Sum(nil)) {
		return fmt.Errorf("%s: the copy differs from the original", dst)
	}
	return nil
}

// ctxReader stops at ctx's end and reports what it read every 8 MiB.
type ctxReader struct {
	ctx     context.Context
	r       io.Reader
	n       func(int64)
	pending int64
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if c.n != nil {
		c.pending += int64(n)
		if c.pending >= 8<<20 || err != nil {
			c.n(c.pending)
			c.pending = 0
		}
	}
	return n, err
}

// copyRefs adds the refs of srcRepo naming commit to dstRepo, where it
// has none of that name (the target's own refs win).
func copyRefs(srcRepo, dstRepo, commit string) {
	root := filepath.Join(srcRepo, "refs")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil || strings.TrimSpace(string(data)) != commit {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		target := filepath.Join(dstRepo, "refs", rel)
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if os.MkdirAll(filepath.Dir(target), 0o755) == nil {
			_ = os.WriteFile(target, data, 0o644)
		}
		return nil
	})
}

// removeStaleCopy removes hub's mlx-audio copy of repo when a link in it
// leads nowhere (spec §7.8: after $HUB moved, a stale copy may go; the
// engine makes it again).
func removeStaleCopy(hub, repo string) {
	dir := models.EngineCopy(hub, repo)
	stale := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if _, err := os.Stat(p); err != nil {
				stale = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if stale {
		_ = os.RemoveAll(dir)
	}
}
