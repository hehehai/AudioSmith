package hub

// Deleting models (spec §7.6): Models → Delete (Remove), and the clean-up
// of repos AudioSmith no longer uses (Leftovers, RemoveLeftovers), both
// decided by the family's claims (Family.Decider) when the app passes
// them, snapshot by snapshot under the repo's lock.

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/models"
)

// Removal is what Remove did.
type Removal struct {
	// Decision is the main repo's, as decided when deleting (who else
	// uses it); zero without a Decider.
	Decision models.Decision
	// Deleted: the main repo's files went. Otherwise other apps use
	// them (Kept names them, Decision.Users says which builds) and the
	// model is only removed from AudioSmith.
	Deleted bool
	Kept    []string
	// Tombstoned: deleted for all apps over other apps' claims.
	Tombstoned bool
	Freed      int64
}

// Remove deletes e from AudioSmith (scope ThisApp) or for all apps
// (AllApps), deciding by the claims (Options.Family.Decider): its
// snapshot goes unless another app uses it; then its auxiliary repos,
// each kept while another app or another of AudioSmith's models uses it.
// A repo another process downloads (its lock is held) is not touched:
// *models.BusyError (see Holder). Without a Decider, e's snapshot goes
// and its auxiliary repos stay.
func (c *Client) Remove(ctx context.Context, e catalog.Entry, scope models.Scope) (Removal, error) {
	re, err := ResolveEntry(e, c.targetOS())
	if err != nil {
		return Removal{}, err
	}
	defer c.lockRepo(re.Repo)()
	return RemoveFor(ctx, c.opts.CacheDir, c.targetOS(), e, c.opts.Family, scope)
}

// Delete is Remove from AudioSmith, without its report.
func (c *Client) Delete(ctx context.Context, e catalog.Entry) error {
	_, err := c.Remove(ctx, e, models.ThisApp)
	return err
}

// Decide says who else uses e's snapshot (its main repo), for the delete
// dialog; ok is false without a Decider or a snapshot.
func (c *Client) Decide(e catalog.Entry) (d models.Decision, ok bool, err error) {
	re, rerr := ResolveEntry(e, c.targetOS())
	if rerr != nil {
		return models.Decision{}, false, nil
	}
	cache, dec := c.opts.CacheDir, c.opts.Family.Decider
	commit := commitOf(RepoDir(cache, re.Repo), re)
	if dec == nil || !hasSnapshot(cache, re.Repo, commit) {
		return models.Decision{}, false, nil
	}
	d, err = dec.Decide(models.Ref{Repo: re.Repo, Revision: commit})
	return d, err == nil, err
}

func hasSnapshot(cacheDir, repo, commit string) bool {
	return commit != "" && isDir(filepath.Join(RepoDir(cacheDir, repo), "snapshots", commit))
}

// Remove is Client.Remove for a cache directory.
func Remove(ctx context.Context, cacheDir string, e catalog.Entry, fam Family, scope models.Scope) (Removal, error) {
	return RemoveFor(ctx, cacheDir, "", e, fam, scope)
}

// RemoveFor is Remove for a specific target OS ("windows", "darwin", …).
func RemoveFor(ctx context.Context, cacheDir, goos string, e catalog.Entry, fam Family, scope models.Scope) (Removal, error) {
	re, err := ResolveEntry(e, goos)
	if err != nil {
		return Removal{}, err
	}
	commit := commitOf(RepoDir(cacheDir, re.Repo), re)
	d := fam.Decider
	if d == nil || !hasSnapshot(cacheDir, re.Repo, commit) {
		if commit == "" {
			commit = noSnapshot // partial downloads only
		}
		freed, err := deleteRepo(cacheDir, re.Repo, commit, fam)
		return Removal{Freed: freed, Deleted: err == nil}, err
	}
	res, err := d.DeleteModel(ctx, models.Ref{Repo: re.Repo, Revision: commit}, e, scope)
	out := Removal{Decision: res.Decision, Deleted: res.Deleted, Tombstoned: res.Tombstoned, Freed: res.Freed}
	if err != nil {
		return out, err
	}
	if !res.Deleted {
		out.Kept = res.Decision.UserNames()
	} else if err := dropOrphanCopy(cacheDir, re.Repo); err != nil {
		return out, err
	}
	// The auxiliary repos: AudioSmith no longer needs them for e, kept
	// files or not. One another process holds stays.
	for _, a := range re.Aux {
		rev := commitOf(RepoDir(cacheDir, a.Repo), auxEntry(re, a))
		if !hasSnapshot(cacheDir, a.Repo, rev) {
			continue
		}
		res, err := d.DeleteModel(ctx, models.Ref{Repo: a.Repo, Revision: rev}, e, scope)
		out.Freed += res.Freed
		if _, busy := Holder(err); err != nil && !busy {
			return out, err
		}
	}
	return out, nil
}

