package api

import (
	"crypto/rand"
	"crypto/subtle"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/oplog"
)

// Request headers of the API.
const (
	// HeaderCSRF must be on state-changing /api requests that carry no
	// API key (any value): a page in a browser cannot send it to another
	// origin without a CORS preflight, which the server refuses.
	HeaderCSRF = "X-AudioSmith-Request"
	// HeaderPIN carries the LAN sharing PIN (also ?pin= or the cookie).
	HeaderPIN = "X-AudioSmith-Pin"
	// HeaderClient names the client (agent) for per-agent voices.
	HeaderClient = "X-AudioSmith-Client"
	// HeaderVia is set by the server (never by clients) on requests it
	// passes on: ViaLoopback, ViaLAN (PIN) or ViaLANKey. The MCP server
	// reads it to decide whether a tool may read files by path.
	HeaderVia = "X-AudioSmith-Via"
	pinCookie = "audiosmith_pin"
)

// Values of HeaderVia.
const (
	ViaLoopback = "loopback"
	ViaLAN      = "lan"
	ViaLANKey   = "lan-key"
)

// loopbackHosts are the names the loopback listener answers to.
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// guard checks every request before its route runs: the Host header
// (DNS rebinding), the Origin (browsers), credentials (the API key, the
// LAN PIN, or none on loopback) against the route's access, and the CSRF
// header. lan is the LAN listener (on port).
func (s *Server) guard(lan bool, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Code review 11-02: no type sniffing (/api/agent-skill's
		// markdown), no framing of any page.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w = bodyDeadline(w, r)
		host := hostname(r.Host)
		if !s.hostAllowed(lan, host) {
			writeError(w, &Error{Status: http.StatusForbidden, Code: CodeForbiddenHost,
				Message: "Host " + strconv.Quote(r.Host) + " is not allowed: use http://127.0.0.1."})
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !s.originAllowed(lan, r.Host, origin) {
			writeError(w, &Error{Status: http.StatusForbidden, Code: CodeForbiddenOrigin,
				Message: "Origin " + strconv.Quote(origin) + " is not allowed: add it in AudioSmith → Settings → Local API."})
			return
		}
		if origin != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "X-AudioSmith-Take-Id, X-AudioSmith-Model, X-AudioSmith-Voice, X-AudioSmith-Seed, X-AudioSmith-Job-Id, Retry-After")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, "+HeaderCSRF+", "+HeaderPIN+", "+HeaderClient+", Mcp-Session-Id, Mcp-Protocol-Version")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		_, pattern := s.mux.Handler(r)
		acc, known := s.access[pattern]
		if !known {
			acc = accessAdmin // unknown paths answer 404 to those who may see them
		}
		if acc != accessPublic {
			if err := s.authorize(lan, r, acc); err != nil {
				if isStateChanging(r.Method) {
					oplog.Warn("api.denied", "method", r.Method, "path", r.URL.Path, "lan", lan, "error", err)
				}
				writeError(w, err)
				return
			}
			s.seen()
		}
		if isStateChanging(r.Method) && r.URL.Path != "/mcp" { // MCP logs its tools (internal/mcp)
			start, sw := time.Now(), &statusWriter{ResponseWriter: w}
			w = sw
			who := "api"
			if lan {
				who = "lan"
			}
			r = r.WithContext(oplog.WithActor(r.Context(), who))
			defer func() {
				st := sw.status
				if st == 0 {
					st = http.StatusOK
				}
				log := oplog.Info
				if st >= 400 {
					log = oplog.Warn
				}
				log("api.call", "method", r.Method, "path", r.URL.Path, "via", who, "status", st, "ms", time.Since(start))
			}()
		}
		via := ViaLoopback
		if lan {
			via = ViaLAN
			if s.hasKey(r) {
				via = ViaLANKey
			}
		}
		r.Header.Set(HeaderVia, via)
		if isStateChanging(r.Method) && strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get(HeaderCSRF) == "" && !s.hasKey(r) {
			writeError(w, &Error{Status: http.StatusForbidden, Code: CodeCSRF,
				Message: "Send the header " + HeaderCSRF + ": 1 with requests that change something (or an API key)."})
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func isStateChanging(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// hostname is the host of a Host header, without port or brackets.
func hostname(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

func (s *Server) hostAllowed(lan bool, host string) bool {
	if slices.Contains(loopbackHosts, host) {
		return true
	}
	if !lan {
		return false
	}
	st := s.share.Load()
	return st != nil && slices.ContainsFunc(st.Hosts, func(h string) bool { return strings.EqualFold(h, host) })
}

// originAllowed: the API's own origin (a page it serves, /docs), and the
// origins of the settings.
func (s *Server) originAllowed(lan bool, reqHost, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if strings.EqualFold(u.Host, reqHost) && s.hostAllowed(lan, hostname(u.Host)) {
		return true
	}
	for _, o := range s.b.Settings().API.Origins {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(o), "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

// bearer is the request's API key: Authorization: Bearer, or X-API-Key.
func bearer(r *http.Request) string {
	if a := r.Header.Get("Authorization"); a != "" {
		if k, ok := strings.CutPrefix(a, "Bearer "); ok {
			return strings.TrimSpace(k)
		}
		if k, ok := strings.CutPrefix(a, "bearer "); ok {
			return strings.TrimSpace(k)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

// hasKey reports whether the request carries the right API key.
func (s *Server) hasKey(r *http.Request) bool {
	key, got := s.Key(), bearer(r)
	return key != "" && got != "" && subtle.ConstantTimeCompare([]byte(key), []byte(got)) == 1
}

// authorize decides whether the request may use a route of access acc.
//
//	loopback, "Require a key" off:  everything (a Bearer sent anyway is ignored:
//	                                OpenAI SDKs always send one)
//	loopback, "Require a key" on:   the key
//	LAN:                            the key; or the PIN for consumption routes
func (s *Server) authorize(lan bool, r *http.Request, acc access) error {
	if s.hasKey(r) {
		return nil
	}
	if !lan {
		if !s.b.Settings().API.RequireKey {
			return nil
		}
		return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthorized,
			Message: "This AudioSmith asks for its API key: send Authorization: Bearer <key> (Settings → Local API)."}
	}
	st := s.share.Load()
	if st == nil { // sharing just stopped
		return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "LAN sharing is off."}
	}
	ip := remoteIP(r)
	if no, wait := st.guard.blocked(ip); no {
		return &Error{Status: http.StatusTooManyRequests, Code: CodeTooManyAttempts, RetryAfter: seconds(wait),
			Message: "Too many wrong PINs or keys from this device: wait a few minutes."}
	}
	pin := r.Header.Get(HeaderPIN)
	if pin == "" {
		pin = r.URL.Query().Get("pin")
	}
	if pin == "" {
		if c, err := r.Cookie(pinCookie); err == nil {
			pin = c.Value
		}
	}
	if pin != "" {
		if locked, until := st.guard.locked(); locked {
			// Guessed at from many addresses: the PIN rests (code review
			// 11-02 L1, 11-03 M1). The key still works.
			return &Error{Status: http.StatusTooManyRequests, Code: CodeTooManyAttempts, RetryAfter: seconds(time.Until(until)),
				Message: "Too many wrong PINs from several devices: the PIN works again later, or make a new PIN in AudioSmith (Settings → Local API); the API key still works."}
		}
		if subtle.ConstantTimeCompare([]byte(pin), []byte(st.PIN)) == 1 {
			if acc == accessConsume {
				return nil
			}
			return &Error{Status: http.StatusForbidden, Code: CodeUnauthorized,
				Message: "The sharing PIN covers speech, transcription and reading voices; this needs the API key."}
		}
		st.guard.fail(ip, true, pin)
	}
	if k := bearer(r); k != "" {
		st.guard.fail(ip, false, k) // counts toward this address's limit, not the PIN's
	}
	return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthorized,
		Message: "Send the sharing PIN (" + HeaderPIN + " header or ?pin=) or the API key (Authorization: Bearer)."}
}

// seconds is d in whole seconds for Retry-After (at least 1).
func seconds(d time.Duration) int { return max(1, int((d+time.Second-1)/time.Second)) }

func remoteIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// NewKey makes an API key: "as-" and 40 random letters and digits.
func NewKey() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 40)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return "as-" + string(b)
}

// statusWriter records the status of a response (the operations log's
// api.call line); Flush and Unwrap keep streaming and hijacking working.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
