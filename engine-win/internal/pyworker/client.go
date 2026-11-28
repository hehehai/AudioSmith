// Package pyworker manages the optional on-demand Python/PyTorch subprocess
// worker on Windows, speaking protocol-v2 NDJSON over stdin/stdout for
// Chinese/Japanese neural TTS models (Qwen3-TTS, Irodori-TTS v4.1).
//
// Everyday iGPU/CPU PCs never start or download this worker unless the user
// explicitly opts into a PyTorch-only model.
package pyworker

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

//go:embed worker.py
var embeddedWorkerScript []byte

// ErrNotInstalled is returned when no Python executable is configured.
var ErrNotInstalled = errors.New("pyworker: optional Python runtime is not installed")

// Config configures a Python worker subprocess.
type Config struct {
	PythonPath string   // path to python(.exe)
	ScriptPath string   // optional; defaults to embedded worker.py
	Device     string   // optional device override ("cuda", "directml", "cpu")
	Env        []string // extra environment variables
}

// HelloResult is returned by the worker's engine.hello handshake.
type HelloResult struct {
	Protocol int      `json:"protocol"`
	Role     string   `json:"role"`
	Version  string   `json:"version"`
	Device   string   `json:"device"`
	Families []string `json:"families"`
}

// NotifyFunc receives protocol-v2 notifications (e.g. job.progress, tts.chunk)
// emitted by the worker while a request is in flight.
type NotifyFunc func(method string, params json.RawMessage)

// Client manages a single persistent Python worker subprocess.
type Client struct {
	cfg Config

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	stderr     *tailBuffer
	tempScript string
	hello      HelloResult
	started    bool
	seq        atomic.Uint64
}

// New creates an idle Client; the subprocess is started lazily on first Call.
func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// Device returns the device selected by the worker ("cuda", "directml", "cpu")
// after Start/Call, or "" before the worker has started.
func (c *Client) Device() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello.Device
}

// Hello returns the last engine.hello handshake result.
func (c *Client) Hello() HelloResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello
}

// Start launches the Python worker subprocess if not already running and
// completes the protocol-v2 engine.hello handshake.
func (c *Client) Start(ctx context.Context) (HelloResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.startLocked(ctx); err != nil {
		return HelloResult{}, err
	}
	return c.hello, nil
}

// Call sends one protocol-v2 request and waits for its matching response,
// forwarding any interleaved notifications to notify (when non-nil).
func (c *Client) Call(ctx context.Context, method string, params, result any, notify NotifyFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.startLocked(ctx); err != nil {
		return err
	}
	err := c.callLocked(ctx, method, params, result, notify)
	if err != nil {
		// If the transport broke or context cancelled mid-read, reset the
		// subprocess so the next Call gets a clean worker.
		var pe *proto.Error
		if !errors.As(err, &pe) {
			c.stopLocked()
		}
	}
	return err
}

// Close shuts down the worker cleanly and removes any temporary script.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.callLocked(ctx, proto.MethodShutdown, nil, nil, nil)
		cancel()
	}
	c.stopLocked()
	return nil
}

