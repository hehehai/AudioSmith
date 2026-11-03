package hub

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Code review 1102 L7: downloads keep 6 GB free by default, and the bytes
// the downloads under way still write count against the free space, so
// several started at once (the net lane runs three) cannot together fill
// the disk.
func TestDownloadsReserveTogether(t *testing.T) {
	if got := New(Options{}).Options().ReserveBytes; got != 6e9 {
		t.Errorf("default reserve %d", got)
	}
	h := newFakeHub(t)
	const free = 1_000_000
	c := h.client(t, func(o *Options) { o.ReserveBytes = free - 50_000 }) // room for 50 kB
	c.free = func(string) int64 { return free }
	if _, err := c.Plan(context.Background(), h.entry()); err != nil {
		t.Fatalf("alone the repo (12 kB) fits: %v", err)
	}
	// Another repo's download is under way with 45 kB still to write.
	other, err := c.reserve("other/repo", Plan{Bytes: 45_000, FreeBytes: free})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Plan(context.Background(), h.entry())
	if !errors.Is(err, ErrDiskSpace) || !strings.Contains(err.Error(), "other downloads") {
		t.Fatalf("plan beside it: %v", err)
	}
	if err := c.Download(context.Background(), h.entry(), nil); !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("download beside it: %v", err)
	}
	if h.count("served") != 0 {
		t.Error("downloaded despite the other download")
	}
	// Two checked at once do not both count the same space.
	if _, err := c.reserve("third/repo", Plan{Bytes: 10_000, FreeBytes: free}); !errors.Is(err, ErrDiskSpace) {
		t.Fatalf("a third beside the other: %v", err)
	}
	// As the other download writes, what it reserves shrinks.
	other.Store(1_000)
	if _, err := c.Plan(context.Background(), h.entry()); err != nil {
		t.Fatalf("after the other wrote most: %v", err)
	}
	c.release(other)
	if err := c.Download(context.Background(), h.entry(), nil); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	n := len(c.inflight)
	c.mu.Unlock()
	if n != 0 {
		t.Errorf("%d reservations left after the downloads", n)
	}
	// A repo's own reservation does not count against its plan.
	mine, _ := c.reserve(testRepo, Plan{Bytes: 45_000, FreeBytes: -1})
	defer c.release(mine)
	if _, err := c.Plan(context.Background(), h.entry()); err != nil {
		t.Errorf("its own download counted: %v", err)
	}
}
