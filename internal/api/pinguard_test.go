package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Code review 11-03 M1: the PIN's guard (pinguard.go).

// lanClient is a LAN sharing server with a clock the test moves.
type lanClient struct {
	s     *Server
	share Share
	at    time.Time
}

func newLANClient(t *testing.T, o func(*Options)) *lanClient {
	t.Helper()
	f := newFake(t)
	opts := Options{Backend: f, Sched: f.sched, Bus: f.bus, Version: "1", Logf: t.Logf}
	if o != nil {
		o(&opts)
	}
	s := New(opts)
	share, err := s.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.StopLAN)
	c := &lanClient{s: s, share: share, at: time.Now()}
	c.clock()
	return c
}

// clock makes the current guard read c.at.
func (c *lanClient) clock() { c.s.share.Load().guard.now = func() time.Time { return c.at } }

// get asks /v1/models from ip with the header hdr: val; the status and
// Retry-After.
func (c *lanClient) get(ip, hdr, val string) (int, string) {
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = ip + ":1234"
	if hdr != "" {
		req.Header.Set(hdr, val)
	}
	w := httptest.NewRecorder()
	c.s.LANHandler(c.share.Port).ServeHTTP(w, req)
	return w.Code, w.Header().Get("Retry-After")
}

func (c *lanClient) code(ip, hdr, val string) int {
	n, _ := c.get(ip, hdr, val)
	return n
}

// TestPoCStaleKeyLocksPIN (the review's PoC): one device retrying with
// a stale API key for hours neither locks the PIN nor gets refused.
func TestPoCStaleKeyLocksPIN(t *testing.T) {
	c := newLANClient(t, nil)
	c.s.SetKey("as-new")
	for i := range 600 { // 10 hours, once a minute
		if n := c.code("10.0.0.5", "Authorization", "Bearer as-old"); n != http.StatusUnauthorized {
			t.Fatalf("stale key, try %d: %d", i, n)
		}
		c.at = c.at.Add(time.Minute)
	}
	if n := c.code("10.0.0.9", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("another device with the right PIN: %d", n)
	}
	if sh, _ := c.s.Sharing(); sh.Locked || sh.Limited != 0 {
		t.Errorf("status: %+v", sh)
	}
}

// TestStalePINCountsOnce: a client still sending the PIN of the last
// sharing, over and over, counts once.
func TestStalePINCountsOnce(t *testing.T) {
	c := newLANClient(t, nil)
	for i := range 1000 {
		if n := c.code("10.0.0.5", HeaderPIN, "12345678"); n != http.StatusUnauthorized {
			t.Fatalf("stale PIN, try %d: %d", i, n)
		}
		c.at = c.at.Add(5 * time.Second)
	}
	if n := c.code("10.0.0.9", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("right PIN: %d", n)
	}
}

// TestWrongKeysDoNotLockPIN: wrong keys from many addresses do not
// count toward the PIN's lock (a key cannot be guessed).
func TestWrongKeysDoNotLockPIN(t *testing.T) {
	c := newLANClient(t, nil)
	c.s.SetKey("as-k")
	for i := range 3 * globalMax {
		c.code(fmt.Sprintf("10.1.%d.%d", i/200, i%200), "Authorization", "Bearer as-"+strconv.Itoa(i))
	}
	if n := c.code("10.0.0.9", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("right PIN after wrong keys: %d", n)
	}
}

// TestPINLimitPerAddress: addrMax different wrong values from one
// address refuse it (429, Retry-After) and nobody else; the limit
// decays.
func TestPINLimitPerAddress(t *testing.T) {
	var told atomic.Int32
	c := newLANClient(t, func(o *Options) { o.ShareChanged = func() { told.Add(1) } })
	for i := range addrMax {
		if n := c.code("10.0.0.5", HeaderPIN, fmt.Sprintf("%08d", i)); n != http.StatusUnauthorized {
			t.Fatalf("wrong PIN %d: %d", i, n)
		}
		c.at = c.at.Add(time.Second)
	}
	n, retry := c.get("10.0.0.5", HeaderPIN, c.share.PIN)
	if n != http.StatusTooManyRequests || retry == "" {
		t.Errorf("after %d wrong PINs: %d Retry-After %q", addrMax, n, retry)
	}
	if n := c.code("10.0.0.6", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("another address: %d", n)
	}
	sh, _ := c.s.Sharing()
	if sh.Locked || sh.Limited != 1 || sh.Next.IsZero() {
		t.Errorf("status: %+v", sh)
	}
	if told.Load() == 0 {
		t.Error("ShareChanged not told")
	}
	c.at = sh.Next
	if n := c.code("10.0.0.5", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("after the window: %d", n)
	}
}

// TestPINLockEndsAndRenews: the global lock tells the UI, ends by itself
// after globalWindow, and RenewPIN ends it at once (old PIN gone).
func TestPINLockEndsAndRenews(t *testing.T) {
	c := newLANClient(t, nil)
	start := c.at
	for i := range globalMax {
		c.code(fmt.Sprintf("10.2.%d.%d", i/100, i%100), HeaderPIN, "00000000")
	}
	n, retry := c.get("10.0.0.9", HeaderPIN, c.share.PIN)
	if n != http.StatusTooManyRequests || retry == "" {
		t.Fatalf("right PIN while locked: %d %q", n, retry)
	}
	sh, _ := c.s.Sharing()
	if !sh.Locked || !sh.Until.Equal(start.Add(globalWindow)) || !sh.Next.Equal(sh.Until) {
		t.Fatalf("status: %+v", sh)
	}
	c.at = sh.Until
	if n := c.code("10.0.0.9", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("after the lock's end: %d", n)
	}
	c.at = start
	if sh, _ := c.s.Sharing(); !sh.Locked {
		t.Fatal("not locked again")
	}
	old := c.share.PIN
	sh, ok := c.s.RenewPIN()
	if !ok || sh.Locked || sh.PIN == old || sh.Port != c.share.Port {
		t.Fatalf("renew: %v %+v", ok, sh)
	}
	c.share = sh
	if n := c.code("10.0.0.9", HeaderPIN, sh.PIN); n != 200 {
		t.Errorf("new PIN: %d", n)
	}
	if n := c.code("10.0.0.8", HeaderPIN, old); n != http.StatusUnauthorized {
		t.Errorf("old PIN: %d", n)
	}
	c.s.StopLAN()
	if _, ok := c.s.RenewPIN(); ok {
		t.Error("RenewPIN while sharing is off")
	}
}

// TestPINOneAddressCannotLock: one address guessing all day (within its
// limit) adds at most addrMax to the global count.
func TestPINOneAddressCannotLock(t *testing.T) {
	c := newLANClient(t, nil)
	for i := range 24 * 6 * addrMax { // a day at the address's limit
		c.code("10.0.0.5", HeaderPIN, fmt.Sprintf("%08d", i))
		if (i+1)%addrMax == 0 {
			c.at = c.at.Add(addrWindow)
		}
	}
	if n := c.code("10.0.0.9", HeaderPIN, c.share.PIN); n != 200 {
		t.Errorf("right PIN from another device: %d", n)
	}
}
