package hub

// Planning a download: the files of a repo at its revision (and of its
// auxiliary repos), what of them the cache already has, and the bytes
// still to fetch.

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

// remoteFile is a file to download and the blob it becomes.
type remoteFile struct {
	catalog.File
	blob string // blob name: the LFS sha256, else the git blob id
	lfs  bool
}

type plan struct {
	Plan
	repo   string
	rev    string // what was asked: "main", a branch or a commit
	commit string
	ep     string
	files  []remoteFile
	// For the claim once it is downloaded (Options.Claimed): the entry
	// downloaded, whether this is one of its auxiliary repos, and the
	// files it lists (nil: the whole snapshot).
	owner catalog.Entry
	aux   bool
	only  []string
}

// Plan lists the files of e at its revision and checks the disk. Files
// are e's own; the byte counts include its auxiliary repos.
func (c *Client) Plan(ctx context.Context, e catalog.Entry) (Plan, error) {
	_, p, err := c.planAll(ctx, e)
	if err != nil {
		return p, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return p, c.checkDiskLocked(e.Repo, p)
}

func (c *Client) plan(ctx context.Context, e catalog.Entry) (*plan, error) {
	rev := revisionOf(e)
	info, ep, err := c.info(ctx, e.Repo, rev)
	if err != nil {
		return nil, err
	}
	p := &plan{repo: e.Repo, rev: rev, commit: info.SHA, ep: ep}
	p.Revision, p.Endpoint, p.Gated = info.SHA, ep, info.gated()
	restrict := len(e.Files) > 0
	blobs := map[string]bool{} // files with the same content share one blob, counted once
	want := map[string]catalog.File{}
	for _, f := range e.Files {
		want[f.Path] = f
	}
	for _, s := range info.Siblings {
		f := remoteFile{File: catalog.File{Path: s.Path, Size: s.Size}, blob: s.BlobID}
		if s.LFS != nil {
			f.lfs, f.SHA256, f.blob = true, s.LFS.SHA256, s.LFS.SHA256
			f.Size = s.LFS.Size
		}
		if restrict {
			w, ok := want[s.Path]
			if !ok {
				continue
			}
			if w.SHA256 != "" && w.SHA256 != f.SHA256 {
				return nil, fmt.Errorf("%s/%s: the catalogue pins sha256 %s, %s lists %s: %w", e.Repo, s.Path, w.SHA256, ep, f.SHA256, ErrChecksum)
			}
			delete(want, s.Path)
		}
		if f.blob == "" || !safeRel(f.Path) {
			return nil, fmt.Errorf("%s: bad file entry %q", e.Repo, f.Path)
		}
		p.files = append(p.files, f)
		p.Files = append(p.Files, f.File)
		if !blobs[f.blob] {
			blobs[f.blob] = true
			p.TotalBytes += f.Size
			p.Bytes += f.Size - c.have(e.Repo, f)
		}
	}
	for missing := range want {
		return nil, fmt.Errorf("%s@%s has no %s: %w", e.Repo, rev, missing, ErrNotFound)
	}
	return p, nil
}

// auxEntry is an auxiliary repo as an entry of its own.
func auxEntry(e catalog.Entry, a catalog.Aux) catalog.Entry {
	return catalog.Entry{ID: e.ID + "+" + a.Repo, Repo: a.Repo, Revision: a.Revision, Files: a.Files}
}

// planAll plans e's auxiliary repos and e (last) and adds them up; the
// disk is checked by the caller (checkDisk, reserve).
func (c *Client) planAll(ctx context.Context, e catalog.Entry) ([]*plan, Plan, error) {
	var plans []*plan
	var sum Plan
	for _, a := range e.Aux {
		p, err := c.plan(ctx, auxEntry(e, a))
		if err != nil {
			return nil, Plan{}, err
		}
		p.owner, p.aux, p.only = e, true, filePaths(a.Files)
		plans = append(plans, p)
		sum.TotalBytes += p.TotalBytes
		sum.Bytes += p.Bytes
	}
	p, err := c.plan(ctx, e)
	if err != nil {
		return nil, Plan{}, err
	}
	p.owner, p.only = e, filePaths(e.Files)
	plans = append(plans, p)
	sum.Revision, sum.Files, sum.Endpoint, sum.Gated = p.Revision, p.Files, p.Endpoint, p.Gated
	sum.TotalBytes += p.TotalBytes
	sum.Bytes += p.Bytes
	sum.FreeBytes = c.free(c.opts.CacheDir)
	return plans, sum, nil
}

// filePaths are the paths of files; nil for none.
func filePaths(files []catalog.File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// safeRel reports whether a repo path stays inside the snapshot.
func safeRel(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, `\`) && path.Clean(p) == p && !strings.HasPrefix(p, "../") && p != ".."
}

// have is how many bytes of f are on disk already.
func (c *Client) have(repo string, f remoteFile) int64 {
	blobs := filepath.Join(RepoDir(c.opts.CacheDir, repo), "blobs")
	if st, err := os.Stat(filepath.Join(blobs, f.blob)); err == nil && st.Size() == f.Size {
		return f.Size
	}
	done, _ := partialDone(filepath.Join(blobs, f.blob), f.Size, c.chunks(f.Size))
	return done
}
