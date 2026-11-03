package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestAPISharePINLock (code review 11-03 M1): wrong PINs from many
// devices lock the PIN; Settings → Local API says so (the guard tells
// the app), and 「New PIN」 makes a new one that works at once.
func TestAPISharePINLock(t *testing.T) {
	useMemKeys(t)
	a, _, tt := newTestApp(t)
	tallSettings(tt)
	startTestAPI(t, a)
	a.updateSettings(func(s *settings.Settings) { s.API.SharePort = freePort(t) })
	a.startSharing()
	waitUntil(t, func() bool { return a.apiShare != nil || a.apiShareE != "" })
	if a.apiShare == nil {
		t.Fatalf("sharing: %q", a.apiShareE)
	}
	t.Cleanup(a.stopSharing)
	a.showPage("assets")
	a.navigate("/settings/api")
	tt.Frame()
	locked := "the PIN is paused until"
	if tt.HasText(locked) {
		t.Fatal("locked before any wrong PIN")
	}
	old := a.apiShare.PIN
	h := a.apiSrv.LANHandler(a.apiShare.Port)
	get := func(ip, pin string) int {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Host = "127.0.0.1"
		req.RemoteAddr = ip + ":1234"
		req.Header.Set(api.HeaderPIN, pin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := range 200 {
		get(fmt.Sprintf("10.3.%d.%d", i/100, i%100), "00000000")
	}
	if c := get("10.9.9.9", old); c != http.StatusTooManyRequests {
		t.Fatalf("right PIN while locked: %d", c)
	}
	waitUntil(t, func() bool { return a.apiShare.Locked })
	tt.Frame()
	if !tt.HasText(locked) {
		t.Fatalf("the lock is not shown: %q", tt.Texts())
	}
	if n := slices.Index(tt.Texts(), "New PIN"); n < 0 || slices.Index(tt.Texts()[n+1:], "New PIN") >= 0 {
		t.Fatalf("not exactly one New PIN: %q", tt.Texts())
	}
	if err := tt.Click("New PIN"); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return a.apiShare.PIN != old })
	tt.Frame()
	sh := a.apiShare
	if sh.Locked || tt.HasText(locked) || !tt.HasText(sh.PIN[:4]+" "+sh.PIN[4:]) {
		t.Fatalf("after New PIN: %+v %q", sh, tt.Texts())
	}
	if c := get("10.9.9.9", sh.PIN); c != http.StatusOK {
		t.Errorf("new PIN: %d", c)
	}
}
