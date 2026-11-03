package services

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/store"
)

// bigDub saves a project of n lines, each with ten timed words and a
// Spanish translation: the shape a long video's dub has.
func bigDub(t *testing.T, e *dubEnv, n int) string {
	t.Helper()
	return e.create(t, func(p *dub.Project) {
		fillDub(p, n)
	}).ID
}

func fillDub(p *dub.Project, n int) {
	for i := range n {
		sg := dub.Segment{ID: fmt.Sprintf("s%d", i+1), Start: float64(i) * 3, End: float64(i)*3 + 2.5, Speaker: "S1",
			Text: "This is a sentence of about the usual length that people say.", Volume: 1,
			Lines: map[string]dub.Line{"es": {Text: "Esta es una frase de la longitud habitual que dice la gente."}}}
		for w := range 10 {
			sg.Words = append(sg.Words, dub.Word{Text: "word", Start: float64(i)*3 + float64(w)*0.25, End: float64(i)*3 + float64(w)*0.25 + 0.2})
		}
		p.Segments = append(p.Segments, sg)
	}
	p.NextSeg = n + 1
}

// updateCost is the time and allocations of one Update that edits a
// line, averaged over runs.
func updateCost(t testing.TB, e *dubEnv, id string, runs int) (time.Duration, uint64) {
	t.Helper()
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	for i := range runs {
		if _, err := e.dubs.Update(ctx, id, func(p *dub.Project) error {
			l := p.Segments[7].Lines["es"]
			l.Text = fmt.Sprintf("Edit %d", i)
			p.Segments[7].Lines["es"] = l
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	el := time.Since(t0)
	runtime.ReadMemStats(&m1)
	return el / time.Duration(runs), (m1.TotalAlloc - m0.TotalAlloc) / uint64(runs)
}

// countingDubs counts the reads and writes of a dub store.
type countingDubs struct {
	store.Dubs
	gets, puts int
}

func (c *countingDubs) Get(ctx context.Context, id string) (store.DubRecord, error) {
	c.gets++
	return c.Dubs.Get(ctx, id)
}

func (c *countingDubs) Put(ctx context.Context, d store.DubRecord) error {
	c.puts++
	return c.Dubs.Put(ctx, d)
}

// TestDubUpdateCost: editing a line of a 2000-line dub no longer reads
// the document back and decodes it (code review 1102 perf M6: 13 ms and
// 16 MB a change, 45 % of it the read); an Update that changes nothing
// writes and publishes nothing. What is saved is the project's encoding,
// and reads back the same.
func TestDubUpdateCost(t *testing.T) {
	e := newDubEnv(t, 0)
	id := bigDub(t, e, 2000)
	counted := &countingDubs{Dubs: e.dubs.cfg.Store}
	e.dubs.cfg.Store = counted
	updateCost(t, e, id, 2) // warm
	counted.gets, counted.puts = 0, 0
	el, alloc := updateCost(t, e, id, 10)
	t.Logf("2000 lines, Update: %.2f ms, %.1f MB a change", float64(el.Microseconds())/1000, float64(alloc)/(1<<20))
	if counted.gets != 0 || counted.puts != 10 {
		t.Errorf("10 edits read %d and wrote %d times, want 0 and 10", counted.gets, counted.puts)
	}
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	t0 := time.Now()
	for i := range 10 { // the pipeline's saves (stageCtx.save)
		if _, err := e.dubs.save(ctx, id, func(p *dub.Project) error { p.Segments[8].Text = fmt.Sprint(i); return nil }, true); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&m1)
	t.Logf("2000 lines, pipeline save: %.2f ms, %.1f MB a change", float64(time.Since(t0).Microseconds())/10000, float64(m1.TotalAlloc-m0.TotalAlloc)/10/(1<<20))
	runtime.GC()
	runtime.ReadMemStats(&m0)
	if _, _, err := e.dubs.load(ctx, id); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&m1)
	t.Logf("decoding the document (what each change no longer does): %.1f MB", float64(m1.TotalAlloc-m0.TotalAlloc)/(1<<20))

	// The old way, for comparison: read and decode, edit, save, copy twice.
	runtime.GC()
	runtime.ReadMemStats(&m0)
	t0 = time.Now()
	for i := range 10 {
		q, _, err := e.dubs.load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		q.Segments[8].Text = fmt.Sprint(i)
		doc, _ := json.Marshal(q)
		counted.Dubs.Put(ctx, store.DubRecord{ID: id, CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt, Doc: doc})
		q.Clone()
		q.Clone()
	}
	runtime.ReadMemStats(&m1)
	t.Logf("2000 lines, the old way: %.2f ms, %.1f MB a change", float64(time.Since(t0).Microseconds())/10000, float64(m1.TotalAlloc-m0.TotalAlloc)/10/(1<<20))
	e.dubs.cache.drop(id) // the old way wrote behind the cache
	if _, err := e.dubs.save(ctx, id, func(p *dub.Project) error { p.Segments[8].Text = "9!"; return nil }, true); err != nil {
		t.Fatal(err)
	}

	rec, _ := counted.Dubs.Get(ctx, id)
	got, _ := e.dubs.Get(ctx, id)
	if want, _ := json.Marshal(got); string(rec.Doc) != string(want) {
		t.Fatalf("saved document differs from the project's encoding")
	}
	fresh, err := NewDubs(DubsConfig{Store: counted.Dubs}).Get(ctx, id)
	again, _ := json.Marshal(fresh)
	if err != nil || string(again) != string(rec.Doc) || fresh.Segments[7].Lines["es"].Text != "Edit 9" || fresh.Segments[8].Text != "9!" {
		t.Fatalf("read back %v: differs from the cached project", err)
	}

	e.mu.Lock()
	before := e.changes
	e.mu.Unlock()
	puts := counted.puts
	p, err := e.dubs.Update(ctx, id, func(*dub.Project) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the bus delivers asynchronously
	e.mu.Lock()
	changes := e.changes
	e.mu.Unlock()
	if counted.puts != puts || changes != before || !p.UpdatedAt.Equal(got.UpdatedAt) {
		t.Errorf("a no-op Update wrote or published: %d writes, %d → %d changes, updated %v → %v", counted.puts-puts, before, changes, got.UpdatedAt, p.UpdatedAt)
	}
}
