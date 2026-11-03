package hub

// The disk check of a download (code review 1102 M4/L7): a download must
// leave ReserveBytes free once it and the other downloads under way (the
// net lane runs several) have written what they still lack. A download
// reserves its bytes for as long as it runs, so two checked at once do
// not both count the same free space.

import (
	"fmt"
	"sync/atomic"

	"github.com/hehehai/AudioSmith/internal/catalog"
)

// DefaultReserve is the free space downloads keep by default: macOS
// misbehaves well before the disk is quite full, and the app's own work
// (dubs, books) needs room too.
const DefaultReserve = 6e9

// othersLocked are the bytes the downloads under way of repos other than
// repo still have to write. c.mu is held.
func (c *Client) othersLocked(repo string) int64 {
	var n int64
	for left, r := range c.inflight {
		if r != repo {
			n += max(left.Load(), 0)
		}
	}
	return n
}

// checkDiskLocked refuses p (repo's plan) with ErrDiskSpace when the
// cache volume would keep less than ReserveBytes free. c.mu is held.
func (c *Client) checkDiskLocked(repo string, p Plan) error {
	if c.opts.ReserveBytes <= 0 || p.FreeBytes < 0 {
		return nil
	}
	others := c.othersLocked(repo)
	if p.Bytes+others+c.opts.ReserveBytes <= p.FreeBytes {
		return nil
	}
	busy := ""
	if others > 0 {
		busy = fmt.Sprintf(" (other downloads still write %s)", catalog.HumanSize(others))
	}
	return fmt.Errorf("%s needs %s more and keeps %s free; %s free%s: %w", repo, catalog.HumanSize(p.Bytes),
		catalog.HumanSize(c.opts.ReserveBytes), catalog.HumanSize(p.FreeBytes), busy, ErrDiskSpace)
}

// reserve checks the disk for repo's download (plan p) and records its
// bytes as under way; the counter it returns is what is left to write
// (the download lowers it as it goes) until release.
func (c *Client) reserve(repo string, p Plan) (*atomic.Int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkDiskLocked(repo, p); err != nil {
		return nil, err
	}
	left := &atomic.Int64{}
	left.Store(p.Bytes)
	c.inflight[left] = repo
	return left, nil
}

// release forgets a download's reservation.
func (c *Client) release(left *atomic.Int64) {
	c.mu.Lock()
	delete(c.inflight, left)
	c.mu.Unlock()
}
