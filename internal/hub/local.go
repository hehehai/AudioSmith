package hub

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/models"
)

// commitOf is the snapshot e uses on disk: its pinned commit, else what
// its ref (refs/main) points at; "" when unknown.
func commitOf(repoDir string, e catalog.Entry) string {
	rev := revisionOf(e)
	if isCommit(rev) {
		return rev
	}
	data, err := os.ReadFile(filepath.Join(repoDir, "refs", filepath.FromSlash(rev)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Status reports what of e is on disk, from the files alone (no network).
func (c *Client) Status(ctx context.Context, e catalog.Entry) (Local, error) {
	return StatusIn(append([]string{c.opts.CacheDir}, c.opts.AlsoIn...), e)
}

// Status is Client.Status for a cache directory. An entry whose
// auxiliary repos are not all there is Partial, and its Missing lists
// them as "<repo>:".
func Status(cacheDir string, e catalog.Entry) (Local, error) {
	return StatusIn([]string{cacheDir}, e)
}

// StatusIn is Status across folders, the first one first: a move of the
// models that did not finish leaves each repo in one or the other
// (#101 M2). Each repo counts from the first folder that has it
// downloaded, else from the first folder.
func StatusIn(dirs []string, e catalog.Entry) (Local, error) {
	one := func(e catalog.Entry, weights bool) (Local, error) {
		var first Local
		for i, d := range dirs {
			l, err := statusOne(d, e, weights)
			if err != nil {
				return l, err
			}
			if l.State == Downloaded {
				return l, nil
			}
			if i == 0 {
				first = l
			}
		}
		return first, nil
	}
	l, err := one(e, true)
	if err != nil {
		return l, err
	}
	for _, a := range e.Aux {
		al, err := one(auxEntry(e, a), false)
		if err != nil {
			return l, err
		}
		l.Bytes += al.Bytes
		if al.State != Downloaded {
			l.Missing = append(l.Missing, a.Repo+":")
			if l.State == Downloaded || al.State == Partial {
				l.State = Partial
			}
		}
	}
	return l, nil
}

// AuxSnapshots are the snapshot directories of e's auxiliary repos in
// cacheDir at their pinned commits (repo id → directory), those that are
// there: the engine loads them from these (proto.ParamAuxPaths).
func AuxSnapshots(cacheDir string, e catalog.Entry) map[string]string {
	return AuxSnapshotsIn([]string{cacheDir}, e)
}

// AuxSnapshotsIn is AuxSnapshots across folders (StatusIn), the first one
// first.
func AuxSnapshotsIn(dirs []string, e catalog.Entry) map[string]string {
	out := map[string]string{}
	for _, a := range e.Aux {
		for _, d := range dirs {
			rev := commitOf(RepoDir(d, a.Repo), auxEntry(e, a))
			if hasSnapshot(d, a.Repo, rev) {
				out[a.Repo] = filepath.Join(RepoDir(d, a.Repo), "snapshots", rev)
				break
			}
		}
	}
	return out
}

// statusOne is the status of one repo; weights asks for a weights file
// when e lists no files. It is the family's "installed" check
// (actnowkit models.Check, spec §7.5): the snapshot is there, every link
// reaches its blob at the size it was verified with (models--*/.actnow/
// blobs.json, written when a download finishes), the listed files are
// there, a main repo has a weights file. Partial
// downloads in blobs/ (another commit's, a tool's) do not make an
// installed snapshot partial (family audit C4).
func statusOne(cacheDir string, e catalog.Entry, weights bool) (Local, error) {
	var l Local
	commit := commitOf(RepoDir(cacheDir, e.Repo), e)
	if !models.ValidRepo(e.Repo) {
		l.State = Missing
		return l, nil
	}
	if commit == "" {
		// A branch never resolved here: something in the repo's folder is
		// a download under way.
		st := models.Check(cacheDir, models.Claim{Repo: e.Repo, Revision: strings.Repeat("0", 40), Aux: !weights})
		l.State, l.Bytes = Missing, st.Incomplete
		if st.State != models.Missing {
			l.State = Partial
		}
		return l, nil
	}
	c := models.Claim{Repo: e.Repo, Revision: commit, Aux: !weights}
	for _, f := range e.Files {
		c.Files = append(c.Files, f.Path)
	}
	st := models.Check(cacheDir, c)
	l.Snapshot, l.Bytes = st.Snapshot, st.Bytes
	l.Missing = append(st.Missing, st.Damaged...) // a truncated file is fetched again
	switch st.State {
	case models.Installed:
		l.State = Downloaded
	case models.Partial:
		l.State = Partial
		l.Bytes += st.Incomplete
	default:
		l.State = Missing
	}
	return l, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Verify hashes every file of e's snapshot against the HF API's listing
// (or e.Files when they carry hashes) and returns those missing or bad;
// bad blobs are removed, so that Download fetches them again. Files of
// auxiliary repos are named "<repo>:<path>".
func (c *Client) Verify(ctx context.Context, e catalog.Entry) ([]string, error) {
	defer c.lockRepo(e.Repo)()
	bad, err := c.verifyOne(ctx, e)
	if err != nil {
		return nil, err
	}
	for _, a := range e.Aux {
		if commitOf(RepoDir(c.opts.CacheDir, a.Repo), auxEntry(e, a)) == "" {
			bad = append(bad, a.Repo+":")
			continue
		}
		unlock := c.lockRepo(a.Repo)
		ab, err := c.verifyOne(ctx, auxEntry(e, a))
		unlock()
		if err != nil {
			return nil, err
		}
		for _, p := range ab {
			bad = append(bad, a.Repo+":"+p)
		}
	}
	return bad, nil
}

// verifyOne verifies one repo holding its family lock shared (spec §7.4:
// no app deletes it meanwhile); a bad blob is removed only under its blob
// lock (one being downloaded or repaired is left alone), and the family is
// told (TopicModels) so that the other apps see the model is incomplete
// (family audit C5).
func (c *Client) verifyOne(ctx context.Context, e catalog.Entry) ([]string, error) {
	repoDir := RepoDir(c.opts.CacheDir, e.Repo)
	held, err := models.LockRepo(ctx, c.opts.CacheDir, e.Repo, family.Shared, c.opts.Family.owner(models.OpVerify))
	if err != nil && !noLocks(err) {
		return nil, err
	}
	defer held.Release()
	removed := false
	defer func() {
		if removed && c.opts.Family.Changed != nil {
			c.opts.Family.Changed()
		}
	}()
	commit := commitOf(repoDir, e)
	if commit == "" {
		return nil, fmt.Errorf("%s is not downloaded", e.Repo)
	}
	var files []remoteFile
	if hashed := len(e.Files) > 0 && !slices.ContainsFunc(e.Files, func(f catalog.File) bool { return f.SHA256 == "" }); hashed {
		for _, f := range e.Files {
			files = append(files, remoteFile{File: f, blob: f.SHA256, lfs: true})
		}
	} else {
		pinned := e
		pinned.Revision = commit
		p, err := c.plan(ctx, pinned)
		if p == nil {
			return nil, err
		}
		files = p.files
	}
	var bad []string
	snap := filepath.Join(repoDir, "snapshots", commit)
	for _, f := range files {
		link := filepath.Join(snap, filepath.FromSlash(f.Path))
		blob, err := filepath.EvalSymlinks(link)
		if err != nil {
			bad = append(bad, f.Path)
			continue
		}
		ok, err := checkBlob(ctx, blob, f)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			bad = append(bad, f.Path)
			continue
		}
		if !ok {
			bad = append(bad, f.Path)
			name := filepath.Base(blob)
			if real, err := filepath.EvalSymlinks(filepath.Join(repoDir, "blobs")); err != nil || filepath.Dir(blob) != real {
				continue // only a blob of this repo's blobs/
			}
			bl, err := models.TryLockBlob(c.opts.CacheDir, e.Repo, name, c.opts.Family.owner(models.OpVerify))
			if err != nil && !noLocks(err) {
				continue // being downloaded or repaired right now
			}
			if os.Remove(blob) == nil {
				removed = true
			}
			bl.Release()
		}
	}
	return bad, nil
}

// EngineCopiesDir is where mlx-audio-swift keeps its own copies of the
// snapshots it loads: <cache>/mlx-audio/<owner>_<name>/.
func EngineCopiesDir(cacheDir string) string { return filepath.Join(cacheDir, "mlx-audio") }

// EngineCopy is mlx-audio-swift's copy of one repo, made when the engine
// resolves the repo by id (auxiliary repos, families it cannot load from a
// directory).
func EngineCopy(cacheDir, repo string) string {
	return filepath.Join(EngineCopiesDir(cacheDir), strings.ReplaceAll(repo, "/", "_"))
}

// CleanEngineCopies removes the empty directories mlx-audio-swift leaves
// in EngineCopiesDir after a failed load. Left there, the next load of
// that repo finds an "incomplete" model and deletes the repo's snapshot
// from the hub cache as well (research/m0-spikes.md §2). The supervisor
// calls it before starting an engine. Only directories untouched for
// models.EngineCopyMinAge (10 minutes) go: a younger one may be another
// family app's engine at work (spec §7.8). It returns what it removed.
func CleanEngineCopies(cacheDir string) ([]string, error) {
	return models.CleanEngineCopies(cacheDir, models.EngineCopyMinAge)
}

// repoID is an "owner/name" Hugging Face repo id.
var repoID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// leftoverDirs are the directories of repo in cacheDir: exactly its
// cache directory and its engine copy, nothing else.
func leftoverDirs(cacheDir, repo string) ([]string, error) {
	if !repoID.MatchString(repo) || strings.Contains(repo, "..") {
		return nil, fmt.Errorf("leftovers: bad repo id %q", repo)
	}
	return []string{RepoDir(cacheDir, repo), EngineCopy(cacheDir, repo)}, nil
}

// dirBytes is the size of the regular files under dir (links not
// followed); 0 when it is not there.
func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, de fs.DirEntry, err error) error {
		if err == nil && de.Type().IsRegular() {
			if info, err := de.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
