package mcp

// The stdio shim: MCP clients that only start servers as commands
// (stdio) run "AudioSmith --mcp-stdio", which relays their JSON-RPC lines
// to the running app's /mcp (Streamable HTTP) and its answers back. The
// app is found through the port file it writes (<data>/api.json). If the
// app is not running, requests are answered with an error that says so;
// if it restarts, the shim opens a new session for the client by sending
// its initialize again.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
)

// ProxyOptions configure Proxy.
type ProxyOptions struct {
	// URL is the API's base URL; "" reads PortFile each time it is needed.
	URL      string
	PortFile string
	// Key is the API key (AUDIOSMITH_API_KEY), sent as a Bearer token.
	Key string
	// Client names the agent (X-AudioSmith-Client) for its bound voice.
	Client string
	// HTTP is the client; nil = one without a timeout (tools run long).
	HTTP *http.Client
	// Log receives diagnostics (stderr); nil discards them.
	Log io.Writer
}

// ErrNotRunning says the app's API could not be reached.
var ErrNotRunning = errors.New("AudioSmith is not running: open AudioSmith (with Settings → Local API on), then try again")

type proxy struct {
	o       ProxyOptions
	out     io.Writer
	outMu   sync.Mutex
	mu      sync.Mutex
	session string
	version string
	initMsg []byte // the client's initialize, to open a new session after a restart
	wg      sync.WaitGroup
}

// Proxy relays MCP messages between a stdio client (in, out) and the app
// until in ends.
func Proxy(ctx context.Context, in io.Reader, out io.Writer, o ProxyOptions) error {
	if o.HTTP == nil {
		o.HTTP = &http.Client{}
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	p := &proxy{o: o, out: out}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.Unmarshal(msg, &head)
		if head.Method == "initialize" || len(head.ID) == 0 {
			// The handshake and notifications go in order.
			if head.Method == "initialize" {
				p.mu.Lock()
				p.initMsg = msg
				p.session, p.version = "", ""
				p.mu.Unlock()
			}
			p.relay(ctx, msg, head.ID, head.Method)
			continue
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.relay(ctx, msg, head.ID, head.Method)
		}()
	}
	p.wg.Wait()
	p.closeSession()
	return sc.Err()
}

// base is the API's URL now.
func (p *proxy) base() (string, error) {
	if p.o.URL != "" {
		return strings.TrimRight(p.o.URL, "/"), nil
	}
	pf, err := api.ReadPortFile(p.o.PortFile)
	if err != nil {
		return "", ErrNotRunning
	}
	return strings.TrimRight(pf.URL, "/"), nil
}

// relay sends one message and writes back what comes.
func (p *proxy) relay(ctx context.Context, msg, id json.RawMessage, method string) {
	err := p.send(ctx, msg, method, true)
	if err != nil {
		fmt.Fprintf(p.o.Log, "audiosmith mcp: %s: %v\n", method, err)
		if len(id) > 0 {
			p.write(errorResponse(id, err))
		}
	}
}

type sessionGoneError struct{}

func (sessionGoneError) Error() string { return "the MCP session ended (AudioSmith restarted)" }

func (p *proxy) send(ctx context.Context, msg []byte, method string, retry bool) error {
	base, err := p.base()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mcp", bytes.NewReader(msg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	p.headers(req)
	resp, err := p.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		p.mu.Lock()
		p.session = sid
		p.mu.Unlock()
	}
	if resp.StatusCode == http.StatusNotFound && method != "initialize" && retry && p.reopen(ctx) {
		return p.send(ctx, msg, method, false)
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		return p.readSSE(resp.Body, method)
	case strings.HasPrefix(ct, "application/json"):
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode >= 400 && !isJSONRPC(body) {
			return fmt.Errorf("%s: %s", resp.Status, apiMessage(body))
		}
		p.deliver(body, method)
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *proxy) headers(req *http.Request) {
	p.mu.Lock()
	sid, ver := p.session, p.version
	p.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if ver != "" {
		req.Header.Set("Mcp-Protocol-Version", ver)
	}
	if p.o.Key != "" {
		req.Header.Set("Authorization", "Bearer "+p.o.Key)
	}
	if p.o.Client != "" {
		req.Header.Set(api.HeaderClient, p.o.Client)
	}
	req.Header.Set(HeaderTransport, "stdio") // it ends its session (sessions.go)
}