func (c *Client) startLocked(ctx context.Context) error {
	if c.started {
		return nil
	}
	if strings.TrimSpace(c.cfg.PythonPath) == "" {
		return ErrNotInstalled
	}
	scriptPath := c.cfg.ScriptPath
	if scriptPath == "" {
		tmp, err := os.CreateTemp("", "audiosmith-pyworker-*.py")
		if err != nil {
			return fmt.Errorf("pyworker: create temp script: %w", err)
		}
		if _, err := tmp.Write(embeddedWorkerScript); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return fmt.Errorf("pyworker: write temp script: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmp.Name())
			return fmt.Errorf("pyworker: close temp script: %w", err)
		}
		scriptPath = tmp.Name()
		c.tempScript = scriptPath
	}

	cmd := exec.Command(c.cfg.PythonPath, "-u", scriptPath)
	env := append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONIOENCODING=utf-8")
	if c.cfg.Device != "" {
		env = append(env, "AUDIOSMITH_PY_DEVICE="+c.cfg.Device)
	}
	env = append(env, c.cfg.Env...)
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.cleanupTempLocked()
		return fmt.Errorf("pyworker: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		c.cleanupTempLocked()
		return fmt.Errorf("pyworker: stdout pipe: %w", err)
	}
	stderrBuf := newTailBuffer(4096)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdoutPipe.Close()
		c.cleanupTempLocked()
		return fmt.Errorf("pyworker: start %s: %w", c.cfg.PythonPath, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = bufio.NewReaderSize(stdoutPipe, 64*1024)
	c.stderr = stderrBuf
	c.started = true

	var hello HelloResult
	if err := c.callLocked(ctx, proto.MethodHello, map[string]any{"protocol": proto.Version}, &hello, nil); err != nil {
		c.stopLocked()
		return fmt.Errorf("pyworker: hello failed: %w", err)
	}
	if hello.Protocol != proto.Version {
		c.stopLocked()
		return fmt.Errorf("pyworker: protocol mismatch: got %d, want %d", hello.Protocol, proto.Version)
	}
	c.hello = hello
	return nil
}

func (c *Client) callLocked(ctx context.Context, method string, params, result any, notify NotifyFunc) error {
	if !c.started {
		return errors.New("pyworker: worker not started")
	}
	reqID := fmt.Sprintf("py-%d", c.seq.Add(1))
	req, err := proto.NewRequest(reqID, method, params)
	if err != nil {
		return fmt.Errorf("pyworker: encode %s: %w", method, err)
	}
	wire, err := proto.Encode(req)
	if err != nil {
		return fmt.Errorf("pyworker: marshal %s: %w", method, err)
	}
	if _, err := c.stdin.Write(wire); err != nil {
		return c.wrapIOErr("write "+method, err)
	}

	type readRes struct {
		line []byte
		err  error
	}
	for {
		ch := make(chan readRes, 1)
		go func() {
			line, rerr := c.stdout.ReadBytes('\n')
			ch <- readRes{line: line, err: rerr}
		}()

		var res readRes
		select {
		case <-ctx.Done():
			return ctx.Err()
		case res = <-ch:
		}
		if res.err != nil {
			return c.wrapIOErr("read "+method, res.err)
		}
		trimmed := bytes.TrimSpace(res.line)
		if len(trimmed) == 0 {
			continue
		}
		msg, err := proto.Decode(trimmed)
		if err != nil {
			return fmt.Errorf("pyworker: decode message: %w", err)
		}
		switch msg.Kind() {
		case proto.KindNotification:
			if notify != nil {
				notify(msg.Method, msg.Params)
			}
		case proto.KindResponse:
			if msg.ID != reqID {
				continue
			}
			if msg.Error != nil {
				return msg.Error
			}
			if result != nil {
				if err := msg.DecodeResult(result); err != nil {
					return fmt.Errorf("pyworker: decode %s result: %w", method, err)
				}
			}
			return nil
		}
	}
}

func (c *Client) wrapIOErr(op string, err error) error {
	tail := ""
	if c.stderr != nil {
		tail = strings.TrimSpace(c.stderr.String())
	}
	if tail != "" {
		return fmt.Errorf("pyworker: %s: %w (stderr: %s)", op, err, tail)
	}
	return fmt.Errorf("pyworker: %s: %w", op, err)
}

func (c *Client) stopLocked() {
	if c.stdin != nil {
		_ = c.stdin.Close()
		c.stdin = nil
	}
	if c.cmd != nil {
		done := make(chan struct{})
		go func(cmd *exec.Cmd) {
			_ = cmd.Wait()
			close(done)
		}(c.cmd)
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			if c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
			<-done
		}
		c.cmd = nil
	}
	c.stdout = nil
	c.started = false
	c.cleanupTempLocked()
}

func (c *Client) cleanupTempLocked() {
	if c.tempScript != "" {
		_ = os.Remove(c.tempScript)
		c.tempScript = ""
	}
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
