package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/core"
)

// lanSession connects to a's LAN listener with the PIN only.
func lanSession(t *testing.T, a *api.Server) *sdk.ClientSession {
	t.Helper()
	share, err := a.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.StopLAN)
	lan := httptest.NewServer(a.LANHandler(share.Port))
	t.Cleanup(lan.Close)
	return connect(t, lan.URL, "lan-agent", http.Header{api.HeaderPIN: {share.PIN}})
}

// TestLANSecurity1102 turns the code review's PoC (11-02) into checks: a
// LAN client with the PIN only reads no History through history://recent
// (N1), learns nothing about paths outside the sandbox (L2), and gets no
// rewrite from the user's writing help (N3); on the Mac all still work.
func TestLANSecurity1102(t *testing.T) {
	f := newFake(t)
	f.takes["t1"] = core.Take{ID: "t1", Text: "SECRET: my bank PIN is 4711", CreatedAt: time.Now()}
	_, a, ts := serve(t, f)
	cs := lanSession(t, a)

	_, err := cs.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "history://recent"})
	if err == nil || !strings.Contains(err.Error(), api.CodeUnauthorized) {
		t.Errorf("LAN history://recent: %v", err)
	}
	local := connect(t, ts.URL, "claude-code", nil)
	res, err := local.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "history://recent"})
	if err != nil || !strings.Contains(res.Contents[0].Text, "SECRET") {
		t.Errorf("local history://recent: %v", err)
	}

	var answers []string
	for _, p := range []string{"/etc/passwd", "/etc/does-not-exist-xyz", "/etc"} {
		r := call(t, cs, "transcribe", map[string]any{"path": p})
		txt := textOf(r)
		if !r.IsError || !strings.HasPrefix(txt, api.CodePathNotAllowed+":") {
			t.Errorf("LAN transcribe %s: %s", p, txt)
		}
		answers = append(answers, txt)
	}
	if answers[0] != answers[1] || answers[1] != answers[2] {
		t.Errorf("LAN answers tell paths apart: %q", answers)
	}
	if r := call(t, local, "transcribe", map[string]any{"path": "/etc/does-not-exist-xyz"}); !strings.Contains(textOf(r), "cannot read") {
		t.Errorf("local transcribe of a missing file: %s", textOf(r))
	}

	fit := map[string]any{"text": "hello there", "slot": 1, "rewrite": true}
	if r := call(t, cs, "check_line_fit", fit); !r.IsError || !strings.HasPrefix(textOf(r), api.CodeUnauthorized+":") {
		t.Errorf("LAN check_line_fit rewrite: %s", textOf(r))
	}
	delete(fit, "rewrite")
	if r := call(t, cs, "check_line_fit", fit); r.IsError {
		t.Errorf("LAN check_line_fit: %s", textOf(r))
	}
	fit["rewrite"] = true
	if r := call(t, local, "check_line_fit", fit); r.IsError {
		t.Errorf("local check_line_fit rewrite: %s", textOf(r))
	}
}

// TestUntrustedWithoutVia: a call that did not come through the API's
// guard (no HeaderVia) is not trusted.
func TestUntrustedWithoutVia(t *testing.T) {
	if (caller{}).trusted() || (caller{via: api.ViaLAN}).trusted() {
		t.Error("no via or LAN is trusted")
	}
	if !(caller{via: api.ViaLoopback}).trusted() || !(caller{via: api.ViaLANKey}).trusted() {
		t.Error("loopback or the key is not trusted")
	}
}

// TestSessionFlood (N6): made-up Mcp-Session-Id headers leave nothing in
// the session table, and a LAN client cannot pass for the stdio shim.
func TestSessionFlood(t *testing.T) {
	m, a, ts := serve(t, newFake(t))
	for i := range 300 {
		req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
		req.Header.Set("Mcp-Session-Id", fmt.Sprintf("bogus-%d", i))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	m.sessions.mu.Lock()
	n := len(m.sessions.m)
	m.sessions.mu.Unlock()
	if n != 0 {
		t.Errorf("%d sessions tracked after made-up ids", n)
	}

	share, err := a.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.StopLAN)
	lan := httptest.NewServer(a.LANHandler(share.Port))
	t.Cleanup(lan.Close)
	cs := connect(t, lan.URL, "lan-agent", http.Header{api.HeaderPIN: {share.PIN}, HeaderTransport: {"stdio"}})
	call(t, cs, "list_voices", nil)
	m.sessions.mu.Lock()
	defer m.sessions.mu.Unlock()
	for id, s := range m.sessions.m {
		if s.stdio {
			t.Errorf("LAN session %s counts as the stdio shim", id)
		}
	}
}

// TestSessionsOneTimer: many requests schedule one recheck, which goes
// on until the last session goes idle.
func TestSessionsOneTimer(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := newSessions()
	tr.now = func() time.Time { return now }
	var pending []func()
	tr.after = func(_ time.Duration, f func()) { pending = append(pending, f) }
	tr.opened("a")
	for range 50 {
		tr.begin("a", false)
		tr.end("a")
	}
	if len(pending) != 1 {
		t.Fatalf("%d timers for 50 requests", len(pending))
	}
	now = now.Add(IdleAfter / 2)
	tr.begin("a", false)
	tr.end("a")
	now = now.Add(IdleAfter/2 + time.Second)
	f := pending[0]
	pending = pending[1:]
	f() // the first timer: a still counts (its last request was recent)
	if !tr.Connected() || len(pending) != 1 {
		t.Fatalf("connected %v, %d timers", tr.Connected(), len(pending))
	}
	now = now.Add(IdleAfter)
	pending[0]()
	if tr.Connected() {
		t.Error("still connected after IdleAfter")
	}
}

// TestHintsAndNames (N7, N8): overwriting tools are destructive, tools
// that may reach a cloud model go online, and detect_watermark claims no
// proof.
func TestHintsAndNames(t *testing.T) {
	cs := connect(t, serveKit(t, newKitFake(t)).URL, "claude-code", nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*sdk.Tool{}
	for _, tl := range res.Tools {
		by[tl.Name] = tl
	}
	for _, n := range []string{"export_audio", "export_voice", "update_longform", "edit_dub", "update_voice"} {
		if a := by[n].Annotations; a.DestructiveHint == nil || !*a.DestructiveHint {
			t.Errorf("%s is not destructive", n)
		}
	}
	for _, n := range []string{"check_line_fit", "write_story", "run_dub"} {
		if a := by[n].Annotations; a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s does not go online", n)
		}
	}
	if d := by["detect_watermark"].Description; strings.Contains(d, "this Mac") || !strings.Contains(d, "not proof") {
		t.Errorf("detect_watermark: %s", d)
	}
}
