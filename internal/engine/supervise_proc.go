package engine

// One engine process (proc): starting it, the handshake, reading its
// lines and logs, writing requests and waiting for their replies, and
// killing it; lineTail keeps its last stderr lines.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/logs"
)

func (s *supervisor) startProcess(w *worker, bin string) (*proc, error) {
	cmd := exec.Command(bin, "--role", string(w.role))
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = append(os.Environ(), s.cfg.Env...)
	// stdin is our own pipe, not StdinPipe's wrapper: its *os.File takes
	// write deadlines (write).
	stdinR, stdin, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdin = stdinR
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdinR.Close()
		stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdinR.Close()
		stdin.Close()
		return nil, err
	}
	err = cmd.Start()
	stdinR.Close() // the child has its copy
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	p := &proc{s: s, w: w, cmd: cmd, stdin: stdin, tail: newLineTail(40), exited: make(chan struct{}),
		pending: map[string]*call{}, pollNow: make(chan struct{}, 1), writeSem: make(chan struct{}, 1)}
	var logFile *rotatingLog
	if s.cfg.LogDir != "" {
		logFile = openLog(filepath.Join(s.cfg.LogDir, "engine-"+string(w.role)+".log"), 0)
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			p.tail.add(line)
			if logFile != nil {
				logFile.line(logs.Redact(line)) // #100 L3: the file goes into diagnostic bundles
			}
			if strings.TrimSpace(line) != "" {
				s.emit(core.LogEntry{Time: time.Now(), Level: stderrLevel(line), Source: "engine:" + string(w.role), Message: line})
			}
		}
	}()
	go func() {
		p.read(stdout)
		<-stderrDone
		err := cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}
		s.exited(p, err)
	}()
	return p, nil
}

func (s *supervisor) handshake(w *worker, p *proc) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HelloTimeout)
	defer cancel()
	s.mu.Lock()
	cacheDir := s.cfg.CacheDir
	s.mu.Unlock()
	raw, err := p.call(ctx, "", proto.MethodHello, proto.HelloParams{
		Protocol: proto.Version, App: s.cfg.AppVersion, Role: w.role,
		CacheDir: cacheDir, Offline: s.cfg.Offline, LogLevel: "info",
	}, nil, false)
	var hello proto.HelloResult
	if err == nil {
		err = json.Unmarshal(raw, &hello)
	}
	var perr *proto.Error
	switch {
	case p.isClosed():
		return // exited (legacy engine, crash): exited reports it
	case errors.Is(err, context.DeadlineExceeded):
		p.killFor(FailHandshake, fmt.Sprintf("the engine did not answer engine.hello within %s", s.cfg.HelloTimeout), false)
		return
	case errors.As(err, &perr) && perr.Code == proto.CodeProtocolMismatch:
		p.killFor(FailProtocol, "engine version mismatch: rebuild audiosmith-engine ("+perr.Message+")", true)
		return
	case err != nil:
		p.killFor(FailHandshake, "engine.hello failed: "+err.Error(), false)
		return
	case hello.Protocol != proto.Version || hello.Role != w.role:
		p.killFor(FailProtocol, fmt.Sprintf("engine version mismatch: rebuild audiosmith-engine (it speaks protocol %d as role %q; want %d as %q)",
			hello.Protocol, hello.Role, proto.Version, w.role), true)
		return
	}
	p.helloDone.Store(true)
	s.mu.Lock()
	if w.proc != p {
		s.mu.Unlock()
		return
	}
	w.status.State, w.status.Since, w.status.Hello = WorkerReady, time.Now(), &hello
	if hello.PID != 0 {
		w.status.PID = hello.PID
	}
	if !isClosed(w.ready) {
		close(w.ready)
	}
	st := s.statusLocked(w)
	s.mu.Unlock()
	s.publish(st)
	if s.cfg.StatsEvery > 0 {
		go s.pollStats(w, p)
	}
}

