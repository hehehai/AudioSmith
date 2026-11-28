package engine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// fakeFamily is the family's side as the supervisor sees it: the
// budget it gives, what it was told, and its requests.
type fakeFamily struct {
	mu        sync.Mutex
	budget    int64 // what Budget returns; it records its arguments
	own, phys int64
	published []map[proto.Role][]proto.LoadedModel
	requests  int
	// onRequest runs on RequestMemory (the other apps unload).
	onRequest func(f *fakeFamily)
	req       chan struct{}
	idle      time.Duration
}

func newFakeFamily(budget int64) *fakeFamily {
	return &fakeFamily{budget: budget, req: make(chan struct{}, 1), idle: time.Minute}
}

func (f *fakeFamily) Budget(own, physical int64) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.own, f.phys = own, physical
	return f.budget
}

func (f *fakeFamily) Publish(loaded map[proto.Role][]proto.LoadedModel, budget int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, loaded)
}

func (f *fakeFamily) RequestMemory(context.Context) {
	f.mu.Lock()
	f.requests++
	fn := f.onRequest
	f.mu.Unlock()
	if fn != nil {
		fn(f)
	}
}

func (f *fakeFamily) Requested() <-chan struct{}     { return f.req }
func (f *fakeFamily) UnloadIdleAfter() time.Duration { return f.idle }

func (f *fakeFamily) setBudget(b int64) {
	f.mu.Lock()
	f.budget = b
	f.mu.Unlock()
}

func (f *fakeFamily) requested() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

// lastPublished lists the gpu worker's models in the last Publish.
func (f *fakeFamily) lastPublished() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) == 0 {
		return nil
	}
	var out []string
	for _, m := range f.published[len(f.published)-1][proto.RoleGPU] {
		out = append(out, m.Model)
	}
	return out
}

