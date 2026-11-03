package api

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// bodyIdle is how long a request body may stall: a client that stops
// sending (or trickles) is dropped (code review 11-02: the server had
// only a header timeout). A whole-request ReadTimeout or a WriteTimeout
// would cut long uploads, SSE, WebSockets and MCP streams, so the
// deadline moves with each read and is lifted once the body is read.
var bodyIdle = 60 * time.Second // a variable for tests

// bodyDeadline arms the idle deadline on r's body, if it has one, and
// returns the writer to answer through: the deadline is lifted at the
// body's end or, at the latest, when the answer starts (net/http then
// reads the rest of the body, and a deadline left behind would cancel a
// long answer's context).
func bodyDeadline(w http.ResponseWriter, r *http.Request) http.ResponseWriter {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return w
	}
	rc := http.NewResponseController(w)
	if rc.SetReadDeadline(time.Now().Add(bodyIdle)) != nil {
		return w // not supported (tests' recorders)
	}
	b := &idleBody{ReadCloser: r.Body, rc: rc}
	r.Body = b
	return &liftWriter{ResponseWriter: w, b: b}
}

// liftWriter lifts its body's deadline when the answer starts.
type liftWriter struct {
	http.ResponseWriter
	b *idleBody
}

func (w *liftWriter) WriteHeader(code int) {
	w.b.lift()
	w.ResponseWriter.WriteHeader(code)
}

func (w *liftWriter) Write(p []byte) (int, error) {
	w.b.lift()
	return w.ResponseWriter.Write(p)
}

func (w *liftWriter) Flush() {
	w.b.lift()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *liftWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// idleBody pushes the read deadline back before each read and lifts it
// at the end of the body. After a read error the deadline stays, so
// the server drops the connection rather than wait for the rest.
type idleBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	mu   sync.Mutex // a handler may read and answer on two goroutines
	done bool
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if !b.done {
		b.rc.SetReadDeadline(time.Now().Add(bodyIdle))
	}
	b.mu.Unlock()
	n, err := b.ReadCloser.Read(p)
	switch {
	case err == io.EOF:
		b.lift() // read whole
	case err != nil:
		// The deadline stays, so the server drops the connection
		// rather than wait for the rest of the body.
		b.mu.Lock()
		b.done = true
		b.mu.Unlock()
	}
	return n, err
}

func (b *idleBody) lift() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		b.done = true
		b.rc.SetReadDeadline(time.Time{})
	}
}
