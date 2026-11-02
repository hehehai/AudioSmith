package mcp

// Connected agents (thread #132): the menu bar icon shows a violet dot
// while an MCP client is connected. A session is open from the client's
// initialized notification until the SDK closes it (the client's DELETE,
// or SessionTimeout). An open session counts as connected when
//   - it came through the stdio shim, which ends it when its client goes
//     (closeSession), or
//   - it has a request in flight (a tool call; a client's GET stream,
//     which ends when the client quits), or
//   - it made a request in the last IdleAfter: HTTP clients may go
//     without closing their session, and then it lasts until the SDK's
//     30-minute timeout.

import (
	"net/http"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
)

// HeaderTransport tells the server how a client reaches it: the stdio
// shim sends "stdio".
const HeaderTransport = "X-AudioSmith-Transport"

// IdleAfter is how long an HTTP session counts as connected after its
// last request.
const IdleAfter = 60 * time.Second

type session struct {
	open     bool
	stdio    bool
	inflight int
	last     time.Time
}

// sessions tracks the MCP sessions by id. Goroutine-safe.
type sessions struct {
	mu        sync.Mutex
	now       func() time.Time
	after     func(time.Duration, func()) // time.AfterFunc
	m         map[string]*session
	connected bool
	onChange  func(bool)
	timer     bool // a recheck is scheduled
}

func newSessions() *sessions {
	return &sessions{
		now:   time.Now,
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		m:     map[string]*session{},
	}
}

// opened marks a session open (the SDK's InitializedHandler).
func (t *sessions) opened(id string) {
	if id == "" {
		return
	}
	t.mu.Lock()
	s := t.m[id]
	if s == nil {
		s = &session{}
		t.m[id] = s
	}
	s.open, s.last = true, t.now()
	t.changedLocked()
}

// closed forgets a session (the SDK closed it).
func (t *sessions) closed(id string) {
	t.mu.Lock()
	delete(t.m, id)
	t.changedLocked()
}

// begin and end bracket one HTTP request of a session. Only sessions the
// SDK opened are tracked: ids it never gave out (a client's made-up
// Mcp-Session-Id, which the SDK answers with 404) leave nothing behind
// (code review 11-02, N6). begin reports whether it counted the request;
// end only ends counted ones.
func (t *sessions) begin(id string, stdio bool) bool {
	t.mu.Lock()
	s := t.m[id]
	if s == nil {
		t.mu.Unlock()
		return false
	}
	s.inflight++
	s.last = t.now()
	s.stdio = s.stdio || stdio
	t.changedLocked()
	return true
}

func (t *sessions) end(id string) {
	t.mu.Lock()
	if s := t.m[id]; s != nil {
		s.inflight = max(0, s.inflight-1)
		s.last = t.now()
		if s.inflight == 0 && !s.stdio {
			t.scheduleLocked(IdleAfter + time.Second)
		}
	}
	t.changedLocked()
}

// seen marks a request that was not counted (its session opened during
// it: the initialized notification) as the session's latest, with its
// transport.
func (t *sessions) seen(id string, stdio bool) {
	t.mu.Lock()
	if s := t.m[id]; s != nil {
		s.last = t.now()
		s.stdio = s.stdio || stdio
		if !s.stdio {
			t.scheduleLocked(IdleAfter + time.Second)
		}
	}
	t.changedLocked()
}

// scheduleLocked schedules one recheck in d unless one is pending.
func (t *sessions) scheduleLocked(d time.Duration) {
	if t.timer {
		return
	}
	t.timer = true
	t.after(d, t.recheck)
}

// recheck notices sessions gone idle, and schedules itself again while a
// session counts only for its recent request.
func (t *sessions) recheck() {
	t.mu.Lock()
	t.timer = false
	now, next := t.now(), time.Duration(-1)
	for _, s := range t.m {
		if s.open && !s.stdio && s.inflight == 0 {
			if left := IdleAfter - now.Sub(s.last); left > 0 && (next < 0 || left < next) {
				next = left
			}
		}
	}
	if next > 0 {
		t.scheduleLocked(next + time.Second)
	}
	t.changedLocked()
}

func (t *sessions) live(s *session, now time.Time) bool {
	return s.open && (s.stdio || s.inflight > 0 || now.Sub(s.last) < IdleAfter)
}

// changedLocked works out whether a client is connected, drops sessions
// that never opened, unlocks, and tells onChange of a change.
func (t *sessions) changedLocked() {
	now, c := t.now(), false
	for id, s := range t.m {
		if !s.open && s.inflight == 0 && now.Sub(s.last) >= IdleAfter {
			delete(t.m, id) // requests for a session the SDK did not know
			continue
		}
		c = c || t.live(s, now)
	}
	changed := c != t.connected
	t.connected = c
	fn := t.onChange
	t.mu.Unlock()
	if changed && fn != nil {
		fn(c)
	}
}

// Connected reports whether an MCP client is connected now.
func (t *sessions) Connected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, s := range t.m {
		if t.live(s, now) {
			return true
		}
	}
	return false
}

// wrap tracks the requests of h by their session.
func (t *sessions) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("Mcp-Session-Id")
		if id == "" {
			h.ServeHTTP(w, r) // initialize: the session opens with initialized
			return
		}
		// Only a client on this Mac can be the stdio shim: a LAN client
		// claiming it would keep the menu bar's dot lit.
		stdio := r.Header.Get(HeaderTransport) == "stdio" && r.Header.Get(api.HeaderVia) == api.ViaLoopback
		if !t.begin(id, stdio) {
			h.ServeHTTP(w, r)
			t.seen(id, stdio)
			return
		}
		defer t.end(id)
		h.ServeHTTP(w, r)
	})
}

// AgentConnected reports whether an MCP client is connected now.
func (s *Server) AgentConnected() bool { return s.sessions.Connected() }

// OnAgentConnected calls fn (on any goroutine) when the answer of
// AgentConnected changes. Set it once, before clients come.
func (s *Server) OnAgentConnected(fn func(connected bool)) {
	s.sessions.mu.Lock()
	s.sessions.onChange = fn
	s.sessions.mu.Unlock()
}
