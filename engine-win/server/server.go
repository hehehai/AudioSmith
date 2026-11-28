// Package server implements AudioSmith's protocol-v2 JSON-RPC over stdio
// (docs/protocol-v2.md) for the Windows engine binary.
package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/hehehai/AudioSmith/engine-win/hw"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Additive Windows control methods (protocol-v2 allows additive methods).
const (
	MethodPing = "engine.ping"
	MethodInfo = "engine.info"
)

// PingResult answers engine.ping.
type PingResult struct {
	OK   bool       `json:"ok"`
	Role proto.Role `json:"role"`
	PID  int        `json:"pid"`
}

// InfoResult answers engine.info with the full iGPU-first hardware probe.
type InfoResult struct {
	Version  string     `json:"version"`
	Role     proto.Role `json:"role"`
	PID      int        `json:"pid"`
	Hardware hw.Info    `json:"hardware"`
}

// Notifier lets a handler emit protocol-v2 notifications (job.progress,
// tts.chunk, llm.token, capture.level, log) safely from any goroutine.
type Notifier interface {
	Notify(method string, params any) error
}

// Handler handles one protocol-v2 RPC method.
// Returning a *proto.Error sends that structured error code; any other error
// is wrapped as proto.CodeInternal (or proto.CodeCancelled if ctx was cancelled).
type Handler func(ctx context.Context, jobID string, params json.RawMessage, n Notifier) (any, error)

// RoleHooks provides optional role-specific hooks for stats, flush, config,
// and supported model families.
type RoleHooks struct {
	Concurrency int
	Families    []string
	Stats       func() (proto.Memory, []proto.LoadedModel)
	Flush       func()
	Configure   func(proto.EngineConfigParams) (proto.EngineConfigResult, error)
}

// Config configures a Server.
type Config struct {
	Role     proto.Role
	Version  string
	Hardware hw.Info
	Hooks    RoleHooks
	Logf     func(format string, args ...any)
}

// Server serves protocol-v2 NDJSON over an input reader and output writer.
type Server struct {
	cfg      Config
	handlers map[string]Handler

	writeMu sync.Mutex
	out     io.Writer

	mu          sync.Mutex
	initialized bool
	helloParams proto.HelloParams
	jobs        map[string]context.CancelFunc
	wg          sync.WaitGroup
}

// New constructs a Server for cfg.Register methods before calling Serve.
func New(cfg Config) *Server {
	if cfg.Role == "" {
		cfg.Role = proto.RoleGPU
	}
	if cfg.Version == "" {
		cfg.Version = "0.1.0-win"
	}
	if cfg.Hooks.Concurrency <= 0 {
		cfg.Hooks.Concurrency = 1
	}
	if cfg.Logf == nil {
		cfg.Logf = log.New(os.Stderr, "[audiosmith-engine] ", log.LstdFlags).Printf
	}
	return &Server{
		cfg:      cfg,
		handlers: map[string]Handler{},
		jobs:     map[string]context.CancelFunc{},
	}
}

// Handle registers a handler for method.
func (s *Server) Handle(method string, h Handler) {
	s.handlers[method] = h
}

// HelloParams returns the parameters received in engine.hello (after handshake).
func (s *Server) HelloParams() proto.HelloParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.helloParams
}

// Notify writes a protocol-v2 notification line to stdout.
func (s *Server) Notify(method string, params any) error {
	m, err := proto.NewNotification(method, params)
	if err != nil {
		return err
	}
	return s.writeMessage(m)
}

// Serve reads NDJSON requests from in and writes responses/notifications to
// out until engine.shutdown or EOF on in.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = out
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), proto.MaxLine)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := proto.Decode(line)
		if err != nil {
			s.cfg.Logf("invalid wire line: %v", err)
			continue
		}
		if msg.Kind() != proto.KindRequest {
			s.cfg.Logf("ignoring non-request message kind=%s", msg.Kind())
			continue
		}
		if s.dispatch(msg) {
			break // engine.shutdown requested
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.cancelAllJobs()
		s.wg.Wait()
		return err
	}
	s.cancelAllJobs()
	s.wg.Wait()
	return nil
}