// read routes the engine's stdout: responses to their calls, a job's
// notifications to its call's notify, log lines to the bus.
func (p *proc) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			p.handleLine(line)
		}
		if err != nil {
			if err != io.EOF {
				p.tail.add("stdout: " + err.Error())
			}
			// Drain, so the process is not blocked writing.
			io.Copy(io.Discard, r)
			return
		}
	}
}

// readLine reads one line of at most proto.MaxLine bytes; a longer one is
// skipped and reported.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(buf)+len(chunk) <= proto.MaxLine {
			buf = append(buf, chunk...)
		} else {
			buf = buf[:0:0]
		}
		if err != nil || !isPrefix {
			return buf, err
		}
	}
}

func (p *proc) handleLine(line []byte) {
	if !p.helloDone.Load() {
		if v, ok := proto.LegacyReady(line); ok {
			p.killFor(FailProtocol, fmt.Sprintf("the engine speaks protocol %d: rebuild audiosmith-engine", v), true)
			return
		}
	}
	m, err := proto.Decode(line)
	if err != nil {
		p.tail.add("stdout: " + string(line))
		return
	}
	switch m.Kind() {
	case proto.KindResponse:
		p.mu.Lock()
		c := p.pending[m.ID]
		delete(p.pending, m.ID)
		p.mu.Unlock()
		if c != nil {
			c.resp = m
			close(c.done)
			if c.job || c.method == proto.MethodModelsUnload {
				p.poll() // the resident models may have changed
			}
		}
	case proto.KindNotification:
		var ref struct {
			Job string `json:"job"`
		}
		m.DecodeParams(&ref)
		if m.Method == proto.MethodLog {
			p.log(m)
		}
		if ref.Job == "" {
			return
		}
		p.mu.Lock()
		c := p.pending[ref.Job]
		p.mu.Unlock()
		if c != nil && c.notify != nil {
			c.notify(m)
		}
	}
}

func (p *proc) log(m proto.Message) {
	var lp proto.LogParams
	if m.DecodeParams(&lp) != nil {
		return
	}
	level := core.Severity(lp.Level)
	switch level {
	case core.SeverityDebug, core.SeverityInfo, core.SeverityWarning, core.SeverityError:
	default:
		level = core.SeverityInfo
	}
	p.s.emit(core.LogEntry{Time: time.Now(), Level: level, Source: "engine:" + string(p.w.role), Message: lp.Message, JobID: lp.Job})
}

// emit hands a log line to Config.Log, or the bus.
func (s *supervisor) emit(e core.LogEntry) {
	if s.cfg.Log != nil {
		s.cfg.Log(e)
		return
	}
	s.cfg.Bus.Publish(core.LogAppended{Entry: e})
}

// stderrLevel guesses the level of a free-form stderr line.
func stderrLevel(line string) core.Severity {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "fatal") || strings.Contains(l, "error"):
		return core.SeverityError
	case strings.Contains(l, "warning"):
		return core.SeverityWarning
	}
	return core.SeverityInfo
}

