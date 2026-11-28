package family

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/enginetest"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/actnowkit/conformance"
	actnow "github.com/hehehai/actnowkit/family"
	famruntime "github.com/hehehai/actnowkit/runtime"
)

// The fake engine of internal/engine/enginetest runs in this test binary
// (the supervisor tests below spawn it).
func TestMain(m *testing.M) {
	if enginetest.Enabled() {
		enginetest.Main()
	}
	os.Exit(m.Run())
}

var _ engine.Family = (*Runtime)(nil)

// startedFamily is AudioSmith's Family in a fresh fixture, Start done.
func startedFamily(t *testing.T) *testFamily {
	t.Helper()
	fx := conformance.New(t)
	f := newFamily(t, fx, "")
	if err := f.Start(context.Background(), inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	return f
}

// peerHolds writes another running app's runtime file (this process's
// pid: alive) holding bytes.
func peerHolds(t *testing.T, f *testFamily, bundleID string, bytes int64) {
	t.Helper()
	err := famruntime.Write(f.Dir, famruntime.File{BundleID: bundleID, Loaded: []famruntime.Loaded{
		{Repo: "mlx-community/Qwen3-ASR-0.6B-4bit", Revision: revB, Worker: "listen", Bytes: bytes}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeBudget(t *testing.T) {
	f := startedFamily(t)
	rt := f.Runtime(RuntimeOptions{Fresh: time.Nanosecond})
	const gb = int64(1) << 30
	// Alone: 60 % of physical memory, or the setting when lower.
	if b := rt.Budget(0, 16*gb); b != famruntime.FamilyCap(uint64(16*gb)) {
		t.Errorf("alone: %d", b)
	}
	if b := rt.Budget(4*gb, 16*gb); b != 4*gb {
		t.Errorf("own setting: %d", b)
	}
	// Dicta holds 6 GB: the family's 9.6 GB leave 3.6 GB.
	peerHolds(t, f, "dev.hehehai.dicta", 6*gb)
	want := famruntime.FamilyCap(uint64(16*gb)) - 6*gb
	if b := rt.Budget(0, 16*gb); b != want {
		t.Errorf("Dicta holds 6 GB: budget %d, want %d", b, want)
	}
	if b := rt.Budget(8*gb, 16*gb); b != want {
		t.Errorf("own 8 GB is above what is left: %d", b)
	}
	if b := rt.Budget(2*gb, 16*gb); b != 2*gb {
		t.Errorf("own 2 GB fits: %d", b)
	}
	// Nothing left: 0 (the supervisor then loads anyway).
	peerHolds(t, f, "dev.hehehai.dicta", 12*gb)
	if b := rt.Budget(0, 16*gb); b != 0 {
		t.Errorf("Dicta over the family's share: %d", b)
	}
	// AudioSmith's own file does not count against it.
	rt.Publish(map[proto.Role][]proto.LoadedModel{proto.RoleGPU: {{Model: "acme/tts-a", Bytes: 5 * gb}}}, 0)
	os.Remove(f.Dir.RuntimeFile("dev.hehehai.dicta"))
	if b := rt.Budget(0, 16*gb); b != famruntime.FamilyCap(uint64(16*gb)) {
		t.Errorf("its own models count against it: %d", b)
	}
	// A crashed app's file (pid gone) is ignored.
	err := famruntime.Write(f.Dir, famruntime.File{BundleID: "dev.hehehai.pik", PID: deadPID(t), Loaded: []famruntime.Loaded{{Repo: "x/y", Bytes: 9 * gb}}})
	if err != nil {
		t.Fatal(err)
	}
	if b := rt.Budget(0, 16*gb); b != famruntime.FamilyCap(uint64(16*gb)) {
		t.Errorf("a gone process counts: %d", b)
	}
}

// deadPID is the pid of a process that ended.
func deadPID(t *testing.T) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

func TestRuntimePublishAndClose(t *testing.T) {
	fx := conformance.New(t)
	f := newFamily(t, fx, "")
	rt := f.Runtime(RuntimeOptions{Revision: func(repo string) string { return map[string]string{"acme/tts-a": revA}[repo] }})
	path := f.Dir.RuntimeFile(ReleaseBundleID)
	loaded := map[proto.Role][]proto.LoadedModel{
		proto.RoleGPU:    {{Model: "acme/tts-a", Bytes: 300, IdleSeconds: 90}},
		proto.RoleListen: {{Model: "acme/asr-b", Bytes: 200}}, // busy
	}
	// Nothing loaded yet: no file.
	rt.Publish(nil, 1000)
	// Before Start the directory is not set up: nothing written yet.
	rt.Publish(loaded, 1000)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("written before Start: %v", err)
	}
	if err := f.Start(context.Background(), inputs(fx.Hub), wantedOf()); err != nil {
		t.Fatal(err)
	}
	rt.Flush()
	file, err := famruntime.Read(f.Dir, ReleaseBundleID)
	if err != nil {
		t.Fatal(err)
	}
	if file.PID != os.Getpid() || file.BudgetBytes != 1000 || len(file.Loaded) != 2 {
		t.Fatalf("runtime file %+v", file)
	}
	tts, asr := file.Loaded[0], file.Loaded[1]
	if tts.Repo != "acme/tts-a" || tts.Revision != revA || tts.Worker != "gpu" || tts.Bytes != 300 || tts.IdleSince == nil ||
		time.Since(*tts.IdleSince) < 85*time.Second {
		t.Errorf("idle model %+v", tts)
	}
	if asr.Worker != "listen" || asr.IdleSince != nil || asr.Revision != "" {
		t.Errorf("busy model %+v", asr)
	}
	// The same models again: not rewritten.
	os.Remove(path)
	loaded[proto.RoleGPU][0].IdleSeconds = 95
	rt.Publish(loaded, 1000)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("rewritten although nothing changed")
	}
	// A change: written; everything unloaded: written, empty.
	rt.Publish(map[proto.Role][]proto.LoadedModel{proto.RoleGPU: loaded[proto.RoleGPU]}, 1000)
	if file, _ := famruntime.Read(f.Dir, ReleaseBundleID); len(file.Loaded) != 1 {
		t.Errorf("after the listen model left: %+v", file.Loaded)
	}
	rt.Publish(nil, 1000)
	if file, err := famruntime.Read(f.Dir, ReleaseBundleID); err != nil || len(file.Loaded) != 0 {
		t.Errorf("all unloaded: %+v %v", file, err)
	}
	// Quit: removed, and nothing more is written.
	rt.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("still there after Close: %v", err)
	}
	rt.Publish(loaded, 1000)
	rt.SetPasting(true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("written after Close")
	}
}

func TestRuntimeReadOnlyWritesNothing(t *testing.T) {
	fx := conformance.New(t)
	addMember(t, fx.Family.FamilyFile(), "specVersion", "2")
	f := newFamily(t, fx, "")
	if err := f.Start(context.Background(), inputs(fx.Hub), wantedOf()); err == nil {
		t.Fatal("Start of a newer spec's directory")
	}
	rt := f.Runtime(RuntimeOptions{})
	rt.Publish(map[proto.Role][]proto.LoadedModel{proto.RoleGPU: {{Model: "acme/tts-a", Bytes: 1}}}, 1)
	rt.Flush()
	rt.RequestMemory(context.Background())
	if _, err := os.Stat(f.Dir.RuntimeFile(ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("a read-only directory got a runtime file: %v", err)
	}
}

// A request for memory: the others' loaded bytes are watched until they
// shrink; AudioSmith's own request coming back is not a request to it,
// another app's is.
func TestRuntimeRequestMemory(t *testing.T) {
	f := startedFamily(t)
	rt := f.Runtime(RuntimeOptions{Wait: 3 * time.Second, Poll: 10 * time.Millisecond})
	const gb = int64(1) << 30
	peerHolds(t, f, "dev.hehehai.dicta", 6*gb)
	w, err := f.Dir.Watch(actnow.TopicMemory)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	go func() {
		<-w.C // Dicta hears it and unloads
		peerHolds(t, f, "dev.hehehai.dicta", 1*gb)
	}()
	start := time.Now()
	rt.RequestMemory(context.Background())
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("waited %v although Dicta unloaded at once", d)
	}
	if b := rt.Budget(0, 16*gb); b != famruntime.FamilyCap(uint64(16*gb))-gb {
		t.Errorf("budget after the request: %d", b)
	}
	rt.Handle([]string{actnow.TopicMemory}) // its own request, heard back
	select {
	case <-rt.Requested():
		t.Error("AudioSmith's own request unloads its models")
	default:
	}
	rt2 := f.Runtime(RuntimeOptions{})
	rt2.Handle([]string{actnow.TopicMemory})
	select {
	case <-rt2.Requested():
	default:
		t.Error("another app's request is not passed on")
	}
	// Nobody else holds anything: no request at all.
	os.Remove(f.Dir.RuntimeFile("dev.hehehai.dicta"))
	start = time.Now()
	rt2.RequestMemory(context.Background())
	if time.Since(start) > time.Second {
		t.Error("asked and waited with nobody to ask")
	}
}

// The whole path with the fake engine: another app asks for memory
// (TopicMemory), the Family's watcher hands it to the Runtime, the
// supervisor unloads the models idle long enough and the runtime file
// says so; at quit the file is removed before the engines stop.
func TestRuntimeWithSupervisor(t *testing.T) {
	f := startedFamily(t)
	rt := f.Runtime(RuntimeOptions{IdleAfter: 300 * time.Millisecond})
	f.OnChange(rt.Handle)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sup := engine.NewSupervisor(engine.Config{Bin: bin, AppVersion: "test", LogDir: t.TempDir(), Env: enginetest.Env(""),
		StatsEvery: 20 * time.Millisecond, Logf: t.Logf, Family: rt})
	t.Cleanup(sup.Stop)
	if err := sup.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the engine", func() bool { return sup.Status()[0].State == engine.WorkerReady })
	ctx := context.Background()
	for _, m := range []string{"acme/old", "acme/recent"} {
		if _, err := sup.Call(ctx, proto.RoleGPU, "L-"+m, proto.MethodModelsLoad, proto.LoadParams{Model: m}, nil); err != nil {
			t.Fatal(err)
		}
		if m == "acme/old" {
			time.Sleep(600 * time.Millisecond)
		}
	}
	waitFor(t, "both models in the runtime file", func() bool { return slices.Equal(runtimeRepos(f), []string{"acme/old", "acme/recent"}) })

	if err := famruntime.RequestMemory(f.Dir); err != nil { // as Dicta does
		t.Fatal(err)
	}
	waitFor(t, "acme/old unloaded", func() bool { return slices.Equal(runtimeRepos(f), []string{"acme/recent"}) })

	// Quit: Close, then Stop (enginelink.go's order).
	rt.Close()
	sup.Stop()
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(f.Dir.RuntimeFile(ReleaseBundleID)); !os.IsNotExist(err) {
		t.Errorf("the runtime file outlived the quit: %v", err)
	}
}

func runtimeRepos(f *testFamily) []string {
	file, err := famruntime.Read(f.Dir, ReleaseBundleID)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range file.Loaded {
		out = append(out, l.Repo)
	}
	slices.Sort(out)
	return out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRuntimeLoadedIn(t *testing.T) {
	f := startedFamily(t)
	f.fx.Register("dev.hehehai.dicta")
	f.Rediscover()
	peerHolds(t, f, "dev.hehehai.dicta", 1<<30)
	rt := f.Runtime(RuntimeOptions{})
	if got := rt.LoadedIn("mlx-community/Qwen3-ASR-0.6B-4bit", ""); !slices.Equal(got, []string{"Dicta"}) {
		t.Errorf("LoadedIn = %v", got)
	}
	if got := rt.LoadedIn("mlx-community/Qwen3-ASR-0.6B-4bit", revA); len(got) != 0 {
		t.Errorf("another revision: %v", got)
	}
}

func TestRuntimePasting(t *testing.T) {
	f := startedFamily(t)
	rt := f.Runtime(RuntimeOptions{})
	rt.SetPasting(true)
	if file, err := famruntime.Read(f.Dir, ReleaseBundleID); err != nil || !file.Pasting {
		t.Fatalf("pasting not written: %+v %v", file, err)
	}
	rt.SetPasting(false)
	if file, _ := famruntime.Read(f.Dir, ReleaseBundleID); file.Pasting {
		t.Error("still pasting")
	}
	// Another app pasting: the wait ends after at most half a second.
	err := famruntime.Write(f.Dir, famruntime.File{BundleID: "dev.hehehai.auralink", Pasting: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if rt.WaitPasting(context.Background()) {
		t.Error("the way is clear while Auralink pastes")
	}
	if d := time.Since(start); d < 400*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v", d)
	}
}
