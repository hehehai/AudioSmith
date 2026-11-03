package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// This file is the protocol-2 contract thread B implements (M0): a
// Supervisor that runs one engine process per role, handshakes, routes
// requests and notifications by id, restarts crashed workers and keeps the
// resident models of all workers under one memory budget. See
// docs/protocol-v2.md for the wire format and docs/architecture.md for
// how the job scheduler sits on top.

// WorkerState is where an engine process is in its life.
type WorkerState string

const (
	WorkerStopped  WorkerState = "stopped"  // not started, or stopped on purpose
	WorkerStarting WorkerState = "starting" // spawned, engine.hello not answered yet
	WorkerReady    WorkerState = "ready"    // handshake done, accepting requests
	WorkerFailed   WorkerState = "failed"   // exited or refused; Err and StderrTail say why
)

// WorkerStatus is a snapshot of one worker, for the status bar, the ⌘E
// switcher and the Engine/Diagnostics pages.
type WorkerStatus struct {
	Role       proto.Role
	State      WorkerState
	PID        int
	Hello      *proto.HelloResult // nil until ready
	Stats      *proto.StatsResult // last engine.stats, polled while jobs run; nil before the first
	Running    []string           // ids of requests in flight
	Err        string             // why it failed, in English (details for logs and bug reports)
	Reason     FailReason         // why it failed, for a translated message; "" unless failed
	StderrTail string             // last stderr lines after a crash
	Restarts   int                // automatic restarts since Start
	RestartAt  time.Time          // when the next automatic restart happens; zero = none planned
	Since      time.Time          // when State last changed
}

// FailReason classifies a failed worker, so the UI can explain it in the
// user's language and offer the right fix.
type FailReason string

const (
	FailMissing   FailReason = "missing"   // no executable: build it (scripts/build-engine.sh) or set AUDIOSMITH_ENGINE
	FailStart     FailReason = "start"     // the executable could not be started
	FailProtocol  FailReason = "protocol"  // it speaks another protocol version or role: rebuild it; not restarted
	FailHandshake FailReason = "handshake" // engine.hello was not answered in time
	FailCrashed   FailReason = "crashed"   // it exited without being asked to
	FailStuck     FailReason = "stuck"     // killed because a cancelled job did not stop within the grace period
)

// StatusChanged is published on the core.Bus whenever a worker's State,
// PID, loaded models or Err change. Topic "engine.status".
type StatusChanged struct{ Status WorkerStatus }

func (StatusChanged) Topic() string { return "engine.status" }

// Config configures a Supervisor.
type Config struct {
	Bin   string       // audiosmith-engine executable (Find)
	Roles []proto.Role // roles Start runs; default gpu
	// OnDemand roles start with the first Call to them (and after Restart),
	// not with Start: the listen and media workers, which most sessions
	// never need. Status lists them (stopped until then).
	OnDemand   []proto.Role
	AppVersion string    // sent in engine.hello
	CacheDir   string    // HF hub cache (hub.CacheDir), sent in engine.hello
	Offline    bool      // engines never download; the Go hub does (M0 goal)
	LogDir     string    // stderr of role r goes to LogDir/engine-<r>.log (rotated)
	Bus        *core.Bus // StatusChanged (and log lines when Log is nil)
	// Log receives the engines' log notifications and stderr lines, with
	// Source "engine:<role>" (the app's logs.Ring.Append). nil publishes
	// them on Bus as core.LogAppended.
	Log func(core.LogEntry)
	// Prepare runs before every start of a worker, e.g. to remove what a
	// failed model load left in the HF cache (hub.CleanEngineCopies).
	Prepare func(proto.Role)
	// MemoryBudget is the bytes resident models of all workers may take
	// together; above it the supervisor unloads the least recently used
	// idle models (models.unload). 0 means 60% of physical memory.
	MemoryBudget int64
	// Family, when set, shares the budget with the other ActNow family
	// apps (supervise_family.go): it caps MemoryBudget, hears what the
	// workers hold, and is asked for memory before a load that does not
	// fit; its requests unload the models idle for a minute.
	Family Family
	// IdleUnload unloads a model nobody used for this long (settings:
	// "Unload idle models"); 0 keeps models until the budget needs room.
	IdleUnload time.Duration
	// HelloTimeout bounds the handshake (default 30 s: first launch
	// compiles Metal shaders).
	HelloTimeout time.Duration
	// MaxRestarts bounds automatic restarts within RestartWindow (default 3
	// in 10 min); beyond it the worker stays failed until Restart.
	MaxRestarts   int
	RestartWindow time.Duration
	// CancelGrace is how long a cancelled job may take to end before its
	// worker is killed and restarted (default 5 s).
	CancelGrace time.Duration
	// WriteTimeout bounds one request's write to the engine's stdin
	// (default 10 s): an engine that stops reading is killed (FailStuck)
	// and restarted instead of blocking every caller, cancel and shutdown.
	WriteTimeout time.Duration
	// StopTimeout is how long Stop waits for a process to exit after
	// engine.shutdown and closing its stdin before killing it (default 3 s).
	StopTimeout time.Duration
	// StatsEvery is how often engine.stats is polled while jobs run
	// (default 2 s); it is also polled when a job starts and ends.
	// Negative: never (tests).
	StatsEvery time.Duration
	// Env is added to the engine processes' environment (HF_HUB_CACHE,
	// proxies…; tests select a fake engine with it).
	Env  []string
	Logf func(format string, args ...any) // supervisor diagnostics; default log.Printf
}