// dispatch handles one request. Returns true if the server should exit.
func (s *Server) dispatch(req proto.Message) bool {
	if req.Method == proto.MethodHello {
		s.handleHello(req)
		return false
	}

	s.mu.Lock()
	init := s.initialized
	s.mu.Unlock()
	if !init {
		s.writeError(req.ID, proto.Errorf(proto.CodeNotInitialized, "send %s first", proto.MethodHello))
		return false
	}

	switch req.Method {
	case MethodPing:
		s.writeResult(req.ID, PingResult{OK: true, Role: s.cfg.Role, PID: os.Getpid()})
		return false
	case MethodInfo:
		s.writeResult(req.ID, InfoResult{
			Version:  s.cfg.Version,
			Role:     s.cfg.Role,
			PID:      os.Getpid(),
			Hardware: s.cfg.Hardware,
		})
		return false
	case proto.MethodStats:
		s.handleStats(req.ID)
		return false
	case proto.MethodFlush:
		if s.cfg.Hooks.Flush != nil {
			s.cfg.Hooks.Flush()
		}
		s.writeResult(req.ID, proto.Empty{})
		return false
	case proto.MethodEngineConfig:
		s.handleConfig(req)
		return false
	case proto.MethodJobCancel:
		s.handleCancel(req)
		return false
	case proto.MethodShutdown:
		s.cancelAllJobs()
		s.writeResult(req.ID, proto.Empty{})
		return true
	}

	h, ok := s.handlers[req.Method]
	if !ok {
		s.writeError(req.ID, proto.Errorf(proto.CodeMethodNotFound, "method %q is not served by role %q", req.Method, s.cfg.Role))
		return false
	}

	if proto.IsJob(req.Method) {
		s.startJob(req, h)
		return false
	}

	// Non-job control handler: run inline so ordering is preserved.
	res, err := h(context.Background(), req.ID, req.Params, s)
	s.sendHandlerOutcome(req.ID, context.Background(), res, err)
	return false
}

func (s *Server) handleHello(req proto.Message) {
	var p proto.HelloParams
	if err := req.DecodeParams(&p); err != nil {
		s.writeError(req.ID, proto.Errorf(proto.CodeBadRequest, "invalid hello params: %v", err))
		return
	}
	if p.Protocol != proto.Version {
		data, _ := json.Marshal(map[string]any{"supported": []int{proto.Version}})
		s.writeError(req.ID, &proto.Error{
			Code:    proto.CodeProtocolMismatch,
			Message: fmt.Sprintf("unsupported protocol %d (supported: %d)", p.Protocol, proto.Version),
			Data:    data,
		})
		return
	}
	if p.Role != "" && p.Role != s.cfg.Role {
		s.writeError(req.ID, proto.Errorf(proto.CodeBadRequest, "role mismatch: process is %q, hello asked for %q", s.cfg.Role, p.Role))
		return
	}

	s.mu.Lock()
	s.initialized = true
	s.helloParams = p
	s.mu.Unlock()

	methods := s.advertisedMethods()
	families := slices.Clone(s.cfg.Hooks.Families)
	if families == nil {
		families = []string{}
	}

	s.writeResult(req.ID, proto.HelloResult{
		Protocol:    proto.Version,
		Version:     s.cfg.Version,
		Role:        s.cfg.Role,
		PID:         os.Getpid(),
		Concurrency: s.cfg.Hooks.Concurrency,
		Methods:     methods,
		Families:    families,
		Device:      s.cfg.Hardware.ToProtoDevice(),
	})
}

