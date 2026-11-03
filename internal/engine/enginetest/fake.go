// Package enginetest is a scripted protocol-2 audiosmith-engine for tests:
// a test binary runs it instead of its tests when AUDIOSMITH_FAKE_ENGINE is
// set, so the supervisor spawns a real process and talks to it over real
// pipes, without MLX. In the test package:
//
//	func TestMain(m *testing.M) {
//		if enginetest.Enabled() {
//			enginetest.Main()
//		}
//		os.Exit(m.Run())
//	}
//
// and point engine.Config at os.Executable() with Env: enginetest.Env(opts).
//
// The fake serves every method of protocol 2 in the role it is started
// with (--role, or hello's role), as the real engine will: gpu (models,
// tts.*, stt.*, vad.detect, enhance.run), media (media.*), listen
// (capture.*). It reads and writes real WAV files (Audio, ReadWAV,
// WriteWAV), so code downstream of it (joining, analysis, playback) can be
// tested on its output. See fake_media.go for what each M1 method does.
package enginetest

//
// AUDIOSMITH_FAKE_ENGINE is a comma-separated list of options:
//
//	(empty)        speak protocol 2 (the default; any option below changes it)
//	legacy         behave as a protocol-1 engine: print its ready line
//	mismatch       answer engine.hello with protocol 3
//	refuse         answer engine.hello with protocol_mismatch
//	silent         never answer engine.hello
//	role=R         claim role R in hello
//	crash-start    exit 3 at once
//	hang-cancel    ignore job.cancel (the job keeps running)
//	stall-stdin    stop reading stdin after engine.hello (a stuck engine)
//	mem=N          device memory in hello, bytes
//	replay=PATH    replay a fixture transcript (engine/Fixtures/*.jsonl)
//	deny-mic       capture.start fails with permission_denied
//	level-ms=N     capture.level every N ms (default 50)
//
// tts.synthesize reacts to its text, and the other jobs to their input
// path: "crash" exits 3 mid-job, "oom" fails with oom, "slow" runs 100
// steps of 20 ms (cancellable), anything else 3 steps. Models whose repo
// contains "broken" fail to load. models.load of "pressure/<level>" plays
// a memory-pressure event (engine.memory; see fakeEngine.pressure).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

const fakeEnv = "AUDIOSMITH_FAKE_ENGINE"

// Enabled reports whether this process should be the fake engine.
func Enabled() bool {
	_, ok := os.LookupEnv(fakeEnv)
	return ok
}

// Env returns the environment that makes a re-executed test binary the
// fake engine with opts (see the package comment of the file).
func Env(opts string) []string { return []string{fakeEnv + "=" + opts} }

// Main runs the fake engine and exits.
func Main() {
	runFakeEngine()
	os.Exit(0)
}

// Methods returns what an engine of role implements (hello.methods).
func Methods(role proto.Role) []string {
	common := []string{proto.MethodHello, proto.MethodStats, proto.MethodFlush, proto.MethodShutdown, proto.MethodJobCancel}
	switch role {
	case proto.RoleMedia:
		return append(common, proto.MethodMediaProbe, proto.MethodMediaDecode, proto.MethodMediaEncode,
			proto.MethodMediaMux, proto.MethodMediaThumbnail, proto.MethodMediaTranscode,
			proto.MethodWatermarkEmbed, proto.MethodWatermarkDetect)
	case proto.RoleListen:
		return append(common, proto.MethodCaptureDevices, proto.MethodCaptureStart, proto.MethodCaptureStop,
			proto.MethodModelsLoad, proto.MethodModelsUnload, proto.MethodModelsLoaded, proto.MethodSTTTranscribe)
	}
	return append(common, proto.MethodModelsLoad, proto.MethodModelsUnload, proto.MethodModelsLoaded,
		proto.MethodTTSSynthesize, proto.MethodTTSEncodeVoice, proto.MethodSTTTranscribe, proto.MethodSTTAlign,
		proto.MethodVADDetect, proto.MethodEnhanceRun, proto.MethodSeparateRun, proto.MethodDiarizeRun, proto.MethodLLMGenerate,
		proto.MethodTTSDialogue)
}