// weightsDir is a model directory whose files take n bytes.
func weightsDir(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// loadWith loads model on the gpu worker from dir (its size is the
// estimate) and waits for the stats to show it.
func loadWith(t *testing.T, s Supervisor, w *watch, model, dir string) {
	t.Helper()
	if _, err := s.Call(context.Background(), proto.RoleGPU, "L-"+model, proto.MethodModelsLoad, proto.LoadParams{Model: model, Path: dir}, nil); err != nil {
		t.Fatal(err)
	}
	w.until(t, "stats with "+model, func(st WorkerStatus) bool {
		return st.Stats != nil && slices.ContainsFunc(st.Stats.Models, func(m proto.LoadedModel) bool { return m.Model == model })
	})
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("not %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The family's budget applies (own setting and the engine's memory are
// passed on), and the runtime file hears what is loaded.
func TestSupervisorFamilyBudgetAndPublish(t *testing.T) {
	fam := newFakeFamily(150) // one 100-byte model of the fake fits
	s, w := newTestSup(t, "", func(c *Config) { c.Family, c.MemoryBudget = fam, 1<<40 })
	w.until(t, "ready", ready)
	loadWith(t, s, w, "a/one", "")
	eventually(t, "a/one published", func() bool { return slices.Equal(fam.lastPublished(), []string{"a/one"}) })
	time.Sleep(20 * time.Millisecond)
	loadWith(t, s, w, "b/two", "")
	waitLoaded(t, s, 1) // over the family's budget: the older one goes
	if got := loadedModels(t, s); got[0] != "b/two" {
		t.Errorf("kept %v", got)
	}
	eventually(t, "b/two alone published", func() bool { return slices.Equal(fam.lastPublished(), []string{"b/two"}) })
	fam.mu.Lock()
	own, phys := fam.own, fam.phys
	fam.mu.Unlock()
	if own != 1<<40 || phys != 16<<30 {
		t.Errorf("Budget(%d, %d): want the setting and the engine's 16 GB", own, phys)
	}
	if fam.requested() != 0 {
		t.Errorf("asked for memory %d times without a load that did not fit", fam.requested())
	}
}

// Before a load: own idle models go first; the other apps are not asked
// when that makes room.
func TestSupervisorMakesRoomBeforeLoading(t *testing.T) {
	fam := newFakeFamily(150)
	s, w := newTestSup(t, "", func(c *Config) { c.Family = fam })
	w.until(t, "ready", ready)
	loadWith(t, s, w, "a/one", weightsDir(t, 100))
	sup := s.(*supervisor)
	eventually(t, "a/one idle in stats", func() bool {
		_, idle := sup.resident()
		return len(idle) > 0
	})
	// b/two's 100 bytes and a/one's would take 200 of 150: a/one is
	// unloaded before b/two loads (not after, as the budget alone does).
	sup.makeRoom(context.Background(), proto.RoleGPU, proto.LoadParams{Model: "b/two", Path: weightsDir(t, 100)})
	if got := loadedModels(t, s); len(got) != 0 {
		t.Errorf("before loading b/two: still loaded %v", got)
	}
	if fam.requested() != 0 {
		t.Errorf("the other apps were asked although unloading a/one made room")
	}
	loadWith(t, s, w, "b/two", weightsDir(t, 100))
	if got := loadedModels(t, s); !slices.Equal(got, []string{"b/two"}) {
		t.Errorf("loaded %v", got)
	}
}

// Not enough even with nothing of its own loaded: the family is asked
// once, the budget read again, and the model loads anyway.
func TestSupervisorRequestsMemoryOnceThenLoads(t *testing.T) {
	fam := newFakeFamily(50) // the other apps hold most of the family's share
	s, w := newTestSup(t, "", func(c *Config) { c.Family = fam })
	w.until(t, "ready", ready)
	loadWith(t, s, w, "a/one", weightsDir(t, 100))
	if n := fam.requested(); n != 1 {
		t.Errorf("asked for memory %d times, want once", n)
	}
	// It loaded anyway (loadWith saw it); idle and over the budget, it
	// is unloaded again afterwards, as without the family.

	// The others unload when asked: the retry fits, nothing more asked.
	fam2 := newFakeFamily(50)
	fam2.onRequest = func(f *fakeFamily) { f.budget = 1000 }
	s2, w2 := newTestSup(t, "", func(c *Config) { c.Family = fam2 })
	w2.until(t, "ready", ready)
	loadWith(t, s2, w2, "a/one", weightsDir(t, 100))
	if n := fam2.requested(); n != 1 {
		t.Errorf("asked %d times", n)
	}
	// A model already loaded asks nothing.
	fam2.setBudget(50)
	loadWith(t, s2, w2, "a/one", weightsDir(t, 100))
	if n := fam2.requested(); n != 1 {
		t.Errorf("a loaded model made room: asked %d times", n)
	}
}

// Another app's request unloads the models idle for UnloadIdleAfter,
// never one a job is using.
func TestSupervisorFamilyRequestUnloadsIdleModels(t *testing.T) {
	fam := newFakeFamily(0) // no budget: only the request unloads
	fam.idle = 300 * time.Millisecond
	s, w := newTestSup(t, "", func(c *Config) { c.Family = fam })
	w.until(t, "ready", ready)
	loadWith(t, s, w, "a/one", "")
	time.Sleep(500 * time.Millisecond)
	loadWith(t, s, w, "b/two", "")
	fam.req <- struct{}{}
	waitLoaded(t, s, 1)
	if got := loadedModels(t, s); !slices.Equal(got, []string{"b/two"}) {
		t.Errorf("kept %v, want the recently used b/two", got)
	}
	eventually(t, "b/two alone published", func() bool { return slices.Equal(fam.lastPublished(), []string{"b/two"}) })

	// A job running: everything is busy (the fake's rule), nothing goes.
	time.Sleep(500 * time.Millisecond)
	notify, started := jobStarted()
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), proto.RoleGPU, "J1", proto.MethodTTSSynthesize, synth("slow", false), notify)
		done <- err
	}()
	started(t)
	s.(*supervisor).unloadIdleFor(fam.idle) // as the request does, synchronously
	if got := loadedModels(t, s); len(got) < 2 {
		t.Errorf("a request during a job unloaded models: %v", got)
	}
	if err := <-done; err != nil {
		t.Errorf("the job was interrupted: %v", err)
	}
}

func TestModelOfAndWeights(t *testing.T) {
	if m, p := modelOf(proto.LoadParams{Model: "a/b", Path: "/x"}); m != "a/b" || p != "/x" {
		t.Errorf("load params: %q %q", m, p)
	}
	if m, p := modelOf([]byte(`{"model":"a/b","modelPath":"/y"}`)); m != "a/b" || p != "/y" {
		t.Errorf("raw params: %q %q", m, p)
	}
	if m, _ := modelOf(nil); m != "" {
		t.Error("nil params")
	}
	dir := weightsDir(t, 1000)
	os.Symlink(filepath.Join(dir, "model.safetensors"), filepath.Join(dir, "linked.safetensors")) // a snapshot links to blobs
	if n := weightsBytes(dir); n != 2000 {
		t.Errorf("weights %d bytes, want 2000", n)
	}
	if weightsBytes("") != 0 || weightsBytes(filepath.Join(dir, "missing")) != 0 {
		t.Error("an unknown directory weighs nothing")
	}
}
