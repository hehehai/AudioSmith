package main

// View tests of the M1 Studio: the composer's controls, the recipe they
// make, the streaming preview and chunk progress, the output panel, the
// History's tools and Settings → Pronunciation.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/text/lexicon"
)

// The form's output options, seed and steps go into the recipe.
func TestRecipeCarriesOutputOptions(t *testing.T) {
	a, eng, tt := newTestApp(t)
	tt.Frame()
	tt.Click("Generate")
	r := renderOf(t, eng.last(t))
	if r.Take.Render == nil || r.Take.Render.Master != services.MasterDefault || r.Take.Render.Bits != 24 ||
		r.Take.Render.NoNormalize || r.Take.Seed != 0 || !r.Stream || r.Lang != "en-US" {
		t.Fatalf("defaults %+v %+v", r, r.Take.Render)
	}
	eng.cancelled(eng.last(t).ID)

	a.selectModel(entry(t, "omnivoice-bf16"))
	a.acceptLicenceForTest(t)
	a.evenOut, a.normalize = false, false
	a.seedPinned, a.seed, a.steps = true, 1234, 48
	tt.Frame()
	tt.Click("Generate")
	r = renderOf(t, eng.last(t))
	if rr := r.Take.Render; rr.Bits != 24 || rr.Master != "" || !rr.NoNormalize || !rr.SeedPinned || r.Take.Seed != 1234 || r.Take.Steps != 48 {
		t.Fatalf("options %+v %+v", r.Take, rr)
	}
	// Reuse brings all of it back.
	tk := r.Take
	a.evenOut, a.normalize, a.seedPinned, a.steps = true, true, false, 0
	a.reuse(&tk)
	if a.evenOut || a.normalize || !a.seedPinned || a.seed != 1234 || a.steps != 48 || a.preset().ID != "omnivoice-bf16" {
		t.Errorf("reuse: even %v norm %v seed %v/%v steps %v", a.evenOut, a.normalize, a.seedPinned, a.seed, a.steps)
	}
}