// Concurrency is the jobs an engine of role runs at once: gpu 1, listen 2
// (a recording and a transcription), media 2.
func Concurrency(role proto.Role) int {
	if role == proto.RoleGPU {
		return 1
	}
	return 2
}

type fakeEngine struct {
	opts map[string]string
	out  *bufio.Writer
	wmu  sync.Mutex

	mu       sync.Mutex
	hello    bool
	role     proto.Role
	running  map[string]string    // job id → method
	cancel   map[string]bool      // jobs asked to stop by job.cancel
	stop     map[string]chan bool // capture.start jobs: closed by capture.stop
	finished map[string]chan struct{}
	models   map[string]time.Time // loaded model → last use
}

func runFakeEngine() {
	opts := map[string]string{}
	for _, o := range strings.Split(os.Getenv(fakeEnv), ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
		if k != "" {
			opts[k] = v
		}
	}
	f := &fakeEngine{opts: opts, out: bufio.NewWriter(os.Stdout), role: proto.RoleGPU,
		running: map[string]string{}, cancel: map[string]bool{}, stop: map[string]chan bool{},
		finished: map[string]chan struct{}{}, models: map[string]time.Time{}}
	if i := slices.Index(os.Args, "--role"); i >= 0 && i+1 < len(os.Args) {
		f.role = proto.Role(os.Args[i+1])
	}
	fmt.Fprintln(os.Stderr, "fake engine starting", os.Args[1:])
	if _, ok := opts["crash-start"]; ok {
		fmt.Fprintln(os.Stderr, "fatal: could not start")
		os.Exit(3)
	}
	if _, ok := opts["legacy"]; ok {
		f.writeRaw(`{"event":"ready","version":"0.1.0","protocolVersion":1,"pid":` + strconv.Itoa(os.Getpid()) + `}`)
	}
	if path, ok := opts["replay"]; ok {
		f.replay(path)
		os.Exit(0)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), proto.MaxLine)
	for sc.Scan() {
		m, err := proto.Decode(sc.Bytes())
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad line:", err)
			continue
		}
		f.handle(m)
		if _, stall := opts["stall-stdin"]; stall && m.Method == proto.MethodHello {
			// Never reads again; stdin closing goes unnoticed too. A sleep
			// loop, not select {}: with no other goroutine the runtime would
			// call that a deadlock and exit (CGO_ENABLED=0 builds).
			for {
				time.Sleep(time.Hour)
			}
		}
	}
	// stdin closed: cancel the jobs, exit.
	f.mu.Lock()
	for id := range f.running {
		f.cancel[id] = true
	}
	f.mu.Unlock()
	os.Exit(0)
}

func (f *fakeEngine) writeRaw(line string) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.WriteString(line + "\n")
	f.out.Flush()
}

func (f *fakeEngine) send(m proto.Message) {
	if m.Kind() == proto.KindResponse {
		// As the real engine does, a job's slot is free before its
		// response is written: a job sent once the app has the response
		// is never answered busy.
		f.release(m.ID)
	}
	f.put(m)
}

// put writes m as it is.
func (f *fakeEngine) put(m proto.Message) {
	line, _ := proto.Encode(m)
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.out.Write(line)
	f.out.Flush()
}

func (f *fakeEngine) result(id string, v any) {
	m, _ := proto.NewResult(id, v)
	f.send(m)
}

func (f *fakeEngine) fail(id, code, format string, args ...any) {
	f.send(proto.NewError(id, proto.Errorf(code, format, args...)))
}

// failData answers id with an error carrying data.
func (f *fakeEngine) failData(id, code string, data any, format string, args ...any) {
	e := proto.Errorf(code, format, args...)
	e.Data, _ = json.Marshal(data)
	f.send(proto.NewError(id, e))
}

