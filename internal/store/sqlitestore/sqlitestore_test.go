package sqlitestore

// These tests need the plugin's native SQLite library (MyGo's Sqlitecore
// module). Outside a packaged app it is downloaded once (SHA-256 checked)
// into os.UserCacheDir()/mygo/natives; see the README's Tests.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

var ctx = context.Background()

func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSplitSQL(t *testing.T) {
	got := splitSQL(`-- a comment; with a semicolon;
CREATE TABLE a (x);

CREATE TRIGGER t AFTER INSERT ON a BEGIN
  INSERT INTO b VALUES (1);
  INSERT INTO b VALUES (2);
END;
-- trailing;
CREATE INDEX i ON a(x);
`)
	if len(got) != 3 || !strings.HasPrefix(got[1], "CREATE TRIGGER") || !strings.HasSuffix(got[1], "END;") {
		t.Fatalf("%q", got)
	}
	// Every statement of the real migrations prepares on its own.
	if n := len(migrations[0].statements); n < 20 {
		t.Errorf("migration 1 has %d statements", n)
	}
}

func TestOpenMigratesAndReopens(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	if v, _ := userVersion(ctx, s.db); v != Version() {
		t.Fatalf("user_version %d", v)
	}
	for _, sub := range []string{"clips", "voices"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v", sub, err)
		}
	}
	s.Close()
	s = openTest(t, dir)
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.bak-*")); len(matches) != 0 {
		t.Errorf("backups without a migration: %v", matches)
	}
}

func TestBackupBeforeMigrating(t *testing.T) {
	dir := t.TempDir()
	saved := migrations
	t.Cleanup(func() { migrations = saved })

	migrations = saved[:1]
	s := openTest(t, dir)
	if err := s.Documents().Put(ctx, "k", []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A second migration: the file is copied first.
	migrations = append(slices.Clone(saved[:1]), migration{version: 2, statements: []string{"CREATE TABLE extra (x)"}})
	s = openTest(t, dir)
	if v, _ := userVersion(ctx, s.db); v != 2 {
		t.Fatalf("user_version %d", v)
	}
	bak := filepath.Join(dir, FileName+".bak-1")
	if _, err := os.Stat(bak); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// The backup is the version-1 database, with its data.
	migrations = saved[:1]
	old := t.TempDir()
	if err := copyFile(bak, filepath.Join(old, FileName)); err != nil {
		t.Fatal(err)
	}
	b := openTest(t, old)
	if v, _ := userVersion(ctx, b.db); v != 1 {
		t.Errorf("backup version %d", v)
	}
	if doc, ok, _ := b.Documents().Get(ctx, "k"); !ok || string(doc) != `{"v":1}` {
		t.Errorf("backup data %s %v", doc, ok)
	}

	// A database newer than the app is refused, untouched.
	if _, err := Open(ctx, dir); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("newer database: %v", err)
	}

	// A failing migration rolls back and leaves the version as it was.
	migrations = append(slices.Clone(saved[:1]), migration{version: 2, statements: []string{"CREATE TABLE ok (x)", "NOT SQL"}})
	fresh := t.TempDir()
	if _, err := Open(ctx, fresh); err == nil {
		t.Fatal("bad migration succeeded")
	}
	migrations = saved[:1]
	s = openTest(t, fresh)
	if rows, _ := s.db.Query(ctx, "SELECT name FROM sqlite_master WHERE name = 'ok'"); len(rows.Values) != 0 {
		t.Error("partial migration kept")
	}
	if v, _ := userVersion(ctx, s.db); v != 1 {
		t.Errorf("version after failed migration %d", v)
	}
}

func take(id string, at time.Time, text string) core.Take {
	return core.Take{ID: id, CreatedAt: at, File: id + ".wav", Text: text, Model: "m/kokoro", Voice: "af_heart",
		Speed: 1.25, SampleRate: 24000, AudioSeconds: 2, GenSeconds: 0.5}
}