// acceptLicenceForTest accepts the licence of the Studio's model.
func (a *app) acceptLicenceForTest(t *testing.T) {
	t.Helper()
	e := a.preset()
	if err := a.licences.Accept(context.Background(), core.LicenceAcceptance{Licence: e.Licence.ID, Model: e.Repo, AcceptedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// TestStudioAdvanced: the Studio's 「高级」 holds what the old Voice
// controls and Audio quality popovers held, as the model takes it, opens
// and closes per page (remembered), and Restore recommended clears it.
func TestStudioAdvanced(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1100, 1000) // OmniVoice's 「高级」 in view
	tt.Frame()
	if tt.HasText("Even out volume") || !tt.HasText("Recommended settings") {
		t.Fatalf("closed: %q", tt.Texts())
	}
	tt.Click("Advanced")
	tt.Frame()
	if !a.advancedOpen("studio") || !tt.HasText("Even out volume") || !tt.HasText("Pin the seed") || tt.HasText("Voice refinement") || tt.HasText("WAV precision") {
		t.Fatalf("Kokoro's advanced: %q", tt.Texts())
	}
	a.evenOut = false
	tt.Frame()
	if !tt.HasText("1 change") {
		t.Errorf("changes: %q", tt.Texts())
	}

	a.selectModel(entry(t, "omnivoice-bf16"))
	tt.Frame()
	if !tt.HasText("Voice refinement") || !tt.HasText("Prompt guidance  1.5") {
		t.Fatalf("OmniVoice's advanced: %q", tt.Texts())
	}
	tt.Click("Quality")
	tt.Frame()
	if a.steps != services.StepsQuality {
		t.Errorf("steps %v", a.steps)
	}
	if err := tt.Click("Restore recommended"); err != nil {
		t.Fatal(err)
	}
	if a.steps != 0 || !a.evenOut {
		t.Errorf("after restore: %v %v", a.steps, a.evenOut)
	}
}

func TestInsertMenuIsModelAware(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.text = "Hello"
	tt.Frame()
	tt.Key(ui.Alt, ui.KeySlash)
	tt.Frame()
	if !a.composer.insertOpen || !tt.HasText("One second") || !tt.HasText("Kokoro 82M cannot voice expressions; they are left out.") {
		t.Fatalf("insert menu: %q", tt.Texts())
	}
	tt.Click("Laughter") // disabled for Kokoro
	if strings.Contains(a.text, "[laughter]") {
		t.Error("Kokoro inserted an expression")
	}
	tt.Click("One second")
	tt.Frame()
	if !strings.Contains(a.text, "[pause 1s]") || a.composer.insertOpen {
		t.Errorf("text %q open %v", a.text, a.composer.insertOpen)
	}

	a.selectModel(entry(t, "omnivoice-bf16"))
	tt.Frame()
	tt.Click("Insert")
	tt.Frame()
	tt.Click("Laughter")
	tt.Frame()
	if !strings.Contains(a.text, "[laughter]") {
		t.Errorf("OmniVoice: %q", a.text)
	}
	// Undo takes an insertion back.
	a.undoScript()
	if strings.Contains(a.text, "[laughter]") {
		t.Errorf("after undo %q", a.text)
	}
}

func TestLanguagePicker(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.selectModel(entry(t, "qwen3-tts-0.6b-customvoice-8bit"))
	tt.Frame()
	tt.Click("Auto-detect") // the recommended language: detected
	tt.Frame()
	if !tt.HasText("Auto-detect") || !tt.HasText("Not supported by “Standard voices”") {
		t.Fatalf("picker: %q", tt.Texts())
	}
	a.composer.langQuery = "japan"
	tt.Frame()
	tt.Click("Japanese · 日本語")
	tt.Frame()
	if a.language != "Japanese" || a.recentLangs[0] != "ja" || a.composer.langOpen {
		t.Fatalf("language %q recent %v open %v", a.language, a.recentLangs, a.composer.langOpen)
	}
	// An unsupported language cannot be chosen.
	tt.Click("🇯🇵 Japanese")
	a.composer.langQuery = "arabic"
	tt.Frame()
	tt.Click("Arabic · العربية")
	if a.language != "Japanese" {
		t.Errorf("chose an unsupported language: %q", a.language)
	}
	// Recent languages come first.
	a.composer.langQuery = ""
	tt.Frame()
	tt.Click("Auto-detect")
	if a.language != "" {
		t.Errorf("auto: %q", a.language)
	}
	tt.Click("Auto-detect")
	tt.Frame()
	tt.Click("🇯🇵 Japanese")
	if a.language != "Japanese" {
		t.Errorf("recent: %q", a.language)
	}
	// OmniVoice takes codes from its long list.
	a.selectModel(entry(t, "omnivoice-bf16"))
	tt.Frame()
	tt.Click("Auto-detect")
	a.composer.langQuery = "swahili"
	tt.Frame()
	tt.Click("Swahili · Kiswahili")
	if a.language != "sw" {
		t.Errorf("omnivoice: %q", a.language)
	}
}

func TestScriptStatsAndDiagnostics(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.text = "Ha [laughter] that is [voice:Ann] funny. " + strings.Repeat("A long sentence for many chunks. ", 30)
	tt.Frame()
	st := a.scriptStats()
	if st.chunks < 3 || st.seconds < 30 || len(st.diags) != 2 {
		t.Fatalf("stats %+v", st)
	}
	// The notes are a short warning (0.3): its tooltip lists them, a click
	// shows them as a toast.
	if err := tt.Click("2 notes about the markup"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.Contains(strings.Join(tt.Texts(), "|"), "2 notes about the markup: This model cannot voice [laughter]; it is left out.") {
		t.Errorf("diagnostics: %q", tt.Texts())
	}
	a.text = ""
	tt.Frame()
	if !tt.HasText("Get started with") || tt.HasText("Type, paste or import a script.") { // the chips say it (0.5 F3)
		t.Errorf("readiness: %q", tt.Texts())
	}
	a.text = "[pause 1s]"
	tt.Frame()
	if !tt.HasText("The script has nothing to say: only markup.") {
		t.Errorf("readiness: %q", tt.Texts())
	}
	// A long OmniVoice script warns that it is slow.
	a.selectModel(entry(t, "omnivoice-bf16"))
	a.text = strings.Repeat("This is a sentence of a long chapter. ", 200)
	tt.Frame()
	if !tt.HasText("A long render: about 10 min 19 s of audio may take 3 min 26 s or more. It plays from the first chunk while the rest renders.") {
		t.Errorf("slow warning: %q", tt.Texts())
	}
}

func TestImportScriptAtTheCaret(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.text = "Before."
	tt.Frame()
	dir := t.TempDir()
	md := filepath.Join(dir, "chapter.md")
	os.WriteFile(md, []byte("# The title\n\nFirst *paragraph* here.\n\nSecond one."), 0o644)
	a.importScriptFrom(md)
	tt.Frame()
	if !strings.Contains(a.text, "The title\n\nFirst paragraph here.\n\nSecond one.") || !strings.HasPrefix(a.text, "Before.") && !strings.HasSuffix(a.text, "Before.") {
		t.Fatalf("text %q", a.text)
	}
	if !tt.HasText("Imported chapter.md · 47 characters") {
		t.Errorf("toast: %q", tt.Texts())
	}
	a.undoScript()
	tt.Frame()
	if a.text != "Before." {
		t.Errorf("undo: %q", a.text)
	}
	a.importScriptFrom(filepath.Join(dir, "book.pdf"))
	tt.Frame()
	if !tt.HasText("book.pdf: this kind of file cannot be imported (PDF comes later).") {
		t.Errorf("pdf: %q", tt.Texts())
	}
	srt := filepath.Join(dir, "subs.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello there.\n\n2\n00:00:03,000 --> 00:00:04,000\nGeneral Kenobi.\n"), 0o644)
	a.importScriptFrom(srt)
	if !strings.Contains(a.text, "Hello there.") || !strings.Contains(a.text, "General Kenobi.") {
		t.Errorf("srt: %q", a.text)
	}
}

// pcm16 is n frames of a quiet tone as pcm16le.
func pcm16(n int) []byte {
	b := make([]byte, 2*n)
	for i := range n {
		v := int16((i % 40) * 200)
		b[2*i], b[2*i+1] = byte(v), byte(v>>8)
	}
	return b
}

func chunkUpdate(t *testing.T, eng *fakeEngine, id string, seq int, data []byte, final bool) {
	t.Helper()
	raw, _ := json.Marshal(proto.ChunkParams{Job: id, Seq: seq, SampleRate: 24000, Channels: 1, Encoding: "pcm16le", Data: data, Final: final})
	eng.a.bus.Publish(jobs.Updated{Job: eng.jobs[id], Chunk: raw})
}

// The render's progress names the chunk, and its audio plays while it
// renders; the finished take does not restart it.
func TestStreamingPreviewAndChunkProgress(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.autoplay = true
	tt.Frame()
	tt.Click("Generate")
	req := eng.last(t)
	p := renderOf(t, req)
	eng.progress(req.ID, "generating", 0.2, 1)
	a.bus.Publish(services.RenderProgress{Job: req.ID, Owner: req.Spec.Owner, Chunk: 2, Chunks: 5, Cached: 1})
	tt.Frame()
	if !tt.HasText("Generating… · chunk 2 of 5 · 1 from the cache") {
		t.Errorf("progress: %q", tt.Texts())
	}
	chunkUpdate(t, eng, req.ID, 0, pcm16(4800), false)
	tt.Frame()
	if a.preview == nil || !a.pl.Streaming(p.Take.ID) || !tt.HasText("Pause preview") {
		t.Fatalf("preview not playing: %v %q", a.preview, tt.Texts())
	}
	chunkUpdate(t, eng, req.ID, 1, pcm16(2400), true)
	if a.preview.Seconds() != 0.3 || !a.preview.Done() {
		t.Errorf("preview %v s, done %v", a.preview.Seconds(), a.preview.Done())
	}
	// Another job's chunks are not the Studio's.
	other, _ := eng.host.sched.Submit(context.Background(), jobs.Spec{Kind: services.KindRender, Lane: jobs.LaneGPU, Owner: "take:other"})
	chunkUpdate(t, eng, other, 0, pcm16(2400), false)
	if a.preview.Seconds() != 0.3 {
		t.Error("another take's audio joined the preview")
	}
	finishRender(t, eng, req, 0.3)
	tt.Frame()
	if !a.pl.Streaming(p.Take.ID) || a.lastClipID != p.Take.ID {
		t.Errorf("the take restarted playback: streaming %v", a.pl.Streaming(p.Take.ID))
	}
	// Play on a finished preview plays the file.
	a.pl.Stop()
	a.play(a.lastClip())
	if a.pl.Streaming(p.Take.ID) || a.pl.Current() != p.Take.ID {
		t.Error("the file does not play")
	}
}

func TestOutputPanel(t *testing.T) {
	a, _, tt := newTestApp(t)
	tk := core.Take{ID: "T1", CreatedAt: time.Now(), Text: "A long take.", Model: kokoro, Voice: "af_heart", SampleRate: 24000,
		AudioSeconds: 1, GenSeconds: 0.4, Render: &core.TakeRender{Bits: 24, Channels: 1, Bytes: 72044, Master: "broadcast",
			Warnings: []core.TakeNotice{{Code: services.NoticeChunkShort, Severity: "warning", Chunk: 2, Start: 0.4, End: 0.6, Value: 0.2,
				Params: map[string]string{"expected": "2.5"}}}}}
	addTake(t, a, tk, 1)
	a.lastClipID = "T1"
	tt.Frame()
	// The Studio's latest take says who spoke it, how long, when (0.5 F3);
	// the file's details are in its tooltip and in Assets.
	for _, want := range []string{"Latest take", "af_heart · 1.0 s · ", "Another voice",
		"Chunk 2 is 0.2 s long where its text needs about 2.5 s: words may be missing.", "Check audio", "Export audio…"} {
		if !strings.Contains(strings.Join(tt.Texts(), "|"), want) {
			t.Errorf("lacks %q: %q", want, tt.Texts())
		}
	}
	// A warning seeks there.
	tt.Click("Chunk 2 is 0.2 s long where its text needs about 2.5 s: words may be missing.")
	if a.pl.Current() != "T1" {
		t.Error("the warning did not play the take")
	}
	// Check audio runs over the file and keeps what it found.
	tt.Click("Check audio")
	tt.Frame()
	got := a.findTake("T1")
	if got.Render == nil || !got.Render.QCDone || !a.checkOpen["T1"] {
		t.Fatalf("check: %+v", got.Render)
	}
	if !tt.HasText("No problems found: no clipping, silences or sudden jumps in loudness.") && !strings.Contains(strings.Join(tt.Texts(), "|"), "Check audio found") {
		t.Errorf("results: %q", tt.Texts())
	}
	stored, _ := a.lib.svc.Takes.Get(context.Background(), "T1")
	if stored.Render == nil || !stored.Render.QCDone || stored.Render.Bits != 24 {
		t.Errorf("stored %+v", stored.Render)
	}
	// Star.
	tt.Click("Star: keep this take")
	if !a.findTake("T1").Starred {
		t.Error("not starred")
	}
	// Export audio… opens the Export audio dialog (export_dialog.go).
	if err := tt.Click("Export audio…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.exp == nil || a.exp.take.ID != "T1" || !tt.HasText("Also save") {
		t.Errorf("no dialog: %q", tt.Texts())
	}
}

func TestHistoryStarFilterSearchAndDetails(t *testing.T) {
	a, _, tt := newTestApp(t)
	at := time.Now()
	addTake(t, a, core.Take{ID: "A", CreatedAt: at, Text: "The quick brown fox.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	addTake(t, a, core.Take{ID: "B", CreatedAt: at.Add(time.Second), Text: "你好，世界。", Model: "mlx-community/OmniVoice-bf16",
		Seed: 77, Steps: 48, AudioSeconds: 2, GenSeconds: 1,
		Render: &core.TakeRender{SeedPinned: true, Master: "broadcast", Chunks: []core.TakeChunk{{Text: "你好，", End: 1}, {Text: "世界。", Start: 1.2, End: 2, Cached: true}}}}, 0.3)
	a.showPage("history")
	tt.Frame()
	if !tt.HasText("2 takes") || !tt.HasText("The quick brown fox.") || !tt.HasText("你好，世界。") {
		t.Fatalf("history: %q", tt.Texts())
	}
	// Star A, then show only starred takes.
	a.setStarred(a.findTake("A"), true)
	a.history.filter = 1
	tt.Frame()
	if !tt.HasText("The quick brown fox.") || tt.HasText("你好，世界。") {
		t.Errorf("starred: %q", tt.Texts())
	}
	a.history.filter = 0
	// By model.
	a.history.model = "Multilingual all-rounder"
	tt.Frame()
	if tt.HasText("The quick brown fox.") || !tt.HasText("你好，世界。") {
		t.Errorf("by model: %q", tt.Texts())
	}
	a.history.model = ""
	// Search through the index.
	a.history.query = "brown"
	a.searchTakes()
	tt.Frame()
	if !tt.HasText("The quick brown fox.") || tt.HasText("你好，世界。") || !a.history.results["A"] {
		t.Errorf("search: %q", tt.Texts())
	}
	a.history.query = "nothing like it"
	a.searchTakes()
	tt.Frame()
	if !tt.HasText("No take matches") {
		t.Errorf("no match: %q", tt.Texts())
	}
	a.history.query = ""
	a.searchTakes()
	// Details: the whole recipe and the chunks.
	a.history.selected = "B"
	tt.Frame()
	for _, want := range []string{"Take details", "Recipe", "77 · pinned", "48", "Evened out (broadcast)", "2 chunks", "cached"} {
		if !tt.HasText(want) {
			t.Errorf("details lack %q: %q", want, tt.Texts())
		}
	}
	tt.Scroll(900, 500, 0, 2000) // the details pane, to its chunks
	tt.Frame()
	if err := tt.Click("世界。"); err != nil {
		t.Fatal(err)
	}
	if a.pl.Current() != "B" {
		r, _ := tt.Find("世界。")
		t.Errorf("a chunk does not play: %q at %v (%q)", a.pl.Current(), r, a.toasts)
	}
	if err := tt.Click("Load the whole recipe into the studio, seed included"); err != nil {
		tt.Click("Reuse")
	}
	a.reuse(a.findTake("B"))
	if a.currentPage() != "studio" || a.seed != 77 || !a.seedPinned || a.steps != 48 || a.text != "你好，世界。" {
		t.Errorf("reuse: seed %v steps %v text %q", a.seed, a.steps, a.text)
	}
}

func TestHistoryCapSetting(t *testing.T) {
	a, _, tt := newTestApp(t)
	at := time.Now()
	for i, id := range []string{"A", "B", "C", "D"} {
		addTake(t, a, core.Take{ID: id, CreatedAt: at.Add(time.Duration(i) * time.Second), Text: "Take " + id, Model: kokoro}, 0.1)
	}
	a.setStarred(a.findTake("A"), true)
	a.navigate("/settings/studio")
	tt.Frame()
	if !tt.HasText("Keep at most") {
		t.Fatalf("settings: %q", tt.Texts())
	}
	a.updateSettings(func(s *settings.Settings) { s.Studio.KeepTakes = 2 })
	// The library prunes in the background.
	deadline := time.Now().Add(5 * time.Second)
	for {
		takes, _ := a.lib.svc.Takes.List(context.Background(), core.TakeQuery{})
		if len(takes) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("takes %d", len(takes))
		}
		time.Sleep(5 * time.Millisecond)
	}
	tt.Frame()
	if a.findTake("A") == nil || a.findTake("B") != nil || a.findTake("D") == nil {
		t.Errorf("kept %v", a.takes)
	}
	if a.history.keepTakes != 2 {
		t.Errorf("form %v", a.history.keepTakes)
	}
}

func TestPronunciationSettings(t *testing.T) {
	a, eng, tt := newTestApp(t)
	a.navigate("/settings/pronunciation")
	tt.Frame()
	if !tt.HasText("No entries yet.") {
		t.Fatalf("page: %q", tt.Texts())
	}
	tt.Click("Add an entry")
	tt.Frame()
	a.pron.entries[0].Term, a.pron.entries[0].Replacement = "GIF", "jif"
	a.savePronunciation()
	a.pron.test = "A GIF costs $5."
	tt.Frame()
	if !tt.HasText("The model reads: A jif costs five dollars.") {
		t.Errorf("test: %q", tt.Texts())
	}
	es, _ := a.lib.svc.Pronunciation.Entries(context.Background())
	if len(es) != 1 || es[0].Term != "GIF" || !es[0].Enabled || es[0].Scope != lexicon.Global {
		t.Fatalf("saved %+v", es)
	}
	// A broken entry says why.
	tt.Click("Add an entry")
	tt.Frame()
	if !tt.HasText("The term is empty.") {
		t.Errorf("issues: %q", tt.Texts())
	}
	// IPA type.
	a.pron.types[1] = "IPA"
	tt.Frame()
	// Listen renders a preview (no take) and plays it.
	tt.Click("Listen")
	req := eng.last(t)
	p := renderOf(t, req)
	if !strings.HasPrefix(req.Spec.Owner, "preview:") || p.Take.Text != "A GIF costs $5." || a.pron.previewJob != req.ID {
		t.Fatalf("preview %+v", req.Spec)
	}
	writeTestWAV(t, p.Output, 24000, 0.5)
	eng.succeed(req.ID, services.RenderResult{Output: p.Output, SampleRate: 24000})
	if a.pron.previewJob != "" || a.pl.Current() != "preview:"+req.ID {
		t.Errorf("preview not played: %q", a.pl.Current())
	}
	if len(a.takes) != 0 {
		t.Error("the preview made a take")
	}
	// Remove the entries.
	tt.Click("Remove")
	tt.Frame()
	tt.Click("Remove")
	tt.Frame()
	if len(a.pron.entries) != 0 {
		t.Errorf("entries %+v", a.pron.entries)
	}
}

// TestStudioCustomVoiceField: a custom repo's voice is typed under
// Advanced (the catalogue knows none of its speakers).
func TestStudioCustomVoiceField(t *testing.T) {
	a, _, tt := newTestApp(t)
	a.modelLabel, a.customRepo = catalog.CustomLabel, "acme/tts"
	a.modelChanged()
	a.setAdvancedOpen("studio", true)
	tt.Frame()
	if _, ok := tt.Find("Model default"); !ok {
		t.Fatalf("no voice field: %q", tt.Texts())
	}
	a.voice = "narrator_2"
	if r := a.recipe(time.Now()); r.Voice != "narrator_2" || r.Model != "acme/tts" {
		t.Errorf("custom take %+v", r)
	}
}
