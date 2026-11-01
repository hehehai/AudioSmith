package mcp

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// A session connects while open and a request is in flight or recent;
// a stdio shim's session until it closes.
func TestSessionsIdle(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newSessions()
	tr.now = func() time.Time { return now }
	var timers int
	tr.after = func(time.Duration, func()) { timers++ }
	var changes []bool
	tr.onChange = func(c bool) { changes = append(changes, c) }

	// Requests for a session the SDK never opened do not count.
	tr.begin("ghost", false)
	tr.end("ghost")
	if tr.Connected() {
		t.Fatal("an unopened session counts")
	}

	tr.opened("a")
	if !tr.Connected() {
		t.Fatal("a new session does not count")
	}
	tr.begin("a", false) // a GET stream: lasts while the client runs
	now = now.Add(10 * time.Minute)
	tr.recheck()
	if !tr.Connected() {
		t.Error("a session with a request in flight went idle")
	}
	tr.end("a") // the client quit without DELETE
	if timers == 0 {
		t.Error("no recheck scheduled after the last request")
	}
	now = now.Add(IdleAfter - time.Second)
	tr.recheck()
	if !tr.Connected() {
		t.Error("idle for less than IdleAfter: not connected")
	}
	now = now.Add(2 * time.Second)
	tr.recheck()
	if tr.Connected() {
		t.Error("idle for IdleAfter: still connected")
	}

	// The shim's session stays while open, idle or not.
	tr.opened("s")
	tr.begin("s", true)
	tr.end("s")
	now = now.Add(time.Hour)
	tr.recheck()
	if !tr.Connected() {
		t.Error("an idle stdio session does not count")
	}
	tr.closed("s")
	tr.closed("a")
	if tr.Connected() {
		t.Error("closed sessions count")
	}
	want := []bool{true, false, true, false}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v, want %v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("changes = %v, want %v", changes, want)
		}
	}
	if len(tr.m) != 0 {
		t.Errorf("sessions left: %d", len(tr.m))
	}
}

// Over HTTP: a client connects, and closing it (DELETE) disconnects.
func TestAgentConnected(t *testing.T) {
	m, _, ts := serve(t, newFake(t))
	var mu sync.Mutex
	var got []bool
	m.OnAgentConnected(func(c bool) {
		mu.Lock()
		got = append(got, c)
		mu.Unlock()
	})
	if m.AgentConnected() {
		t.Fatal("connected before any client")
	}
	cs := connect(t, ts.URL, "claude-code", http.Header{HeaderTransport: {"stdio"}})
	waitFor(t, "connected", m.AgentConnected)
	cs.Close()
	waitFor(t, "disconnected after Close", func() bool { return !m.AgentConnected() })
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 || !got[0] || got[len(got)-1] {
		t.Errorf("OnAgentConnected saw %v", got)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("not %s within 5 s", what)
}