func TestTakes(t *testing.T) {
	s := openTest(t, t.TempDir())
	r := s.Takes()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	takes := []core.Take{
		take("a", t0, "Hello from AudioSmith"),
		take("b", t0.Add(time.Second), "你好，这是在 Mac 上生成的语音。"),
		take("c", t0.Add(2*time.Second), "A 100% literal_match test"),
	}
	takes[2].Model = "m/qwen"
	for _, tk := range takes {
		os.WriteFile(r.AudioPath(tk), []byte("RIFF"), 0o644)
		if err := r.Put(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(q core.TakeQuery) string {
		t.Helper()
		got, err := r.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, tk := range got {
			s = append(s, tk.ID)
		}
		return strings.Join(s, ",")
	}
	for _, c := range []struct {
		q    core.TakeQuery
		want string
	}{
		{core.TakeQuery{}, "c,b,a"},
		{core.TakeQuery{Search: "hello"}, "a"},           // FTS, case-insensitive
		{core.TakeQuery{Search: "AUDIOSMITH"}, "a"},      // substring inside a word
		{core.TakeQuery{Search: "生成的"}, "b"},             // Chinese, FTS
		{core.TakeQuery{Search: "你好"}, "b"},              // two characters: LIKE
		{core.TakeQuery{Search: "%"}, "c"},               // LIKE wildcards are literal
		{core.TakeQuery{Search: "%l"}, ""},               // escaped, not "contains l"
		{core.TakeQuery{Search: `"quoted`}, ""},          // FTS syntax is literal too
		{core.TakeQuery{Model: "m/qwen"}, "c"},           //
		{core.TakeQuery{Limit: 2}, "c,b"},                //
		{core.TakeQuery{Limit: 1, Offset: 1}, "b"},       //
		{core.TakeQuery{Offset: 2}, "a"},                 //
		{core.TakeQuery{Starred: true}, ""},              //
		{core.TakeQuery{Search: "nothing here"}, ""},     //
		{core.TakeQuery{Search: "hello", Limit: 5}, "a"}, //
	} {
		if got := ids(c.q); got != c.want {
			t.Errorf("%+v: got %q want %q", c.q, got, c.want)
		}
	}

	// Get round-trips the whole recipe.
	got, err := r.Get(ctx, "a")
	if err != nil || got.Text != takes[0].Text || got.Speed != 1.25 || !got.CreatedAt.Equal(t0) || got.RTF() != 4 {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := r.Get(ctx, "zz"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}

	// Update in place: the index follows the new text, starred filters.
	got.Text, got.Starred = "Rewritten words", true
	if err := r.Put(ctx, got); err != nil {
		t.Fatal(err)
	}
	if ids(core.TakeQuery{Search: "hello"}) != "" || ids(core.TakeQuery{Search: "rewritten"}) != "a" || ids(core.TakeQuery{Starred: true}) != "a" {
		t.Error("index or starred not updated")
	}
	if ids(core.TakeQuery{}) != "c,b,a" {
		t.Error("update changed the order")
	}

	// Relations: an unknown voice is refused (foreign key).
	bad := takes[1]
	bad.VoiceID = "nope"
	if err := r.Put(ctx, bad); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("unknown voice: %v", err)
	}
	if err := r.Put(ctx, core.Take{ID: "../x", File: "x.wav"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}

	// Delete removes the row, its index entry and its audio.
	if err := r.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.AudioPath(takes[1])); !os.IsNotExist(err) {
		t.Error("audio remains")
	}
	if ids(core.TakeQuery{Search: "生成的"}) != "" || ids(core.TakeQuery{}) != "c,a" {
		t.Error("deleted take still listed")
	}
	if err := r.Delete(ctx, "b"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// The FTS index is consistent with its table.
	if _, err := s.db.Execute(ctx, "INSERT INTO takes_fts(takes_fts, rank) VALUES ('integrity-check', 1)"); err != nil {
		t.Errorf("fts integrity: %v", err)
	}
}

func TestVoicesAndRelations(t *testing.T) {
	s := openTest(t, t.TempDir())
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	v := core.Voice{ID: "v1", Name: "Narrator", Kind: core.VoiceCloned, Model: "m/qwen", RefAudio: "ref.wav", CreatedAt: now, UpdatedAt: now, Tags: []string{"warm"}}
	if err := s.Voices().Put(ctx, v); err != nil {
		t.Fatal(err)
	}
	s.Voices().Put(ctx, core.Voice{ID: "v2", Name: "any model", Kind: core.VoiceDesigned, CreatedAt: now, UpdatedAt: now})
	s.Voices().Put(ctx, core.Voice{ID: "v3", Name: "Kokoro Heart", Kind: core.VoiceBuiltin, Model: "m/kokoro", CreatedAt: now, UpdatedAt: now})
	if err := s.Voices().Put(ctx, core.Voice{ID: "v4", Name: "x", Kind: "weird"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	names := func(q core.VoiceQuery) []string {
		vs, err := s.Voices().List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}
	if got := names(core.VoiceQuery{}); !slices.Equal(got, []string{"v2", "v3", "v1"}) {
		t.Errorf("all %v", got)
	}
	if got := names(core.VoiceQuery{Model: "m/qwen"}); !slices.Equal(got, []string{"v2", "v1"}) {
		t.Errorf("model %v", got)
	}
	if got := names(core.VoiceQuery{Search: "narr", Kind: core.VoiceCloned}); !slices.Equal(got, []string{"v1"}) {
		t.Errorf("search %v", got)
	}
	got, err := s.Voices().Get(ctx, "v1")
	if err != nil || got.RefAudio != "ref.wav" || got.Tags[0] != "warm" {
		t.Fatalf("%+v %v", got, err)
	}

	// A take spoken with v1; deleting v1 clears the relation and its blobs.
	os.MkdirAll(filepath.Join(s.Voices().VoiceDir(), "v1"), 0o755)
	os.WriteFile(filepath.Join(s.Voices().VoiceDir(), "v1", "ref.wav"), []byte("x"), 0o644)
	tk := take("t1", now, "Spoken by the narrator")
	tk.VoiceID = "v1"
	if err := s.Takes().Put(ctx, tk); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.Takes().List(ctx, core.TakeQuery{VoiceID: "v1"}); len(l) != 1 {
		t.Fatal("voice filter")
	}
	if err := s.Voices().Delete(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Voices().VoiceDir(), "v1")); !os.IsNotExist(err) {
		t.Error("voice blobs remain")
	}
	if tk, _ := s.Takes().Get(ctx, "t1"); tk.VoiceID != "" {
		t.Errorf("take still points at the deleted voice: %q", tk.VoiceID)
	}
	if err := s.Voices().Delete(ctx, "v1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestProjectsLicencesDocuments(t *testing.T) {
	s := openTest(t, t.TempDir())
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	p := core.Project{ID: "p1", Kind: core.ProjectStory, Title: "The long night", CreatedAt: now, UpdatedAt: now}
	if err := s.Projects().Put(ctx, p); err != nil {
		t.Fatal(err)
	}
	s.Projects().Put(ctx, core.Project{ID: "p2", Kind: core.ProjectDub, Title: "Trailer", Body: json.RawMessage(`{"version":1}`), CreatedAt: now, UpdatedAt: now.Add(time.Hour)})
	if err := s.Projects().Put(ctx, core.Project{ID: "p3", Kind: core.ProjectDub, Body: json.RawMessage(`{`)}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("bad body: %v", err)
	}
	all, _ := s.Projects().List(ctx, "", "")
	if len(all) != 2 || all[0].ID != "p2" || string(all[0].Body) != `{"version":1}` || string(all[1].Body) != "{}" {
		t.Fatalf("projects %+v", all)
	}
	if l, _ := s.Projects().List(ctx, core.ProjectStory, "night"); len(l) != 1 || l[0].ID != "p1" {
		t.Errorf("filter %+v", l)
	}
	// A take in a project survives the project, unlinked.
	tk := take("t1", now, "Chapter one")
	tk.ProjectID = "p1"
	s.Takes().Put(ctx, tk)
	if err := s.Projects().Delete(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if tk, err := s.Takes().Get(ctx, "t1"); err != nil || tk.ProjectID != "" {
		t.Errorf("take after project delete: %+v %v", tk, err)
	}
	if _, err := s.Projects().Get(ctx, "p1"); !errors.Is(err, core.ErrNotFound) {
		t.Error(err)
	}

	l := s.Licences()
	l.Put(ctx, core.LicenceAcceptance{Licence: "cc-by-nc-4.0", Model: "m/omni", AcceptedAt: now})
	l.Put(ctx, core.LicenceAcceptance{Licence: "cc-by-nc-4.0", AcceptedAt: now})
	l.Put(ctx, core.LicenceAcceptance{Licence: "cc-by-nc-4.0", Model: "m/omni", AcceptedAt: now.Add(time.Hour)})
	acc, _ := l.List(ctx)
	if len(acc) != 2 || acc[1].Model != "m/omni" || !acc[1].AcceptedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("licences %+v", acc)
	}
	l.Delete(ctx, "cc-by-nc-4.0", "")
	if acc, _ := l.List(ctx); len(acc) != 1 {
		t.Errorf("after revoke %+v", acc)
	}

	d := s.Documents()
	if _, ok, err := d.Get(ctx, "settings:app"); ok || err != nil {
		t.Fatal(ok, err)
	}
	d.Put(ctx, "settings:app", []byte(`{"version":1}`))
	d.Put(ctx, "settings:app", []byte(`{"version":2}`))
	if doc, ok, _ := d.Get(ctx, "settings:app"); !ok || string(doc) != `{"version":2}` {
		t.Errorf("doc %s", doc)
	}
	if err := d.Put(ctx, "x", []byte("not json")); !errors.Is(err, core.ErrInvalid) {
		t.Error(err)
	}
	d.Delete(ctx, "settings:app")
	if _, ok, _ := d.Get(ctx, "settings:app"); ok {
		t.Error("deleted document remains")
	}
}

func TestJobStore(t *testing.T) {
	s := openTest(t, t.TempDir())
	js := s.Jobs()
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	a := jobs.Job{ID: "j1", Spec: jobs.Spec{Kind: "tts.synthesize", Lane: jobs.LaneGPU, Priority: jobs.PriorityInteractive,
		Params: json.RawMessage(`{"model":"m","text":"hi"}`), Title: "Generate take", Owner: "take:t1", MaxAttempts: 2, Idempotent: true},
		State: jobs.StateQueued, Created: t0}
	// The dependent job is saved first: graph nodes may arrive in any order.
	b := jobs.Job{ID: "j2", Spec: jobs.Spec{Kind: "take.export", Lane: jobs.LaneCPU, Owner: "project:p1", After: []string{"j1", "j0"}},
		State: jobs.StateWaiting, Created: t0.Add(time.Second)}
	if err := js.SaveJob(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := js.SaveJob(ctx, a); err != nil {
		t.Fatal(err)
	}
	load := func(f jobs.Filter) []jobs.Job {
		t.Helper()
		l, err := js.LoadJobs(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	got := load(jobs.Filter{})
	if len(got) != 2 || got[0].ID != "j1" || got[1].ID != "j2" {
		t.Fatalf("jobs %+v", got)
	}
	if g := got[0]; g.Kind != a.Kind || g.Lane != jobs.LaneGPU || g.Priority != jobs.PriorityInteractive ||
		string(g.Params) != string(a.Params) || g.Title != a.Title || g.MaxAttempts != 2 || !g.Idempotent ||
		!g.Created.Equal(t0) || !g.Started.IsZero() || g.Err != nil || g.Result != nil || g.After != nil {
		t.Errorf("round trip %+v", g)
	}
	if !slices.Equal(got[1].After, []string{"j1", "j0"}) || got[1].MaxAttempts != 1 {
		t.Errorf("deps %+v", got[1])
	}

	// Run and fail j1; succeed nothing.
	a.State, a.Attempt, a.Started = jobs.StateRunning, 1, t0.Add(2*time.Second)
	a.Progress = jobs.Progress{Stage: "generating", Fraction: 0.5, AudioSeconds: 1.5}
	js.SaveJob(ctx, a)
	a.Progress.Fraction = 0.75 // a progress tick: no event
	js.SaveJob(ctx, a)
	a.State, a.Finished = jobs.StateFailed, t0.Add(3*time.Second)
	a.Err = &jobs.Error{Code: "oom", Message: "out of memory", Retryable: true, Data: json.RawMessage(`{"bytes":1}`)}
	js.SaveJob(ctx, a)

	if l := load(jobs.Filter{}); len(l) != 1 || l[0].ID != "j2" {
		t.Errorf("unfinished %+v", l)
	}
	f := load(jobs.Filter{States: []jobs.State{jobs.StateFailed}})
	if len(f) != 1 || f[0].Err == nil || f[0].Err.Code != "oom" || !f[0].Err.Retryable || string(f[0].Err.Data) != `{"bytes":1}` ||
		f[0].Progress.Fraction != 0.75 || f[0].Attempt != 1 || !f[0].Finished.Equal(a.Finished) {
		t.Errorf("failed %+v", f)
	}
	if l := load(jobs.Filter{States: []jobs.State{}, Owner: "take:"}); len(l) != 1 || l[0].ID != "j1" {
		t.Errorf("owner prefix %+v", l)
	}
	if l := load(jobs.Filter{States: []jobs.State{}, Owner: "take"}); len(l) != 0 {
		t.Errorf("owner exact %+v", l)
	}
	if l := load(jobs.Filter{States: []jobs.State{}, Kind: "take.export", Lane: jobs.LaneCPU}); len(l) != 1 {
		t.Errorf("kind/lane %+v", l)
	}
	if l := load(jobs.Filter{States: []jobs.State{}, Since: t0.Add(time.Second)}); len(l) != 1 || l[0].ID != "j2" {
		t.Errorf("since %+v", l)
	}
	if l := load(jobs.Filter{States: []jobs.State{}, Limit: 1}); len(l) != 1 || l[0].ID != "j1" {
		t.Errorf("limit %+v", l)
	}

	// Deps are replaced, not merged.
	b.After = []string{"j1"}
	js.SaveJob(ctx, b)
	if l := load(jobs.Filter{Owner: "project:p1"}); !slices.Equal(l[0].After, []string{"j1"}) {
		t.Errorf("deps after resave %v", l[0].After)
	}

	// Events: one per state change, with the failure's code.
	ev, err := s.JobEvents(ctx, "j1")
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, e := range ev {
		states = append(states, string(e.State)+":"+e.Detail)
	}
	if !slices.Equal(states, []string{"queued:", "running:", "failed:oom"}) {
		t.Errorf("events %v", states)
	}

	// Housekeeping deletes finished jobs older than the cut-off only.
	js.DeleteJobs(ctx, t0.Add(3*time.Second))
	if l := load(jobs.Filter{States: []jobs.State{}}); len(l) != 2 {
		t.Errorf("deleted too early: %d", len(l))
	}
	js.DeleteJobs(ctx, t0.Add(time.Hour))
	if l := load(jobs.Filter{States: []jobs.State{}}); len(l) != 1 || l[0].ID != "j2" {
		t.Errorf("after housekeeping %+v", l)
	}
	if ev, _ := s.JobEvents(ctx, "j1"); len(ev) != 0 {
		t.Error("events of a deleted job remain")
	}
	if err := js.SaveJob(ctx, jobs.Job{ID: "x"}); !errors.Is(err, core.ErrInvalid) {
		t.Error(err)
	}
}

// TestConcurrentUse hammers one store from several goroutines (run with -race).
func TestConcurrentUse(t *testing.T) {
	s := openTest(t, t.TempDir())
	t0 := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 25 {
				id := core.NewID(t0.Add(time.Duration(g*100+i) * time.Millisecond))
				if err := s.Takes().Put(ctx, take(id, t0, "concurrent take")); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.Takes().List(ctx, core.TakeQuery{Search: "concurrent", Limit: 5}); err != nil {
					t.Error(err)
					return
				}
				s.Documents().Put(ctx, "draft:x", []byte(`{}`))
			}
		})
	}
	wg.Wait()
	if l, _ := s.Takes().List(ctx, core.TakeQuery{}); len(l) != 100 {
		t.Errorf("%d takes", len(l))
	}
}

// #101 L2: LoadJob reads one job by id, whatever its state.
func TestLoadJob(t *testing.T) {
	s := openTest(t, t.TempDir())
	js := s.Jobs().(jobs.JobLoader)
	j := jobs.Job{ID: "j1", Spec: jobs.Spec{Kind: "k", Lane: jobs.LaneCPU, After: []string{"j0"}}, State: jobs.StateSucceeded,
		Created: time.UnixMilli(1000), Finished: time.UnixMilli(2000), Result: json.RawMessage(`{}`)}
	if err := s.Jobs().SaveJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, ok, err := js.LoadJob(ctx, "j1")
	if err != nil || !ok || got.State != jobs.StateSucceeded || !slices.Equal(got.After, []string{"j0"}) {
		t.Fatalf("LoadJob: %+v %v %v", got, ok, err)
	}
	if _, ok, err := js.LoadJob(ctx, "none"); ok || err != nil {
		t.Errorf("LoadJob of an unknown id: %v %v", ok, err)
	}
}
