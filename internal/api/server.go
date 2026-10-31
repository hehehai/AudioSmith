package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// Options configure a Server. Backend, Sched and Bus are required.
type Options struct {
	Backend Backend
	// Sched is the app's job queue: the API follows, cancels and lists
	// its jobs; Bus carries their events (jobs.Updated).
	Sched jobs.Scheduler
	Bus   *core.Bus
	// Version is the app's version, for /health and the spec.
	Version string
	// Logf reports server errors; nil = log.Printf.
	Logf func(format string, args ...any)
	// Seen is told when a client's request passed the checks (not the
	// public routes: health, discovery, the spec), at most once a second;
	// nil = nobody (the Developers page's 「最近一次请求」).
	Seen func(at time.Time)
}

// Server is the Local API. Its handlers are goroutine-safe; Start and
// Stop are called by the app (one goroutine at a time).
type Server struct {
	o     Options
	b     Backend
	sched jobs.Scheduler
	w     *watcher
	mux   *http.ServeMux
	// access classifies each route pattern for the guard.
	access map[string]access

	last  atomic.Int64 // unix nanoseconds of the last request Seen was told of
	mcp   atomic.Pointer[http.Handler]
	key   atomic.Pointer[string]
	share atomic.Pointer[shareState]
	fails *failLimiter

	mu   sync.Mutex
	loop *listener
	lan  *listener
}

// access is who may call a route.
type access int

const (
	accessPublic  access = iota // anyone the Host/Origin checks let in: health, discovery, the spec
	accessConsume               // generation, transcription, reading voices: LAN clients with the PIN too
	accessAdmin                 // changes and the file system: loopback (or the key) only
)

// New makes a server; it listens once Start is called.
func New(o Options) *Server {
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	s := &Server{o: o, b: o.Backend, sched: o.Sched, w: newWatcher(o.Bus), mux: http.NewServeMux(),
		access: map[string]access{}, fails: newFailLimiter(10, time.Minute)}
	s.routes()
	return s
}

// handle registers a route.
func (s *Server) handle(pattern string, a access, h http.HandlerFunc) {
	s.access[pattern] = a
	s.mux.HandleFunc(pattern, h)
}

// LastRequest is when a client's request last passed the checks (zero:
// none since the start).
func (s *Server) LastRequest() time.Time {
	if n := s.last.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return time.Time{}
}

// seen records a request that passed the checks.
func (s *Server) seen() {
	now := time.Now()
	prev := s.last.Load()
	if prev != 0 && now.UnixNano()-prev < int64(time.Second) {
		return
	}
	if s.last.CompareAndSwap(prev, now.UnixNano()) && s.o.Seen != nil {
		s.o.Seen(now)
	}
}

// MountMCP serves h at /mcp (the MCP server, internal/mcp).
func (s *Server) MountMCP(h http.Handler) { s.mcp.Store(&h) }

// SetKey sets the API key clients may send as a Bearer token; "" = none.
func (s *Server) SetKey(key string) { s.key.Store(&key) }

// Key is the API key in use ("" = none).
func (s *Server) Key() string {
	if k := s.key.Load(); k != nil {
		return *k
	}
	return ""
}

// Handler is the loopback listener's handler (guard included), for tests.
func (s *Server) Handler() http.Handler { return s.guard(false, 0) }

// LANHandler is the LAN listener's handler, for tests.
func (s *Server) LANHandler(port int) http.Handler { return s.guard(true, port) }

// listener is one running http.Server.
type listener struct {
	srv  *http.Server
	ln   net.Listener
	port int
	done chan struct{}
}

func (s *Server) serve(ln net.Listener, h http.Handler) *listener {
	l := &listener{ln: ln, port: ln.Addr().(*net.TCPAddr).Port, done: make(chan struct{}),
		srv: &http.Server{Handler: h, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute,
			ErrorLog: log.New(logWriter(s.o.Logf), "api: ", 0)}}
	go func() {
		defer close(l.done)
		if err := l.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.o.Logf("api: %v", err)
		}
	}()
	return l
}

func (l *listener) stop(ctx context.Context) {
	if l == nil {
		return
	}
	if err := l.srv.Shutdown(ctx); err != nil {
		l.srv.Close()
	}
	<-l.done
}

type logWriter func(string, ...any)

func (f logWriter) Write(p []byte) (int, error) {
	f("%s", string(p))
	return len(p), nil
}

