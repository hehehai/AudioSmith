package main

// Test helpers of M2 (thread #17): the app with a fake OS side for
// dictation (internal/dictation.Fake), fake global shortcuts, a fake
// language model, and transcripts in the library.

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hehehai/actnowkit/sounds"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// fakeHotkeys records global shortcuts; press runs one.
type fakeHotkeys struct {
	mu   sync.Mutex
	keys map[string]func()
	fail map[string]bool // Register refuses these (taken by another app)
	log  []string
}

func newFakeHotkeys() *fakeHotkeys {
	return &fakeHotkeys{keys: map[string]func(){}, fail: map[string]bool{}}
}

func (h *fakeHotkeys) Register(acc string, fn func()) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, "+"+acc)
	if h.fail[acc] {
		return fmt.Errorf("cannot register %s", acc)
	}
	if _, ok := h.keys[acc]; ok {
		return fmt.Errorf("%s is already registered", acc)
	}
	h.keys[acc] = fn
	return nil
}

func (h *fakeHotkeys) Unregister(acc string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log = append(h.log, "-"+acc)
	delete(h.keys, acc)
}

func (h *fakeHotkeys) has(acc string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.keys[acc]
	return ok
}

// press plays a shortcut being pressed.
func (h *fakeHotkeys) press(t *testing.T, acc string) {
	t.Helper()
	h.mu.Lock()
	fn := h.keys[acc]
	h.mu.Unlock()
	if fn == nil {
		t.Fatalf("%s is not registered (%v)", acc, h.log)
	}
	fn()
}

// fakeLLM is a language-model backend that answers with a function.
type fakeLLM struct {
	local  bool
	answer func(req llm.Request) (string, error)
	calls  int
}

func (f *fakeLLM) Info() llm.BackendInfo {
	return llm.BackendInfo{ID: "fake", Transport: "fake", Local: f.local, JSONMode: llm.JSONPromptMode}
}

func (f *fakeLLM) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	f.calls++
	text, err := f.answer(req)
	return llm.Response{Text: text, Model: "fake-1", Provider: "fake"}, err
}

func (f *fakeLLM) Models(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "fake-1"}, {ID: "fake-2"}}, nil
}

// useFakeLLM routes writing help to a fake backend (chosen in the
// settings, so later settings changes keep it).
func useFakeLLM(a *app, f *fakeLLM) {
	a.llmReg.Register("fake", f)
	a.updateSettings(func(s *settings.Settings) {
		s.LLM.Manual, s.LLM.Default = true, []settings.LLMRoute{{Provider: "fake"}}
	})
	a.applyLLM()
}

// m2Test is an app with the fake OS side of dictation.
type m2Test struct {
	a    *app
	eng  *fakeEngine
	tt   *ui.Tester
	sys  *dictation.Fake
	keys *fakeHotkeys
}

func newM2Test(t *testing.T) *m2Test {
	t.Helper()
	saved := llmSecrets
	llmSecrets = llm.StaticSecrets{}
	t.Cleanup(func() { llmSecrets = saved })
	savedSleep := dictSleep
	dictSleep = func(context.Context, time.Duration) {}
	t.Cleanup(func() { dictSleep = savedSleep })
	savedDir := trExportDir
	trExportDir = t.TempDir()
	t.Cleanup(func() { trExportDir = savedDir })

	a, eng, tt := newTestApp(t)
	completeAux(t, a)
	m := &m2Test{a: a, eng: eng, tt: tt, sys: dictation.NewFake(), keys: newFakeHotkeys()}
	a.dictSys, a.dictClip, a.dictKeys = m.sys, m.sys, m.keys
	// Dictation's sounds as a source build has them (none of the family's:
	// system sounds, which m.sys records), whatever resources/sounds holds;
	// useTestSounds gives the family's.
	a.dictSoundCat, a.dictPlayer = nil, sounds.NewPlayer(nil, sounds.Silent{})
	m.sys.OnHotkeyReleased(func() { a.post(a.dictReleased) })
	a.applyDictShortcut()
	a.recheckPermissions()
	a.refreshModels()
	return m
}

// sampleTranscript is a transcript of a 6 s clip with timed words.
func sampleTranscript(id string, at time.Time) core.Transcript {
	return core.Transcript{ID: id, CreatedAt: at, UpdatedAt: at, Title: "interview.m4a", Source: core.SourceFile,
		Mode: "accurate", Model: "mlx-community/whisper-large-v3-turbo-4bit", Aligner: "mlx-community/Qwen3-ForcedAligner-0.6B-4bit",
		Language: "en", Duration: 6, Audio: id + ".wav", Raw: "Hello there. General Kenobi.",
		Segments: []core.TranscriptSegment{
			{Start: 0.2, End: 1.4, Text: "Hello there.", Words: []core.TranscriptWord{{Text: "Hello", Start: 0.2, End: 0.7}, {Text: "there.", Start: 0.8, End: 1.4}}},
			{Start: 2, End: 3.6, Text: "General Kenobi.", Words: []core.TranscriptWord{{Text: "General", Start: 2, End: 2.6}, {Text: "Kenobi.", Start: 2.7, End: 3.6}}},
		}}
}

// putTranscript saves a transcript (and its audio) in the library, as a
// finished transcription would, and announces it.
func putTranscript(t *testing.T, a *app, tr core.Transcript) core.Transcript {
	t.Helper()
	st := a.lib.store.Transcripts()
	if tr.Audio != "" {
		os.MkdirAll(st.AudioDir(), 0o755)
		writeTestWAV(t, filepath.Join(st.AudioDir(), tr.Audio), services.TranscriptRate, tr.Duration)
	}
	if err := st.Put(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	a.lib.bus.Publish(core.TranscriptChanged{Transcript: tr})
	return tr
}

// removeModel deletes a catalogue entry's snapshot from the test model
// folder, so it reads as not downloaded.
func removeModel(t *testing.T, a *app, id string) catalog.Entry {
	t.Helper()
	e, _ := catalog.Builtin().ByID(id)
	for _, repo := range []string{e.Repo, e.EffectiveRepo(currentOS())} {
		if repo == "" {
			continue
		}
		if err := os.RemoveAll(hub.RepoDir(modelCache(a.prefs), repo)); err != nil {
			t.Fatal(err)
		}
	}
	a.refreshModel(e)
	return e
}

// completeAux writes the listed files of every auxiliary repo in the test
// model folder (useLocalModels writes a config and weights only), so the
// speech-to-text models, whose tokenizer is an auxiliary repo, read as
// downloaded.
func completeAux(t *testing.T, a *app) {
	t.Helper()
	dir := modelCache(a.prefs)
	for _, e := range catalog.Builtin().Entries {
		for _, x := range e.Aux {
			snap := filepath.Join(hub.RepoDir(dir, x.Repo), "snapshots", x.Revision)
			for _, f := range x.Files {
				p := filepath.Join(snap, filepath.FromSlash(f.Path))
				os.MkdirAll(filepath.Dir(p), 0o755)
				fh, err := os.Create(p)
				if err != nil {
					t.Fatal(err)
				}
				fh.Truncate(f.Size)
				fh.Close()
			}
		}
	}
	a.resetHub()
}

func jsonUnmarshal(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func splitComma(s string) []string { return strings.Split(s, ",") }
