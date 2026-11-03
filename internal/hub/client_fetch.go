package hub

// Downloading: chunked, resumable range requests over the planned files,
// with retries and throttled progress.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/actnowkit/models"
)

// chunks is how many ranges a file of size downloads in: 1 under two chunks.
func (c *Client) chunks(size int64) int {
	if size < 2*c.opts.ChunkSize {
		return 1
	}
	return int((size + c.opts.ChunkSize - 1) / c.opts.ChunkSize)
}

// partsFile records which chunks of a .incomplete blob are written.
type partsFile struct {
	Size  int64  `json:"size"`
	Chunk int64  `json:"chunk"`
	Done  []bool `json:"done"`
}

// partialDone reads what of blob's .incomplete is done: from its parts
// file when chunked, else the length of the file (written in order, by us
// or by huggingface_hub, which uses the same name).
func partialDone(blob string, size int64, chunks int) (int64, *partsFile) {
	var pf partsFile
	if data, err := os.ReadFile(blob + ".incomplete.parts"); err == nil && json.Unmarshal(data, &pf) == nil && pf.Size == size && len(pf.Done) == chunks && chunks > 1 {
		var done int64
		for i, d := range pf.Done {
			if d {
				done += min(pf.Chunk, size-int64(i)*pf.Chunk)
			}
		}
		return done, &pf
	}
	if st, err := os.Stat(blob + ".incomplete"); err == nil && st.Size() <= size {
		return st.Size(), nil
	}
	return 0, nil
}

// task is one request: a whole file, or one range of a big one.
type task struct {
	file  *fileState
	index int // chunk index
	from  int64
	to    int64 // exclusive
}

type fileState struct {
	remoteFile
	path  string // blobs/<blob>.incomplete
	f     *os.File
	mu    sync.Mutex
	parts *partsFile       // nil: sequential
	left  int              // tasks not done
	lock  *models.HeldLock // the blob's family lock, released when it is in place
}

// Download fetches what Plan lists into the cache: the auxiliary repos
// first, then e's.
func (c *Client) Download(ctx context.Context, e catalog.Entry, progress func(Progress)) error {
	defer c.lockRepo(e.Repo)()
	plans, sum, err := c.planAll(ctx, e)
	if err != nil {
		return err
	}
	left, err := c.reserve(e.Repo, sum)
	if err != nil {
		return err
	}
	defer c.release(left)
	var base int64
	for _, p := range plans {
		unlock := func() {}
		if p.repo != e.Repo {
			unlock = c.lockRepo(p.repo)
		}
		err := c.fetchPlan(ctx, p, func(pr Progress) {
			left.Store(min(sum.Bytes, max(sum.TotalBytes-(base+pr.Bytes), 0)))
			if progress != nil {
				pr.Repo, pr.Bytes, pr.TotalBytes = e.Repo, base+pr.Bytes, sum.TotalBytes
				progress(pr)
			}
		})
		unlock()
		if err != nil {
			return err
		}
		base += p.TotalBytes
	}
	return nil
}

