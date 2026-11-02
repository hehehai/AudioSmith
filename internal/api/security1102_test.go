package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

// TestHeaders1102: every answer says nosniff and no framing; preflights
// allow PATCH (0.5's routes use it).
func TestHeaders1102(t *testing.T) {
	f := newFake(t)
	_, ts := f.server()
	resp := do(t, ts, "GET", "/health", nil, nil)
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("headers %v", resp.Header)
	}
	f.update(func(f *fakeBackend) { f.st.API.Origins = []string{"http://app.example"} })
	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/voices/x", nil)
	req.Header.Set("Origin", "http://app.example")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !strings.Contains(r.Header.Get("Access-Control-Allow-Methods"), "PATCH") {
		t.Errorf("preflight methods %q", r.Header.Get("Access-Control-Allow-Methods"))
	}
}

// TestLANRateFitRewrite (N3): with the PIN only, a rate fit's rewrite
// (the writing help, maybe a cloud model) is refused; the prediction and
// the rewrite on the Mac work.
func TestLANRateFitRewrite(t *testing.T) {
	f := newFake(t)
	s, ts := f.server()
	share, err := s.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopLAN()
	lan := httptest.NewServer(s.LANHandler(share.Port))
	defer lan.Close()
	body := map[string]any{"text": "hello there", "language": "en", "slot": 1, "rewrite": true}
	var e apiErr
	if r := do(t, lan, "POST", "/api/tools/rate-fit", body, &e, HeaderPIN, share.PIN); r.StatusCode != 403 || e.Error.Code != CodeUnauthorized {
		t.Errorf("LAN rewrite: %d %+v", r.StatusCode, e)
	}
	delete(body, "rewrite")
	if r := do(t, lan, "POST", "/api/tools/rate-fit", body, nil, HeaderPIN, share.PIN); r.StatusCode != 200 {
		t.Errorf("LAN fit: %d", r.StatusCode)
	}
	body["rewrite"] = true
	if r := do(t, ts, "POST", "/api/tools/rate-fit", body, nil); r.StatusCode != 200 {
		t.Errorf("local rewrite: %d", r.StatusCode)
	}
}

// TestPINGuessedFromManyAddresses (L1): wrong PINs from many addresses
// (each under the per-address limit) lock the PIN; the key still works.
func TestPINGuessedFromManyAddresses(t *testing.T) {
	f := newFake(t)
	s, _ := f.server()
	share, err := s.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopLAN()
	h := s.LANHandler(share.Port)
	get := func(ip, hdr, val string) int {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Host = "127.0.0.1"
		req.RemoteAddr = ip + ":1234"
		req.Header.Set(hdr, val)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := range maxPINFails {
		get(fmt.Sprintf("10.0.%d.%d", i/5, i%5), HeaderPIN, "00000000")
	}
	if c := get("10.9.9.9", HeaderPIN, share.PIN); c != http.StatusTooManyRequests {
		t.Errorf("right PIN after %d wrong ones: %d", maxPINFails, c)
	}
	s.SetKey("as-k")
	if c := get("10.9.9.9", "Authorization", "Bearer as-k"); c != 200 {
		t.Errorf("key after the lock: %d", c)
	}
	share, err = s.StartLAN(0) // sharing again: a new PIN, a new count
	if err != nil {
		t.Fatal(err)
	}
	h = s.LANHandler(share.Port)
	if c := get("10.9.9.9", HeaderPIN, share.PIN); c != 200 {
		t.Errorf("new PIN: %d", c)
	}
}

// TestBodyIdle: a request body that stalls is dropped; once a body is
// read, a long answer is not cut.
func TestBodyIdle(t *testing.T) {
	old := bodyIdle
	bodyIdle = 200 * time.Millisecond
	t.Cleanup(func() { bodyIdle = old })
	f := newFake(t)
	s := New(Options{Backend: f, Sched: f.sched, Bus: f.bus, Version: "1", Logf: t.Logf})
	s.handle("POST /test/slow", accessPublic, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		time.Sleep(3 * bodyIdle)
		io.WriteString(w, "done")
	})
	s.handle("POST /test/json", accessPublic, func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if err := readJSON(w, r, 1<<20, &v); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(3 * bodyIdle) // a long stream: its context must live
		fmt.Fprint(w, r.Context().Err())
	})
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	defer s.Close(t.Context())
	addr := fmt.Sprintf("127.0.0.1:%d", s.Port())
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /test/slow HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 10\r\n\r\nabc")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err == nil {
		resp.Body.Close()
	}
	if err == nil && resp.StatusCode != 400 || time.Since(start) > 3*time.Second {
		t.Errorf("stalled body: %v %v after %v", resp, err, time.Since(start))
	}
	r, err := http.Post("http://"+addr+"/test/slow", "text/plain", strings.NewReader("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != "done" {
		t.Errorf("long answer: %d %q", r.StatusCode, b)
	}
	r, err = http.Post("http://"+addr+"/test/json", "application/json", strings.NewReader(`{"a": 1}`+strings.Repeat(" ", 20000))) // the decoder stops before the end
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != "<nil>" {
		t.Errorf("long stream after a JSON body: %d %q", r.StatusCode, b)
	}
}

// TestLANVoiceRefText (N1): LAN clients with the PIN only read voices
// without the reference's transcript; on the Mac it is there.
func TestLANVoiceRefText(t *testing.T) {
	f := newFake(t)
	f.update(func(f *fakeBackend) {
		f.voices = append(f.voices, core.Voice{ID: "c1", Name: "Me", Kind: core.VoiceCloned, RefText: "my private words"})
	})
	s, ts := f.server()
	share, err := s.StartLAN(0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopLAN()
	lan := httptest.NewServer(s.LANHandler(share.Port))
	defer lan.Close()
	var v VoiceJSON
	if do(t, lan, "GET", "/api/voices/c1", nil, &v, HeaderPIN, share.PIN); v.Name != "Me" || v.RefText != "" {
		t.Errorf("LAN voice %+v", v)
	}
	var list struct{ Data []VoiceJSON }
	do(t, lan, "GET", "/api/voices", nil, &list, HeaderPIN, share.PIN)
	for _, v := range list.Data {
		if v.RefText != "" {
			t.Errorf("LAN list %+v", v)
		}
	}
	if do(t, ts, "GET", "/api/voices/c1", nil, &v); v.RefText != "my private words" {
		t.Errorf("local voice %+v", v)
	}
}