func (p *proc) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *proc) running() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []string
	for id, c := range p.pending {
		if c.job {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (p *proc) jobCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.pending {
		if c.job {
			n++
		}
	}
	return n
}

func (p *proc) poll() {
	select {
	case p.pollNow <- struct{}{}:
	default:
	}
}

// killFor kills the process for a reason of the supervisor's; exited
// reports it (and restarts it unless noRestart).
func (p *proc) killFor(reason FailReason, msg string, noRestart bool) {
	p.mu.Lock()
	if p.failReason == "" {
		p.failReason, p.failErr, p.noRestart = reason, msg, noRestart
	}
	p.mu.Unlock()
	p.kill()
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
}

// write sends one request line. It heeds ctx while it waits for the
// writes before it and while it writes, and gives up after WriteTimeout:
// an engine that stops reading its stdin (stuck, or suspended) would
// otherwise block every later request, job.cancel and engine.shutdown
// included (#101 M5). A write that stopped part-way, or that timed out,
// leaves the stream unusable: the process is killed (FailStuck) and
// restarted.
func (p *proc) write(ctx context.Context, m proto.Message) error {
	line, err := proto.Encode(m)
	if err != nil {
		return err
	}
	if len(line) > proto.MaxLine {
		return fmt.Errorf("request of %d bytes is over the protocol's limit", len(line))
	}
	select {
	case p.writeSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.exited:
		return ErrWorkerExited
	}
	defer func() { <-p.writeSem }()
	f, ok := p.stdin.(interface{ SetWriteDeadline(time.Time) error })
	if !ok {
		if _, err := p.stdin.Write(line); err != nil {
			return fmt.Errorf("writing to the engine: %v: %w", err, ErrWorkerExited)
		}
		return nil
	}
	dl := time.Now().Add(p.s.cfg.WriteTimeout)
	f.SetWriteDeadline(dl)
	// ctx ending stops the write at once; wait for that to be done
	// before the next write sets its own deadline.
	expired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(expired)
		f.SetWriteDeadline(time.Now())
	})
	n, err := p.stdin.Write(line)
	if !stop() {
		<-expired
	}
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("writing to the engine: %v: %w", err, ErrWorkerExited)
	}
	if n == 0 && ctx.Err() != nil {
		return ctx.Err() // nothing written: the stream is intact
	}
	msg := fmt.Sprintf("the engine stopped reading its requests (%s took over %s)", m.Method, p.s.cfg.WriteTimeout)
	if ctx.Err() != nil {
		msg = fmt.Sprintf("a request (%s) was cut off part-way", m.Method)
	}
	p.s.cfg.Logf("engine %s: %s: restarting the worker", p.w.role, msg)
	p.killFor(FailStuck, msg, false)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("writing to the engine: %s: %w", msg, ErrWorkerExited)
}

// call sends a request and waits for its response (see Supervisor.Call).
func (p *proc) call(ctx context.Context, id, method string, params any, notify func(proto.Message), job bool) (json.RawMessage, error) {
	if id == "" {
		id = fmt.Sprintf("c%d", p.s.ctrlID.Add(1))
	}
	req, err := proto.NewRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("%s params: %w", method, err)
	}
	c := &call{method: method, job: job, notify: notify, done: make(chan struct{})}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrWorkerExited
	}
	if _, dup := p.pending[id]; dup {
		p.mu.Unlock()
		return nil, fmt.Errorf("request id %s is already in flight: %w", id, core.ErrInvalid)
	}
	p.pending[id] = c
	p.mu.Unlock()
	if err := p.write(ctx, req); err != nil {
		p.drop(id)
		return nil, err
	}
	if job {
		p.poll()
	}

	select {
	case <-c.done:
		return c.result()
	case <-ctx.Done():
	}
	if !job {
		p.drop(id)
		return nil, ctx.Err()
	}
	// Ask the engine to stop the job; it answers the job with "cancelled".
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), p.s.cfg.CancelGrace)
		defer cancel()
		p.call(cctx, "", proto.MethodJobCancel, proto.CancelParams{Job: id}, nil, false)
	}()
	grace := time.NewTimer(p.s.cfg.CancelGrace)
	defer grace.Stop()
	select {
	case <-c.done:
	case <-grace.C:
		p.s.cfg.Logf("engine %s: job %s did not stop within %s of being cancelled: restarting the worker", p.w.role, id, p.s.cfg.CancelGrace)
		p.killFor(FailStuck, fmt.Sprintf("job %s did not stop within %s of being cancelled", id, p.s.cfg.CancelGrace), false)
		<-c.done
	}
	return c.result()
}

func (p *proc) drop(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// lineTail keeps the last lines written to it.
type lineTail struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func newLineTail(max int) *lineTail { return &lineTail{max: max} }

func (t *lineTail) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}