// Leftover is what a repo AudioSmith no longer uses (a removed model's,
// catalog.Removed) leaves in the model folder: its cache directory and
// the engine's copy, and their size.
type Leftover struct {
	Repo  string
	Dirs  []string // existing directories, absolute
	Bytes int64    // regular files under them (blobs, copies; links not followed)
	// InUse: another app uses one of its snapshots (the claims say so,
	// or cannot be read): cleaning up keeps it. Users are those apps,
	// UsedBy their names.
	InUse  bool
	Users  []models.User
	UsedBy []string
	// Busy: another process held the repo's lock when cleaning up (it
	// downloads or deletes the repo), so it stayed; BusyBy names the app
	// when known.
	Busy   bool
	BusyBy string
}

// Kept reports whether cleaning up leaves l alone.
func (l Leftover) Kept() bool { return l.InUse || l.Busy }

// addUsers records who uses one of l's snapshots.
func (l *Leftover) addUsers(d models.Decision) {
	l.InUse = true
	for _, u := range append(append([]models.User{}, d.Users...), d.LoadedBy...) {
		if !slices.ContainsFunc(l.Users, func(x models.User) bool { return x.BundleID == u.BundleID }) {
			l.Users = append(l.Users, u)
		}
	}
	for _, n := range d.UserNames() {
		if !slices.Contains(l.UsedBy, n) {
			l.UsedBy = append(l.UsedBy, n)
		}
	}
}

// Leftovers reports what of repos is on disk in cacheDir, and which of
// them other family apps use (fam.Decider: their claims); repos with
// nothing there are left out.
func Leftovers(cacheDir string, repos []string, fam Family) ([]Leftover, error) {
	var out []Leftover
	for _, repo := range repos {
		dirs, err := leftoverDirs(cacheDir, repo)
		if err != nil {
			return out, err
		}
		l := Leftover{Repo: repo}
		for _, d := range dirs {
			if st, err := os.Lstat(d); err != nil || !st.IsDir() {
				continue
			}
			l.Dirs = append(l.Dirs, d)
			l.Bytes += dirBytes(d)
		}
		if len(l.Dirs) == 0 {
			continue
		}
		if fam.Decider != nil {
			for _, rev := range snapshotsOf(cacheDir, repo) {
				if d, err := fam.Decider.Decide(models.Ref{Repo: repo, Revision: rev}); err == nil && d.InUse() {
					l.addUsers(d)
				}
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// RemoveLeftovers deletes repos from cacheDir (see Leftovers) from
// AudioSmith: each snapshot as the claims decide (fam.Decider; models
// nobody claims, such as old downloads, go), then what is left of a repo
// no other app uses (unfinished snapshots, blobs, the engine copy). Never
// a whole directory another process may be writing: each repo is deleted
// holding its lock, and one whose lock is held stays. It returns the
// bytes freed and the leftovers it kept.
func RemoveLeftovers(ctx context.Context, cacheDir string, repos []string, fam Family) (int64, []Leftover, error) {
	ls, err := Leftovers(cacheDir, repos, fam)
	if err != nil {
		return 0, nil, err
	}
	var freed int64
	var kept []Leftover
	for _, l := range ls {
		l.InUse, l.Users, l.UsedBy = false, nil, nil // decided again, under the lock
		if fam.Decider != nil {
			for _, rev := range snapshotsOf(cacheDir, l.Repo) {
				res, err := fam.Decider.Delete(ctx, models.Ref{Repo: l.Repo, Revision: rev}, models.ThisApp)
				freed += res.Freed
				if name, busy := Holder(err); busy {
					l.Busy, l.BusyBy = true, name
					break
				}
				if err != nil {
					return freed, kept, err
				}
				if !res.Deleted {
					l.addUsers(res.Decision)
				}
			}
			if l.Kept() {
				kept = append(kept, l)
				continue
			}
		}
		n, err := deleteRepo(cacheDir, l.Repo, "", fam)
		freed += n
		if name, busy := Holder(err); busy {
			l.Busy, l.BusyBy = true, name
			kept = append(kept, l)
			continue
		}
		if err != nil {
			return freed, kept, err
		}
	}
	return freed, kept, nil
}
