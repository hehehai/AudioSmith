package telephony

// The call gateway: a separate HTTP listener, reached by Twilio through
// the user's tunnel (cloudflared, ngrok), that serves only
//
//	POST /twilio/voice    voice webhook: an answered outbound call, or an incoming one → TwiML
//	POST /twilio/status   call progress events
//	GET  /twilio/stream   the Media Streams WebSocket
//	GET  /twilio/ping/<n> the setup page's reachability check (answers 204 to a nonce it expects)
//
// Every webhook must carry a valid X-Twilio-Signature for the public URL
// and the account's SID; streams need a single-use token (60 s) that the
// voice webhook's TwiML carried. Failed signatures and floods are rate
// limited (429). Nothing else of the app is reachable here.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
)

// Gateway paths.
const (
	PathVoice  = "/twilio/voice"
	PathStatus = "/twilio/status"
	PathStream = "/twilio/stream"
	PathPing   = "/twilio/ping/"
)

// DefaultGatewayPort is the gateway's port when none is set.
const DefaultGatewayPort = 3950

// Gateway limits.
const (
	TokenTTL          = 60 * time.Second
	webhooksPerMinute = 60
	failuresPerMinute = 10
	startTimeout      = 10 * time.Second
)

// GatewayConfig is what the gateway checks requests against, read at
// each request (settings may change while it runs).
type GatewayConfig struct {
	AccountSID string
	AuthToken  string
	// PublicURL is the tunnel's https origin ("https://x.trycloudflare.com").
	PublicURL string
}

// VoiceRequest is a signed voice webhook.
type VoiceRequest struct {
	CallSID    string
	AccountSID string
	From, To   string
	// Direction is "inbound", or "outbound-api" for calls placed through
	// the API.
	Direction string
	// CallID is the "call" query parameter AudioSmith put in the URL of
	// calls it placed; "" for incoming calls.
	CallID string
	Params url.Values
}

// VoiceAnswer is what to do with a call.
type VoiceAnswer struct {
	// CallID, when set, connects the call to a Media Stream that Stream
	// gets with this id.
	CallID string
	// Reject refuses it: "busy" or "rejected". Neither: hang up.
	Reject string
}

// StatusEvent is a call progress webhook.
type StatusEvent struct {
	CallSID  string
	CallID   string // the "call" query parameter, for calls AudioSmith placed
	Status   string // queued, initiated, ringing, in-progress, completed, busy, no-answer, canceled, failed
	Duration time.Duration
	Params   url.Values
}

// Rejection reasons reported to Hooks.Rejected.
const (
	RejectSignature = "invalid_signature"
	RejectToken     = "invalid_stream_token"
	RejectAccount   = "wrong_account"
	RejectLimit     = "rate_limited"
	RejectBusy      = "busy" // reported by the app's hooks, not the gateway
)

// Hooks are the app's side of the gateway. They run on request
// goroutines.
type Hooks interface {
	// Voice decides what happens to a call.
	Voice(ctx context.Context, r VoiceRequest) VoiceAnswer
	// Status records a call's progress.
	Status(ctx context.Context, ev StatusEvent)
	// Stream runs the call's media stream until it ends; ctx ends when
	// the gateway stops.
	Stream(ctx context.Context, callID string, s *MediaStream)
	// Signed reports a webhook that passed the signature check (the setup
	// page's "Twilio reached AudioSmith").
	Signed()
	// Rejected reports a refused request (reason: Reject*).
	Rejected(reason, callSID string)
}

// Gateway serves Twilio. Goroutine-safe.
type Gateway struct {
	cfg   func() GatewayConfig
	hooks Hooks
	logf  func(string, ...any)

	mu      sync.Mutex
	tokens  map[string]tokenEntry
	pings   map[string]time.Time
	pinged  map[string]bool
	window  time.Time // the current minute of the rate limits
	hits    int
	fails   int
	srv     *http.Server
	ln      net.Listener
	addr    string
	ctx     context.Context
	cancel  context.CancelFunc
	streams sync.WaitGroup
	now     func() time.Time
}

type tokenEntry struct {
	callID  string
	expires time.Time
}