// fetchFiles downloads files (blobs missing from the cache, whose locks
// r holds) into r's repo: chunked, resuming what is there, finishing
// each file (and releasing its lock) as its last task ends.
func (r *fetchRun) fetchFiles(ctx context.Context, files []remoteFile) error {
	c, p := r.c, r.p
	var tasks []task
	var states []*fileState
	for _, f := range files {
		final := filepath.Join(r.blobs, f.blob)
		fs := &fileState{remoteFile: f, path: final + ".incomplete", lock: r.locks[f.blob]}
		n := c.chunks(f.Size)
		have, parts := partialDone(final, f.Size, n)
		r.done.Add(have)
		if n > 1 {
			if parts == nil {
				// Start a parts file: chunks inside what a sequential
				// writer left are done.
				parts = &partsFile{Size: f.Size, Chunk: c.opts.ChunkSize, Done: make([]bool, n)}
				for i := range n {
					parts.Done[i] = int64(i+1)*c.opts.ChunkSize <= have
				}
				if err := writeParts(fs.path+".parts", parts); err != nil {
					return err
				}
			}
			fs.parts = parts
			for i := range n {
				if !parts.Done[i] {
					from := int64(i) * c.opts.ChunkSize
					tasks = append(tasks, task{file: fs, index: i, from: from, to: min(from+c.opts.ChunkSize, f.Size)})
				}
			}
		} else {
			tasks = append(tasks, task{file: fs, from: have, to: f.Size})
		}
		fh, err := os.OpenFile(fs.path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return err
		}
		fs.f = fh
		defer fh.Close()
		states = append(states, fs)
	}
	for i := range tasks {
		tasks[i].file.left++
	}
	// Files with nothing left to fetch (all chunks were done) finish now.
	for _, fs := range states {
		if fs.left == 0 {
			if err := c.finishFile(ctx, fs); err != nil {
				return err
			}
		}
	}

	// Workers.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan task)
	var firstErr error
	var errOnce sync.Once
	fail := func(err error) {
		errOnce.Do(func() { firstErr = err; cancel() })
	}
	var wg sync.WaitGroup
	for range c.opts.Parallel {
		wg.Go(func() {
			for t := range queue {
				r.current.Store(t.file.Path)
				err := c.fetch(ctx, p, t, func(n int64) { r.done.Add(n); r.report(false) })
				if err != nil {
					fail(fmt.Errorf("%s/%s: %w", p.repo, t.file.Path, err))
					continue
				}
				if err := c.taskDone(ctx, t); err != nil {
					fail(err)
				}
			}
		})
	}
	for _, t := range tasks {
		if ctx.Err() != nil {
			break
		}
		select {
		case queue <- t:
		case <-ctx.Done():
		}
	}
	close(queue)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// writeRef writes the ref of a finished snapshot: refs/<branch> for a
// branch; refs/main for a pinned commit unless main names another
// snapshot that is there.
func writeRef(repoDir string, p *plan) error {
	ref := p.rev
	if isCommit(ref) {
		// A pinned commit is found by its path; tools that resolve the
		// repo by id (the engine for auxiliary repos and for families it
		// cannot load from a directory, the Python tools) read refs/main.
		// Point it here unless it names another snapshot that is there.
		ref = "main"
		if old, err := os.ReadFile(filepath.Join(repoDir, "refs", "main")); err == nil {
			if commit := strings.TrimSpace(string(old)); commit != p.commit && isDir(filepath.Join(repoDir, "snapshots", commit)) {
				ref = ""
			}
		}
	}
	if ref == "" {
		return nil
	}
	return writeAtomic(filepath.Join(repoDir, "refs", filepath.FromSlash(ref)), []byte(p.commit))
}

// throttle calls fn at most once every d, and always when final.
func throttle(d time.Duration, fn func(final bool)) func(final bool) {
	var mu sync.Mutex
	var last time.Time
	return func(final bool) {
		mu.Lock()
		defer mu.Unlock()
		if !final && time.Since(last) < d {
			return
		}
		last = time.Now()
		fn(final)
	}
}

// fetch downloads one task, retrying transient failures from where it got.
func (c *Client) fetch(ctx context.Context, p *plan, t task, add func(int64)) error {
	u := p.ep + "/" + p.repo + "/resolve/" + p.commit + "/" + escapePath(t.file.Path)
	token := ""
	if c.opts.Token != nil {
		token = strings.TrimSpace(c.opts.Token())
	}
	from := t.from
	var err error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt*attempt) * 500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if from >= t.to && t.to > 0 {
			return nil
		}
		var n int64
		n, err = c.fetchRange(ctx, p.ep, u, token, t, from, add)
		from += n
		if err == nil || ctx.Err() != nil || !retryable(err) {
			break
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

func retryable(err error) bool {
	var pe permanent
	return !errors.As(err, &pe)
}

// fetchRange writes bytes [from, t.to) of the file at their offsets and
// returns how many it wrote.
func (c *Client) fetchRange(ctx context.Context, ep, u, token string, t task, from int64, add func(int64)) (int64, error) {
	req, err := c.newRequest(ctx, ep, u, token)
	if err != nil {
		return 0, permanent{err}
	}
	ranged := from > 0 || t.to < t.file.Size
	if ranged {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, t.to-1))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK:
		if from > 0 || t.to < t.file.Size {
			if t.file.parts != nil {
				return 0, permanent{fmt.Errorf("%s does not serve ranges", ep)}
			}
			// The server ignored the range: start the file again.
			add(-from)
			from = 0
			if err := t.file.f.Truncate(0); err != nil {
				return 0, permanent{err}
			}
		}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return 0, permanent{fmt.Errorf("%w (HTTP %d)", ErrGated, resp.StatusCode)}
	case resp.StatusCode == http.StatusNotFound:
		return 0, permanent{ErrNotFound}
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		return 0, permanent{fmt.Errorf("HTTP 416 for bytes %d-%d", from, t.to-1)}
	default:
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 256<<10)
	off := from
	for off < t.to {
		n, rerr := resp.Body.Read(buf[:min(int64(len(buf)), t.to-off)])
		if n > 0 {
			if _, err := t.file.f.WriteAt(buf[:n], off); err != nil {
				return off - from, permanent{err}
			}
			off += int64(n)
			add(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return off - from, rerr
		}
	}
	if off < t.to {
		return off - from, io.ErrUnexpectedEOF
	}
	return off - from, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