func (f *fakeEngine) notify(method string, params any) {
	m, _ := proto.NewNotification(method, params)
	f.send(m)
}

func (f *fakeEngine) handle(m proto.Message) {
	if m.Method == proto.MethodHello {
		f.handleHello(m)
		return
	}
	f.mu.Lock()
	ready, role := f.hello, f.role
	f.mu.Unlock()
	if !ready {
		f.fail(m.ID, proto.CodeNotInitialized, "engine.hello must come first")
		return
	}
	if !slices.Contains(Methods(role), m.Method) {
		f.fail(m.ID, proto.CodeMethodNotFound, "Unknown method %s", m.Method)
		return
	}
	switch m.Method {
	case proto.MethodStats:
		f.mu.Lock()
		st := proto.StatsResult{Memory: proto.Memory{Active: 1000, Cache: 10, Peak: 2000}, Models: f.loadedLocked(), Jobs: []string{}}
		for id := range f.running {
			st.Jobs = append(st.Jobs, id)
		}
		slices.Sort(st.Jobs)
		f.mu.Unlock()
		f.result(m.ID, st)
	case proto.MethodFlush:
		f.result(m.ID, nil)
	case proto.MethodShutdown:
		f.result(m.ID, nil)
		os.Exit(0)
	case proto.MethodModelsLoaded:
		f.mu.Lock()
		res := proto.LoadedResult{Models: f.loadedLocked()}
		f.mu.Unlock()
		f.result(m.ID, res)
	case proto.MethodModelsUnload:
		var p proto.UnloadParams
		m.DecodeParams(&p)
		f.mu.Lock()
		res := proto.UnloadResult{Unloaded: []string{}}
		for model, used := range f.models {
			if p.IdleAtLeast > 0 && (len(f.running) > 0 || time.Since(used).Seconds() < p.IdleAtLeast) {
				continue // in use, or used since (as the engine's ModelStore)
			}
			if p.Model == "" || p.Model == model {
				delete(f.models, model)
				res.Unloaded = append(res.Unloaded, model)
			}
		}
		f.mu.Unlock()
		f.result(m.ID, res)
	case proto.MethodJobCancel:
		var p proto.CancelParams
		m.DecodeParams(&p)
		f.mu.Lock()
		_, running := f.running[p.Job]
		if running {
			if _, hang := f.opts["hang-cancel"]; !hang {
				f.cancel[p.Job] = true
			}
		}
		f.mu.Unlock()
		f.result(m.ID, proto.CancelResult{Cancelled: running})
	case proto.MethodMediaProbe:
		f.probe(m)
	case proto.MethodCaptureDevices:
		f.result(m.ID, proto.DevicesResult{Devices: FakeDevices()})
	case proto.MethodCaptureStop:
		f.captureStop(m)
	default:
		if proto.IsJob(m.Method) {
			f.startJob(m)
			return
		}
		f.fail(m.ID, proto.CodeMethodNotFound, "Unknown method %s", m.Method)
	}
}