func (s *Server) advertisedMethods() []string {
	methods := []string{
		proto.MethodHello,
		MethodPing,
		MethodInfo,
		proto.MethodStats,
		proto.MethodFlush,
		proto.MethodShutdown,
		proto.MethodJobCancel,
	}
	if s.cfg.Hooks.Configure != nil {
		methods = append(methods, proto.MethodEngineConfig)
	}
	for m := range s.handlers {
		if !slices.Contains(methods, m) {
			methods = append(methods, m)
		}
	}
	slices.Sort(methods)
	return methods
}

func (s *Server) handleStats(id string) {
	var mem proto.Memory
	var models []proto.LoadedModel
	if s.cfg.Hooks.Stats != nil {
		mem, models = s.cfg.Hooks.Stats()
	}
	if models == nil {
		models = []proto.LoadedModel{}
	}
	s.mu.Lock()
	jobIDs := make([]string, 0, len(s.jobs))
	for jid := range s.jobs {
		jobIDs = append(jobIDs, jid)
	}
	s.mu.Unlock()
	slices.Sort(jobIDs)
	s.writeResult(id, proto.StatsResult{
		Memory: mem,
		Models: models,
		Jobs:   jobIDs,
	})
}

func (s *Server) handleConfig(req proto.Message) {
	if s.cfg.Hooks.Configure == nil {
		s.writeError(req.ID, proto.Errorf(proto.CodeMethodNotFound, "%s is not supported by role %q", proto.MethodEngineConfig, s.cfg.Role))
		return
	}
	var p proto.EngineConfigParams
	if err := req.DecodeParams(&p); err != nil {
		s.writeError(req.ID, proto.Errorf(proto.CodeBadRequest, "invalid engine.config params: %v", err))
		return
	}
	res, err := s.cfg.Hooks.Configure(p)
	s.sendHandlerOutcome(req.ID, context.Background(), res, err)
}

func (s *Server) handleCancel(req proto.Message) {
	var p proto.CancelParams
	if err := req.DecodeParams(&p); err != nil {
		s.writeError(req.ID, proto.Errorf(proto.CodeBadRequest, "invalid job.cancel params: %v", err))
		return
	}
	s.mu.Lock()
	cancel, ok := s.jobs[p.Job]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	s.writeResult(req.ID, proto.CancelResult{Cancelled: ok})
}

func (s *Server) startJob(req proto.Message, h Handler) {
	s.mu.Lock()
	if len(s.jobs) >= s.cfg.Hooks.Concurrency {
		s.mu.Unlock()
		s.writeError(req.ID, proto.Errorf(proto.CodeBusy, "role %q already running %d job(s)", s.cfg.Role, s.cfg.Hooks.Concurrency))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.jobs[req.ID] = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		res, err := h(ctx, req.ID, req.Params, s)
		// Free the job slot BEFORE writing the response so a client sending
		// the next job immediately upon receiving our reply never hits busy.
		s.mu.Lock()
		delete(s.jobs, req.ID)
		s.mu.Unlock()
		cancel()
		s.sendHandlerOutcome(req.ID, ctx, res, err)
	}()
}

func (s *Server) cancelAllJobs() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.jobs))
	for _, c := range s.jobs {
		cancels = append(cancels, c)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (s *Server) sendHandlerOutcome(id string, ctx context.Context, res any, err error) {
	if err != nil {
		var perr *proto.Error
		if errors.As(err, &perr) {
			s.writeError(id, perr)
			return
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			s.writeError(id, proto.Errorf(proto.CodeCancelled, "job %s cancelled", id))
			return
		}
		s.writeError(id, proto.Errorf(proto.CodeInternal, "%v", err))
		return
	}
	s.writeResult(id, res)
}

func (s *Server) writeResult(id string, result any) {
	m, err := proto.NewResult(id, result)
	if err != nil {
		s.writeError(id, proto.Errorf(proto.CodeInternal, "encode result: %v", err))
		return
	}
	_ = s.writeMessage(m)
}

func (s *Server) writeError(id string, e *proto.Error) {
	_ = s.writeMessage(proto.NewError(id, e))
}

func (s *Server) writeMessage(m proto.Message) error {
	line, err := proto.Encode(m)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.out.Write(line)
	return err
}
