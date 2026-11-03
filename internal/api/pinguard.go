package api

import (
	"crypto/sha256"
	"slices"
	"sync"
	"time"
)

// pinGuard counts wrong credentials on the LAN listener (code review
// 11-03, M1). It lives as long as one PIN: StartLAN and RenewPIN make a
// new one.
//
//   - Each address may send addrMax distinct wrong values (PINs or keys)
//     in addrWindow; then it gets 429 until the oldest one is
//     addrWindow old. The same wrong value again (a client still holding
//     an old PIN or key) counts once while it keeps coming (forgotten
//     after memory without it).
//   - Only wrong PINs count toward the global ceiling (an API key cannot
//     be guessed): globalMax in globalWindow, each address adding at most
//     addrMax, lock the PIN for everyone until enough of them are
//     globalWindow old: a last resort against guessing from many
//     addresses, which ends by itself (or with a new PIN).
//
// Guessing then gets at most globalMax PINs an hour: an 8-digit PIN holds
// for years.
type pinGuard struct {
	mu   sync.Mutex
	now  func() time.Time
	by   map[string]map[[32]byte]*wrongValue // remote address → wrong values
	note func()                              // told when Locked or Limited may have changed
}

type wrongValue struct {
	first, last time.Time
	pin         bool
}

const (
	addrMax      = 10
	addrWindow   = 10 * time.Minute
	globalMax    = 100
	globalWindow = time.Hour
	memory       = globalWindow // a wrong value not seen for this long is forgotten
	maxAddrs     = 4096         // addresses remembered at most (the oldest go)
)

func newPINGuard() *pinGuard {
	return &pinGuard{now: time.Now, by: map[string]map[[32]byte]*wrongValue{}}
}

func valueHash(kind, v string) [32]byte { return sha256.Sum256([]byte(kind + "\x00" + v)) }

// prune forgets what no longer counts; g.mu held.
func (g *pinGuard) prune(now time.Time) {
	for ip, vals := range g.by {
		for h, w := range vals {
			if now.Sub(w.last) >= memory && now.Sub(w.first) >= globalWindow {
				delete(vals, h)
			}
		}
		if len(vals) == 0 {
			delete(g.by, ip)
		}
	}
}

// addrState is how many values from ip count toward its limit, and when
// the oldest of them stops counting; g.mu held.
func (g *pinGuard) addrState(ip string, now time.Time) (n int, free time.Time) {
	for _, w := range g.by[ip] {
		if now.Sub(w.first) < addrWindow {
			n++
			if end := w.first.Add(addrWindow); free.IsZero() || end.Before(free) {
				free = end
			}
		}
	}
	return n, free
}

// globalState is the wrong PINs counting toward the ceiling, and when
// the lock (if any) ends; g.mu held.
func (g *pinGuard) globalState(now time.Time) (n int, until time.Time) {
	var firsts []time.Time // of the counted PINs
	for _, vals := range g.by {
		var mine []time.Time
		for _, w := range vals {
			if w.pin && now.Sub(w.first) < globalWindow {
				mine = append(mine, w.first)
			}
		}
		// At most addrMax per address: its newest.
		sortTimes(mine)
		if len(mine) > addrMax {
			mine = mine[len(mine)-addrMax:]
		}
		firsts = append(firsts, mine...)
	}
	n = len(firsts)
	if n >= globalMax {
		sortTimes(firsts)
		// Unlocked once n-globalMax+1 of them are old enough.
		until = firsts[n-globalMax].Add(globalWindow)
	}
	return n, until
}

func sortTimes(ts []time.Time) { slices.SortFunc(ts, time.Time.Compare) }

// blocked reports whether ip may not try now, and for how long.
func (g *pinGuard) blocked(ip string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	n, free := g.addrState(ip, now)
	if n < addrMax {
		return false, 0
	}
	return true, free.Sub(now)
}

// locked reports whether the PIN is locked for everyone, and until when.
func (g *pinGuard) locked() (bool, time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, until := g.globalState(g.now())
	return n >= globalMax, until
}

// fail records a wrong PIN (pin) or key from ip.
func (g *pinGuard) fail(ip string, pin bool, value string) {
	kind := "key"
	if pin {
		kind = "pin"
	}
	h := valueHash(kind, value)
	g.mu.Lock()
	now := g.now()
	vals := g.by[ip]
	if w := vals[h]; w != nil {
		w.last = now // the same wrong value again: counted once
		g.mu.Unlock()
		return
	}
	g.prune(now)
	if len(g.by) >= maxAddrs && vals == nil {
		g.dropOldest()
	}
	if vals == nil {
		vals = map[[32]byte]*wrongValue{}
		g.by[ip] = vals
	}
	vals[h] = &wrongValue{first: now, last: now, pin: pin}
	note := g.note
	g.mu.Unlock()
	if note != nil {
		note()
	}
}

// dropOldest forgets the address whose newest wrong value is oldest;
// g.mu held.
func (g *pinGuard) dropOldest() {
	var oldest string
	var at time.Time
	for ip, vals := range g.by {
		var newest time.Time
		for _, w := range vals {
			if w.last.After(newest) {
				newest = w.last
			}
		}
		if oldest == "" || newest.Before(at) {
			oldest, at = ip, newest
		}
	}
	delete(g.by, oldest)
}

// status is the guard's state for the UI: the PIN locked (until when),
// how many addresses are refused for now, and when that next changes by
// itself (zero: nothing pending).
func (g *pinGuard) status() (locked bool, until time.Time, limited int, next time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	n, until := g.globalState(now)
	locked = n >= globalMax
	if locked {
		next = until
	}
	for ip := range g.by {
		if k, free := g.addrState(ip, now); k >= addrMax {
			limited++
			if next.IsZero() || free.Before(next) {
				next = free
			}
		}
	}
	return locked, until, limited, next
}