// Supervisor runs and talks to the engine workers. Goroutine-safe.
type Supervisor interface {
	// Start spawns the configured roles and returns; handshakes complete in
	// the background and are reported as StatusChanged.
	Start(ctx context.Context) error

	// Call sends a request to the worker of role and waits for its
	// response, returning the raw result or an error:
	//   - *proto.Error for an error response (use errors.Is with a code);
	//   - an error wrapping core.ErrEngine when the worker is not ready,
	//     or exits before answering (a job then counts as interrupted).
	// id is the request id; job methods pass the jobs.Job id so logs and
	// notifications correlate, "" lets the supervisor number control
	// requests. notify receives the job's notifications (job.progress,
	// tts.chunk, log lines naming it) on the reader goroutine, in order,
	// before Call returns; it must not block. Cancelling ctx sends
	// job.cancel for job methods and Call returns once the engine answers
	// (or after a 5 s grace, killing the worker if it is stuck).
	Call(ctx context.Context, role proto.Role, id, method string, params any, notify func(proto.Message)) (json.RawMessage, error)

	// Status returns a snapshot of every configured worker.
	Status() []WorkerStatus
	// Restart stops the worker of role (cancelling its calls) and starts a
	// fresh one; it resets the restart budget.
	Restart(role proto.Role) error
	// SetMemoryBudget changes Config.MemoryBudget at run time (settings);
	// 0 is automatic. Models over the new budget are unloaded at once.
	SetMemoryBudget(bytes int64)
	// SetIdleUnload changes Config.IdleUnload at run time.
	SetIdleUnload(d time.Duration)
	// Hold keeps the models of roles resident until release is called
	// (once; more calls do nothing): the supervisor unloads none of them,
	// for the idle limit, the budget or the ActNow family, while
	// something uses them between jobs (a dictation that records, a live
	// call). Holds of a role add up.
	Hold(roles ...proto.Role) (release func())
	// HoldModels is Hold for some models of role only (a dictation
	// pins its recogniser and the clean-up language model, not every
	// model the gpu worker has; code review 1102 M1). A model not
	// resident yet is held once it loads.
	HoldModels(role proto.Role, models ...string) (release func())
	// SetCacheDir changes Config.CacheDir; it applies to the workers
	// started (or restarted) after the call.
	SetCacheDir(dir string)
	// Stop asks every worker to shut down (engine.shutdown), then kills
	// what is left after a grace period. Calls in flight fail with core.ErrEngine.
	Stop()
}

// ErrWorkerExited is wrapped by Call errors when the worker died mid-call.
var ErrWorkerExited = fmt.Errorf("engine worker exited: %w", core.ErrEngine)