// NewGateway makes a gateway. logf may be nil.
func NewGateway(cfg func() GatewayConfig, hooks Hooks, logf func(string, ...any)) *Gateway {
	if logf == nil {
		logf = log.Printf
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Gateway{cfg: cfg, hooks: hooks, logf: logf, tokens: map[string]tokenEntry{}, pings: map[string]time.Time{},
		pinged: map[string]bool{}, ctx: ctx, cancel: cancel, now: time.Now}
}

// ErrPortInUse: another program listens on the gateway's port.
var ErrPortInUse = errors.New("the port is in use")

// Start listens on addr ("127.0.0.1:3950"). Starting again on another
// address moves it.
func (g *Gateway) Start(addr string) error {
	g.mu.Lock()
	if g.srv != nil && g.addr == addr {
		g.mu.Unlock()
		return nil
	}
	g.mu.Unlock()
	g.Stop(context.Background())
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if core.IsAddrInUse(err) {
			return ErrPortInUse
		}
		return err
	}
	srv := &http.Server{Handler: g, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		ErrorLog: log.New(io.Discard, "", 0)}
	g.mu.Lock()
	g.srv, g.ln, g.addr = srv, ln, addr
	g.mu.Unlock()
	go srv.Serve(ln)
	return nil
}

// Addr is the address listened on ("" when stopped).
func (g *Gateway) Addr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ln == nil {
		return ""
	}
	return g.ln.Addr().String()
}

// Stop closes the listener. Running streams continue until their calls
// end (Close ends them).
func (g *Gateway) Stop(ctx context.Context) {
	g.mu.Lock()
	srv := g.srv
	g.srv, g.ln, g.addr = nil, nil, ""
	g.mu.Unlock()
	if srv != nil {
		srv.Shutdown(ctx)
	}
}