// startJob runs a job request on its own goroutine, within the role's
// concurrency.
func (f *fakeEngine) startJob(m proto.Message) {
	f.mu.Lock()
	if _, dup := f.running[m.ID]; dup {
		f.mu.Unlock()
		f.put(proto.NewError(m.ID, proto.Errorf(proto.CodeBadRequest, "Job %s is already running", m.ID))) // not send: the running job keeps its slot
		return
	}
	if len(f.running) >= Concurrency(f.role) {
		f.mu.Unlock()
		f.fail(m.ID, proto.CodeBusy, "The engine runs %d job(s) at a time", Concurrency(f.role))
		return
	}
	f.running[m.ID] = m.Method
	done := make(chan struct{})
	f.finished[m.ID] = done
	f.mu.Unlock()
	go func() {
		defer func() {
			f.release(m.ID)
			f.mu.Lock()
			delete(f.finished, m.ID)
			f.mu.Unlock()
			close(done) // after the response: capture.stop answers after the job
		}()
		switch m.Method {
		case proto.MethodModelsLoad:
			f.load(m)
		case proto.MethodTTSSynthesize:
			f.synthesize(m)
		case proto.MethodTTSEncodeVoice:
			f.encodeVoice(m)
		case proto.MethodSTTTranscribe:
			f.transcribe(m)
		case proto.MethodSTTAlign:
			f.align(m)
		case proto.MethodVADDetect:
			f.vad(m)
		case proto.MethodEnhanceRun:
			f.enhance(m)
		case proto.MethodMediaDecode:
			f.decode(m)
		case proto.MethodMediaEncode:
			f.encode(m)
		case proto.MethodSeparateRun:
			f.separate(m)
		case proto.MethodDiarizeRun:
			f.diarize(m)
		case proto.MethodLLMGenerate:
			f.generate(m)
		case proto.MethodTTSDialogue:
			f.dialogue(m)
		case proto.MethodWatermarkEmbed:
			f.watermarkEmbed(m)
		case proto.MethodWatermarkDetect:
			f.watermarkDetect(m)
		case proto.MethodMediaMux:
			f.mux(m)
		case proto.MethodMediaThumbnail:
			f.thumbnail(m)
		case proto.MethodMediaTranscode:
			f.transcode(m)
		case proto.MethodCaptureStart:
			f.capture(m)
		default:
			f.fail(m.ID, proto.CodeMethodNotFound, "Unknown method %s", m.Method)
		}
	}()
}

// release frees job id's slot (send calls it before the job's response).
func (f *fakeEngine) release(id string) {
	f.mu.Lock()
	delete(f.running, id)
	delete(f.cancel, id)
	delete(f.stop, id)
	f.mu.Unlock()
}

func (f *fakeEngine) handleHello(m proto.Message) {
	var p proto.HelloParams
	m.DecodeParams(&p)
	if _, ok := f.opts["silent"]; ok {
		return
	}
	if _, ok := f.opts["refuse"]; ok || p.Protocol != proto.Version {
		f.send(proto.NewError(m.ID, &proto.Error{Code: proto.CodeProtocolMismatch, Message: "This engine speaks protocol 2", Data: json.RawMessage(`{"supported":[2]}`)}))
		return
	}
	role := p.Role
	if role == "" {
		role = proto.RoleGPU
	}
	families := []string{}
	if role == proto.RoleListen {
		families = []string{"whisper", "qwen3_asr", "parakeet"}
	}
	if role == proto.RoleGPU {
		families = []string{"kokoro", "qwen3_tts", "omnivoice", "chatterbox", "whisper", "qwen3_asr", "qwen3_forced_aligner", "silero_vad", "deepfilternet", "htdemucs", "mel_roformer", "sortformer", "qwen3", "gemma4", "soulx_podcast"}
	}
	if role == proto.RoleMedia {
		families = []string{"audioseal"}
	}
	res := proto.HelloResult{
		Protocol: proto.Version, Version: "fake", Role: role, PID: os.Getpid(), Concurrency: Concurrency(role),
		Methods: Methods(role), Families: families,
		Device: proto.Device{Name: "Fake", Memory: 16 << 30, OS: "test"},
	}
	if r, ok := f.opts["role"]; ok {
		res.Role = proto.Role(r)
	}
	if _, ok := f.opts["mismatch"]; ok {
		res.Protocol = 3
	}
	if mem, ok := f.opts["mem"]; ok {
		res.Device.Memory, _ = strconv.ParseInt(mem, 10, 64)
	}
	f.mu.Lock()
	f.hello, f.role = true, role
	f.mu.Unlock()
	f.notify(proto.MethodLog, proto.LogParams{Level: "info", Message: "fake engine ready"})
	f.result(m.ID, res)
}
