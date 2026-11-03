package hub

// The Downloader (Client): its errors, New, and the Hugging Face API
// (repo info, raced over the endpoints).
//
// Files: client.go, client_plan.go (planning), client_fetch.go
// (downloading), client_files.go (finishing files, the snapshot).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

// Errors of the Downloader; services map them to core sentinels
// (ErrGated → ErrLicenceRequired-like access errors, ErrNotFound →
// core.ErrNotFound…).
var (
	ErrGated     = errors.New("the repo is gated: accept its terms on huggingface.co and add a token")
	ErrNotFound  = errors.New("repo or revision not found")
	ErrDiskSpace = errors.New("not enough free disk space")
	ErrChecksum  = errors.New("checksum mismatch")
	ErrOffline   = errors.New("no endpoint answered")
)

// DefaultEndpoints are raced when Options.Endpoints is empty and
// $HF_ENDPOINT unset: the official hub and its best-known mirror.
var DefaultEndpoints = []string{"https://huggingface.co", "https://hf-mirror.com"}

// winnerTTL is how long a race's winner is reused before racing again.
const winnerTTL = 24 * time.Hour

// Client implements Downloader over the Hugging Face HTTP API.
type Client struct {
	opts Options
	http *http.Client

	// progressEvery is the least time between progress reports.
	progressEvery time.Duration

	mu       sync.Mutex
	winner   string // endpoint that won the last race
	wonAt    time.Time
	repoLock map[string]*sync.Mutex
	// free is the space free on a directory's volume (freeBytes; tests
	// fake it); inflight are the bytes each download under way still has
	// to write, with its repo (client_reserve.go).
	free     func(dir string) int64
	inflight map[*atomic.Int64]string
}

var _ Downloader = (*Client)(nil)

// New returns a Client. Options left zero take their defaults.
func New(o Options) *Client {
	if o.CacheDir == "" {
		o.CacheDir = CacheDir()
	}
	env := strings.TrimRight(os.Getenv("HF_ENDPOINT"), "/")
	if o.Official == "" {
		o.Official = env
	}
	if o.Official == "" {
		o.Official = DefaultEndpoints[0]
	}
	o.Official = strings.TrimRight(o.Official, "/")
	if len(o.Endpoints) == 0 && env != "" {
		o.Endpoints = []string{env}
	}
	if len(o.Endpoints) == 0 {
		o.Endpoints = DefaultEndpoints
	}
	var eps []string
	for _, e := range o.Endpoints {
		if e = strings.TrimRight(strings.TrimSpace(e), "/"); e != "" && !slices.Contains(eps, e) {
			eps = append(eps, e)
		}
	}
	o.Endpoints = eps
	if o.Parallel <= 0 {
		o.Parallel = 4
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = 16 << 20
	}
	if o.ReserveBytes == 0 {
		o.ReserveBytes = DefaultReserve
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = NewHTTPClient(o.Proxy, 30*time.Second)
	}
	return &Client{opts: o, http: hc, repoLock: map[string]*sync.Mutex{}, progressEvery: 250 * time.Millisecond,
		free: freeBytes, inflight: map[*atomic.Int64]string{}}
}

// Options returns the options in use, defaults filled in.
func (c *Client) Options() Options { return c.opts }