// ErrPortInUse says another program listens on the port.
var ErrPortInUse = errors.New("the port is in use")

// Start listens on 127.0.0.1:port (0 picks a free port, for tests). It
// stops a listener already running first.
func (s *Server) Start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loop != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.loop.stop(ctx)
		cancel()
		s.loop = nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("127.0.0.1:%d: %w", port, ErrPortInUse)
		}
		return err
	}
	s.loop = s.serve(ln, s.guard(false, 0))
	return nil
}

// Port is the loopback port listened on (0 = not running).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loop == nil {
		return 0
	}
	return s.loop.port
}

// Stop closes every listener (requests in flight get a few seconds) and
// stops following jobs.
func (s *Server) Stop(ctx context.Context) {
	s.mu.Lock()
	loop, lan := s.loop, s.lan
	s.loop, s.lan = nil, nil
	s.share.Store(nil)
	s.mu.Unlock()
	lan.stop(ctx)
	loop.stop(ctx)
}

// Close stops the server for good.
func (s *Server) Close(ctx context.Context) {
	s.Stop(ctx)
	s.w.close()
}

func isAddrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }

// --- LAN sharing ----------------------------------------------------------

// shareState is LAN sharing while it is on: its PIN (never saved) and the
// addresses other devices reach it at.
type shareState struct {
	PIN   string
	Port  int
	Hosts []string // IPs and names the LAN listener answers to
	URLs  []string // http://<ip>:<port>
}

// Share describes LAN sharing for the UI.
type Share struct {
	PIN  string
	Port int
	URLs []string
}

// StartLAN opens the LAN listener on every interface at port with a new
// PIN, and returns how to reach it.
func (s *Server) StartLAN(port int) (Share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lan != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.lan.stop(ctx)
		cancel()
		s.lan = nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		if isAddrInUse(err) {
			return Share{}, fmt.Errorf("port %d: %w", port, ErrPortInUse)
		}
		return Share{}, err
	}
	port = ln.Addr().(*net.TCPAddr).Port
	st := &shareState{PIN: newPIN(), Port: port}
	for _, ip := range lanAddresses() {
		st.Hosts = append(st.Hosts, ip)
		st.URLs = append(st.URLs, "http://"+net.JoinHostPort(ip, strconv.Itoa(port)))
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		st.Hosts = append(st.Hosts, h)
		if filepath.Ext(h) != ".local" {
			st.Hosts = append(st.Hosts, h+".local")
		}
	}
	s.share.Store(st)
	s.lan = s.serve(ln, s.guard(true, port))
	return Share{PIN: st.PIN, Port: port, URLs: st.URLs}, nil
}

// StopLAN closes the LAN listener; the PIN is forgotten.
func (s *Server) StopLAN() {
	s.mu.Lock()
	lan := s.lan
	s.lan = nil
	s.share.Store(nil)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	lan.stop(ctx)
}

// Sharing reports LAN sharing's state (ok false when off).
func (s *Server) Sharing() (Share, bool) {
	st := s.share.Load()
	if st == nil {
		return Share{}, false
	}
	return Share{PIN: st.PIN, Port: st.Port, URLs: st.URLs}, true
}

// newPIN is a random 6-digit PIN.
func newPIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// lanAddresses are the machine's private IPv4 addresses on interfaces
// that are up.
func lanAddresses() []string {
	var out []string
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
				out = append(out, ip.String())
			}
		}
	}
	return out
}

// --- the port file --------------------------------------------------------

// PortFile is what the running app writes to <data>/api.json so that the
// MCP stdio shim (AudioSmith --mcp-stdio) and scripts find the API.
type PortFile struct {
	URL     string    `json:"url"` // http://127.0.0.1:3901
	Port    int       `json:"port"`
	PID     int       `json:"pid"`
	Version string    `json:"version,omitempty"`
	Started time.Time `json:"started"`
}

// PortFileName is the port file's name in the data folder.
const PortFileName = "api.json"

// WritePortFile writes the port file atomically.
func WritePortFile(path string, p PortFile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadPortFile reads the port file.
func ReadPortFile(path string) (PortFile, error) {
	var p PortFile
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.URL == "" && p.Port > 0 {
		p.URL = "http://127.0.0.1:" + strconv.Itoa(p.Port)
	}
	return p, nil
}