// reopen opens a new session with the client's initialize (its answer is
// not passed on: the client has had one).
func (p *proxy) reopen(ctx context.Context) bool {
	p.mu.Lock()
	init := p.initMsg
	p.session, p.version = "", ""
	p.mu.Unlock()
	if init == nil {
		return false
	}
	if err := p.sendQuiet(ctx, init); err != nil {
		fmt.Fprintf(p.o.Log, "audiosmith mcp: reopening the session: %v\n", err)
		return false
	}
	note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	p.sendQuiet(ctx, note)
	return true
}

// sendQuiet sends a message and keeps only the session id and version.
func (p *proxy) sendQuiet(ctx context.Context, msg []byte) error {
	base, err := p.base()
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mcp", bytes.NewReader(msg))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	p.headers(req)
	resp, err := p.o.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		p.mu.Lock()
		p.session = sid
		p.mu.Unlock()
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for _, ev := range sseData(body) {
			p.noteVersion(ev)
		}
	} else {
		p.noteVersion(body)
	}
	if resp.StatusCode >= 400 {
		return errors.New(resp.Status)
	}
	return nil
}

// readSSE passes each event's data on as a line.
func (p *proxy) readSSE(r io.Reader, method string) error {
	br := bufio.NewReader(r)
	var data bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if d, ok := strings.CutPrefix(trimmed, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(d, " "))
		} else if trimmed == "" && data.Len() > 0 {
			p.deliver(data.Bytes(), method)
			data.Reset()
		}
		if err != nil {
			if data.Len() > 0 {
				p.deliver(data.Bytes(), method)
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func sseData(body []byte) [][]byte {
	var out [][]byte
	for _, ev := range strings.Split(string(body), "\n\n") {
		for _, l := range strings.Split(ev, "\n") {
			if d, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "data:"); ok {
				out = append(out, []byte(strings.TrimPrefix(d, " ")))
			}
		}
	}
	return out
}

// deliver writes a message (or a batch) to the client as one line.
func (p *proxy) deliver(body []byte, method string) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return
	}
	if method == "initialize" {
		p.noteVersion(body)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) == nil {
		body = compact.Bytes()
	}
	p.write(body)
}

// noteVersion keeps the protocol version an initialize result names.
func (p *proxy) noteVersion(body []byte) {
	var r struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &r) == nil && r.Result.ProtocolVersion != "" {
		p.mu.Lock()
		p.version = r.Result.ProtocolVersion
		p.mu.Unlock()
	}
}

func (p *proxy) write(line []byte) {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	p.out.Write(append(append([]byte(nil), line...), '\n'))
}

// closeSession ends the session when the client goes.
func (p *proxy) closeSession() {
	p.mu.Lock()
	sid := p.session
	p.mu.Unlock()
	if sid == "" {
		return
	}
	base, err := p.base()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/mcp", nil)
	p.headers(req)
	if resp, err := p.o.HTTP.Do(req); err == nil {
		resp.Body.Close()
	}
}

func isJSONRPC(body []byte) bool {
	var m struct {
		JSONRPC string `json:"jsonrpc"`
	}
	return json.Unmarshal(body, &m) == nil && m.JSONRPC == "2.0"
}

// apiMessage is the message of an API error body.
func apiMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return strings.TrimSpace(string(body))
}

func errorResponse(id json.RawMessage, err error) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": err.Error()}})
	return b
}