func (c *Client) lockRepo(repo string) func() {
	c.mu.Lock()
	l := c.repoLock[repo]
	if l == nil {
		l = &sync.Mutex{}
		c.repoLock[repo] = l
	}
	c.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ---- the HF API -------------------------------------------------------------

// repoInfo is what /api/models/{repo}/revision/{rev}?blobs=true says.
type repoInfo struct {
	ID       string          `json:"id"`
	SHA      string          `json:"sha"`
	Gated    json.RawMessage `json:"gated"` // false, "auto" or "manual"
	Siblings []struct {
		Path   string `json:"rfilename"`
		BlobID string `json:"blobId"`
		Size   int64  `json:"size"`
		LFS    *struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

func (r *repoInfo) gated() bool {
	g := strings.TrimSpace(string(r.Gated))
	return g != "" && g != "false" && g != "null"
}

// apiError is a failed request.
type apiError struct {
	status int
	err    error
}

func (e *apiError) Error() string { return e.err.Error() }
func (e *apiError) Unwrap() error { return e.err }

func revisionOf(e catalog.Entry) string {
	if e.Revision == "" {
		return "main"
	}
	return e.Revision
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func isCommit(rev string) bool { return commitRE.MatchString(rev) }

func (c *Client) official(ep string) bool { return ep == c.opts.Official }

func (c *Client) newRequest(ctx context.Context, ep, rawURL, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AudioSmith (hub)")
	// Mirrors never see the token. Go drops it on redirects to other
	// hosts (the CDN).
	if token != "" && c.official(ep) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (c *Client) fetchInfo(ctx context.Context, ep, repo, rev, token string) (*repoInfo, error) {
	u := ep + "/api/models/" + repo + "/revision/" + url.PathEscape(rev) + "?blobs=true"
	req, err := c.newRequest(ctx, ep, u, token)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &apiError{resp.StatusCode, fmt.Errorf("%s: %w (HTTP %d)", repo, ErrGated, resp.StatusCode)}
	case resp.StatusCode == http.StatusNotFound:
		return nil, &apiError{resp.StatusCode, fmt.Errorf("%s@%s: %w", repo, rev, ErrNotFound)}
	case resp.StatusCode != http.StatusOK:
		return nil, &apiError{resp.StatusCode, fmt.Errorf("%s: HTTP %d from %s", repo, resp.StatusCode, ep)}
	}
	var info repoInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&info); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", repo, ep, err)
	}
	if info.SHA == "" {
		return nil, fmt.Errorf("%s: %s sent no commit", repo, ep)
	}
	return &info, nil
}

// info asks the endpoints for repo@rev, racing them unless a recent winner
// is known, and returns the answer and the endpoint that gave it. Gated
// repos are then asked of the official endpoint, with the token.
func (c *Client) info(ctx context.Context, repo, rev string) (*repoInfo, string, error) {
	token := ""
	if c.opts.Token != nil {
		token = strings.TrimSpace(c.opts.Token())
	}
	c.mu.Lock()
	winner := c.winner
	if time.Since(c.wonAt) > winnerTTL {
		winner = ""
	}
	c.mu.Unlock()

	var info *repoInfo
	var ep string
	var err error
	if winner != "" {
		info, err = c.fetchInfo(ctx, winner, repo, rev, token)
		ep = winner
		if err != nil && !isDefinite(err) {
			info, winner = nil, "" // the winner failed: race again
		}
	}
	if winner == "" {
		info, ep, err = c.race(ctx, repo, rev, token)
	}
	if err != nil {
		return nil, "", err
	}
	if info.gated() {
		if token == "" {
			return nil, "", fmt.Errorf("%s: %w", repo, ErrGated)
		}
		if !c.official(ep) {
			ep = c.opts.Official
			if info, err = c.fetchInfo(ctx, ep, repo, rev, token); err != nil {
				return nil, "", err
			}
		}
	}
	return info, ep, nil
}

// isDefinite reports an answer that another endpoint would repeat (the
// repo is missing or gated), as opposed to an endpoint being down.
func isDefinite(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && (ae.status == 401 || ae.status == 403 || ae.status == 404)
}

// race asks every endpoint at once and takes the first good answer.
func (c *Client) race(ctx context.Context, repo, rev, token string) (*repoInfo, string, error) {
	eps := c.opts.Endpoints
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		info *repoInfo
		ep   string
		err  error
	}
	ch := make(chan answer, len(eps))
	for _, ep := range eps {
		go func() {
			info, err := c.fetchInfo(ctx, ep, repo, rev, token)
			ch <- answer{info, ep, err}
		}()
	}
	var errs []error
	var definite error
	for range eps {
		a := <-ch
		if a.err == nil {
			c.mu.Lock()
			c.winner, c.wonAt = a.ep, time.Now()
			c.mu.Unlock()
			return a.info, a.ep, nil
		}
		if isDefinite(a.err) && definite == nil {
			definite = a.err
		}
		errs = append(errs, a.err)
	}
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if definite != nil {
		return nil, "", definite
	}
	return nil, "", fmt.Errorf("%s: %w: %w", repo, ErrOffline, errors.Join(errs...))
}