// Close stops the listener and every stream, and waits for them.
func (g *Gateway) Close(ctx context.Context) {
	g.Stop(ctx)
	g.cancel()
	done := make(chan struct{})
	go func() { g.streams.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// ExpectPing returns a nonce the next reachability check fetches at
// PathPing+nonce; Pinged reports whether it arrived.
func (g *Gateway) ExpectPing() string {
	n := randomToken(12)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, at := range g.pings {
		if now.Sub(at) > 5*time.Minute {
			delete(g.pings, k)
			delete(g.pinged, k)
		}
	}
	g.pings[n] = now
	return n
}

// Pinged reports whether the ping of nonce reached the gateway.
func (g *Gateway) Pinged(nonce string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pinged[nonce]
}

// IssueToken makes a single-use stream token for a call.
func (g *Gateway) IssueToken(callID string) string {
	t := randomToken(24)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, e := range g.tokens {
		if now.After(e.expires) {
			delete(g.tokens, k)
		}
	}
	g.tokens[t] = tokenEntry{callID: callID, expires: now.Add(TokenTTL)}
	return t
}

// useToken consumes a token: its call id, or false when it is unknown,
// used or expired.
func (g *Gateway) useToken(t string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.tokens[t]
	delete(g.tokens, t)
	if !ok || g.now().After(e.expires) {
		return "", false
	}
	return e.callID, true
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// limit counts a request and reports whether it may go on.
func (g *Gateway) limit() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if now.Sub(g.window) >= time.Minute {
		g.window, g.hits, g.fails = now, 0, 0
	}
	g.hits++
	return g.hits <= webhooksPerMinute && g.fails < failuresPerMinute
}

func (g *Gateway) fail() {
	g.mu.Lock()
	g.fails++
	g.mu.Unlock()
}

// publicURL is the URL Twilio requested, for the signature.
func publicURL(base string, r *http.Request) string {
	return strings.TrimRight(base, "/") + r.URL.RequestURI()
}

// StreamURL is the WebSocket URL of a public https origin.
func StreamURL(public string) string {
	u := strings.TrimRight(public, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + PathStream
}

// VoiceURL and StatusURL are the webhooks of a public origin.
func VoiceURL(public string) string  { return strings.TrimRight(public, "/") + PathVoice }
func StatusURL(public string) string { return strings.TrimRight(public, "/") + PathStatus }

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == PathVoice || r.URL.Path == PathStatus:
		g.webhook(w, r)
	case r.URL.Path == PathStream:
		g.stream(w, r)
	case strings.HasPrefix(r.URL.Path, PathPing) && r.Method == http.MethodGet:
		nonce := strings.TrimPrefix(r.URL.Path, PathPing)
		g.mu.Lock()
		_, ok := g.pings[nonce]
		if ok {
			g.pinged[nonce] = true
		}
		g.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// webhook checks and answers the voice and status webhooks.
func (g *Gateway) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !g.limit() {
		g.hooks.Rejected(RejectLimit, "")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cfg := g.cfg()
	params := r.PostForm
	callSID := params.Get("CallSid")
	if !ValidSignature(cfg.AuthToken, publicURL(cfg.PublicURL, r), params, r.Header.Get("X-Twilio-Signature")) {
		g.fail()
		g.logf("calls: refused a webhook with an invalid signature (%s)", r.URL.Path)
		g.hooks.Rejected(RejectSignature, callSID)
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	if params.Get("AccountSid") != cfg.AccountSID {
		g.fail()
		g.hooks.Rejected(RejectAccount, callSID)
		http.Error(w, "wrong account", http.StatusForbidden)
		return
	}
	g.hooks.Signed()
	callID := r.URL.Query().Get("call")
	if r.URL.Path == PathStatus {
		d, _ := time.ParseDuration(params.Get("CallDuration") + "s")
		g.hooks.Status(r.Context(), StatusEvent{CallSID: callSID, CallID: callID, Status: params.Get("CallStatus"), Duration: d, Params: params})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ans := g.hooks.Voice(r.Context(), VoiceRequest{CallSID: callSID, AccountSID: params.Get("AccountSid"), From: params.Get("From"),
		To: params.Get("To"), Direction: params.Get("Direction"), CallID: callID, Params: params})
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	switch {
	case ans.CallID != "":
		tok := g.IssueToken(ans.CallID)
		io.WriteString(w, StreamTwiML(StreamURL(cfg.PublicURL), map[string]string{"token": tok}))
	case ans.Reject != "":
		io.WriteString(w, RejectTwiML(ans.Reject))
	default:
		io.WriteString(w, HangupTwiML())
	}
}

// stream accepts a Media Stream: the handshake's signature (when Twilio
// sends one), then the start message's single-use token.
func (g *Gateway) stream(w http.ResponseWriter, r *http.Request) {
	if !g.limit() {
		g.hooks.Rejected(RejectLimit, "")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	cfg := g.cfg()
	if sig := r.Header.Get("X-Twilio-Signature"); sig != "" {
		https := publicURL(cfg.PublicURL, r)
		if !ValidSignature(cfg.AuthToken, https, nil, sig) && !ValidSignature(cfg.AuthToken, StreamURL(cfg.PublicURL)+strings.TrimPrefix(r.URL.RequestURI(), PathStream), nil, sig) {
			g.fail()
			g.hooks.Rejected(RejectSignature, "")
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}
	}
	ws, err := AcceptWebSocket(w, r)
	if err != nil {
		return
	}
	start, err := readStart(ws, startTimeout)
	if err != nil {
		ws.CloseWith(1008, "no start")
		return
	}
	callID, ok := g.useToken(start.Params["token"])
	if !ok {
		g.fail()
		g.logf("calls: refused a media stream with an invalid token (call %s)", start.CallSID)
		g.hooks.Rejected(RejectToken, start.CallSID)
		ws.CloseWith(1008, "invalid token")
		return
	}
	if cfg.AccountSID != "" && start.AccountSID != "" && start.AccountSID != cfg.AccountSID {
		g.fail()
		g.hooks.Rejected(RejectAccount, start.CallSID)
		ws.CloseWith(1008, "wrong account")
		return
	}
	g.streams.Add(1)
	defer g.streams.Done()
	ms := &MediaStream{ws: ws, Start: start}
	defer ws.Close()
	g.hooks.Stream(g.ctx, callID, ms)
}
