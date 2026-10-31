package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

const (
	whisperRepo = "mlx-community/whisper-large-v3-turbo-4bit"
	qwenASRRepo = "mlx-community/Qwen3-ASR-0.6B-4bit"
	alignerRepo = "mlx-community/Qwen3-ForcedAligner-0.6B-4bit"
)

func transcribeReq(t *testing.T, eng *fakeEngine) (sentReq, services.TranscribeRequest) {
	t.Helper()
	r := eng.lastOf(t, services.KindTranscribe)
	var req services.TranscribeRequest
	if err := json.Unmarshal(r.Params, &req); err != nil {
		t.Fatal(err)
	}
	return r, req
}

func TestTranscribeFileAccurate(t *testing.T) {
	m := newM2Test(t)
	a, eng, tt := m.a, m.eng, m.tt
	a.navigate("/transcripts")
	tt.Frame()
	if !tt.HasText("No transcripts yet") || !tt.HasText("Drop audio or video here") || !tt.HasText("Chinese & English transcription + word timings") {
		t.Fatalf("empty page: %q", tt.Texts())
	}
	a.updateSettings(func(s *settings.Settings) { s.Transcribe.Language = "en" })
	src := filepath.Join(t.TempDir(), "interview.m4a")
	os.WriteFile(src, []byte("m4a"), 0o644)
	a.transcribeFile(src)
	if a.trErr != "" {
		t.Fatalf("refused: %s", a.trErr)
	}
	r, req := transcribeReq(t, eng)
	if req.Model != qwenASRRepo || req.Aligner != alignerRepo || req.Mode != settings.TranscribeAccurate || req.Language != "en" ||
		req.Input != src || req.Title != "interview.m4a" || r.Spec.Owner != services.TranscriptOwner+req.ID {
		t.Fatalf("request %+v", req)
	}
	eng.progress(r.ID, "transcribing", 0.4, 0)
	tt.Frame()
	if !tt.HasText("Transcribing interview.m4a") || !tt.HasText("Transcribing…") {
		t.Fatalf("progress: %q", tt.Texts())
	}

	// Done: the transcript shows, its words under the sentence playing.
	tr := sampleTranscript(req.ID, time.Now())
	putTranscript(t, a, tr)
	eng.succeed(r.ID, services.TranscribeResult{Transcript: tr})
	tt.Frame()
	if a.trJob != nil || a.trSel != req.ID || a.trDetail == nil || !tt.HasText("Hello there.") || !tt.HasText("General Kenobi.") ||
		!tt.HasText("2 segments") || a.router.Path() != "/transcripts/"+req.ID {
		t.Fatalf("detail: %q", tt.Texts())
	}

	// Copy.
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "Hello there. General Kenobi." {
		t.Errorf("clipboard %q", tt.Clipboard())
	}

	// Edit a sentence: its words lose their timings.
	if err := tt.Click("General Kenobi."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.trEditBuf = "General Grievous."
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	saved, err := a.trSvc.Get(context.Background(), req.ID)
	if err != nil || saved.Segments[1].Text != "General Grievous." || !saved.Segments[1].Edited || !tt.HasText("edited") {
		t.Fatalf("edit: %+v %v", saved.Segments, err)
	}

	// Export SRT.
	tt.Click("Export")
	tt.Frame()
	if err := tt.Click("Subtitles (.srt)"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(trExportDir, "interview.srt"))
	if err != nil || !strings.Contains(string(data), "00:00:00,200 --> ") || !strings.Contains(string(data), "General Grievous.") {
		t.Fatalf("srt %q %v", data, err)
	}

	// Polish, then the original under it.
	tt.Click("Polish")
	tt.Frame()
	if a.trDetail.Clean == "" || a.trDetail.CleanBy != "polish" || !tt.HasText("Polished") || !tt.HasText("Show the original transcript") {
		t.Fatalf("polish: %+v %q", a.trDetail, tt.Texts())
	}
	tt.Click("Show the original transcript")
	tt.Frame()
	if !tt.HasText("Hello there. General Grievous.") {
		t.Errorf("original: %q", tt.Texts())
	}
	tt.Click("Remove the clean-up")
	tt.Frame()
	if a.trDetail.Clean != "" {
		t.Error("clean-up kept")
	}

	// Reuse as script.
	tt.Click("Read it in Text to speech")
	tt.Frame()
	if a.currentPage() != "studio" || a.text != "Hello there. General Grievous." {
		t.Fatalf("reuse: %s %q", a.currentPage(), a.text)
	}

	// Delete, confirmed.
	a.navigate("/transcripts")
	tt.Frame()
	tt.Click("Transcript actions")
	if err := tt.ChooseMenuItem("Delete…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.trList) != 0 || a.trSel != "" || !tt.HasText("No transcripts yet") {
		t.Fatalf("delete: %d %q", len(a.trList), tt.Texts())
	}
	if _, err := os.Stat(filepath.Join(a.lib.store.Transcripts().AudioDir(), req.ID+".wav")); !os.IsNotExist(err) {
		t.Error("audio kept")
	}
}

func TestTranscribeFastAndRecording(t *testing.T) {
	m := newM2Test(t)
	a, eng, tt := m.a, m.eng, m.tt
	a.navigate("/transcripts")
	tt.Frame()
	tt.Click("Fast")
	tt.Frame()
	if a.prefs.Transcribe.Mode != settings.TranscribeFast || !tt.HasText("Chinese & English transcription") {
		t.Fatalf("fast: %q %q", a.prefs.Transcribe.Mode, tt.Texts())
	}
	// Record, then stop: the recording is transcribed and removed after.
	if err := tt.Click("Record"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.trMic.phase != "recording" {
		t.Fatalf("mic %q %q", a.trMic.phase, a.trMic.err)
	}
	tt.Click("Stop recording")
	tt.Frame()
	_, req := transcribeReq(t, eng)
	if req.Model != qwenASRRepo || req.Aligner != "" || req.Source != core.SourceRecording || !req.RemoveInput || req.Title != "" {
		t.Fatalf("request %+v", req)
	}
	tt.Frame()
	if !tt.HasText("Transcribing Recording") {
		t.Errorf("job: %q", tt.Texts())
	}
	// Cancel.
	tt.Click("Cancel")
	r, _ := transcribeReq(t, eng)
	if eng.last(t).Cmd != "cancel" {
		t.Errorf("cancel not sent: %v", eng.last(t))
	}
	eng.cancelled(r.ID)
	tt.Frame()
	if a.trJob != nil {
		t.Error("job kept")
	}
}

func TestTranscribeErrors(t *testing.T) {
	m := newM2Test(t)
	a, eng, tt := m.a, m.eng, m.tt
	a.navigate("/transcripts")
	e := removeModel(t, a, catalog.DefaultSTT)
	tt.Frame()
	if !tt.HasText("Transcribing needs “Chinese & English transcription”, which is not downloaded yet.") {
		t.Fatalf("banner: %q", tt.Texts())
	}
	src := filepath.Join(t.TempDir(), "a.wav")
	writeTestWAV(t, src, 16000, 1)
	a.transcribeFile(src)
	tt.Frame()
	if a.trJob != nil || a.trNeed == nil || a.trNeed.ID != e.ID || !tt.HasText("Transcription failed") || !tt.HasText("Download “Chinese & English transcription”") {
		t.Fatalf("refused: %q", tt.Texts())
	}
	tt.Click("Dismiss")
	tt.Frame()

	// A failure of the job: Retry sends it again, with a new id.
	useLocalModels(t, a.lib)
	completeAux(t, a)
	a.refreshModels()
	a.transcribeFile(src)
	r, req := transcribeReq(t, eng)
	eng.fail(r.ID, proto.CodeUnsupportedMedia, "cannot read")
	tt.Frame()
	if !tt.HasText("This file's format cannot be read. Convert it to WAV, M4A or MP3 and try again.") || a.trRetry == nil {
		t.Fatalf("failed: %q", tt.Texts())
	}
	tt.Click("Retry")
	_, again := transcribeReq(t, eng)
	if again.ID == req.ID || again.Input != src {
		t.Errorf("retry %+v", again)
	}

	// Word timings missing: the transcript comes with a warning.
	r, req = transcribeReq(t, eng)
	tr := sampleTranscript(req.ID, time.Now())
	tr.Aligner = ""
	putTranscript(t, a, tr)
	eng.succeed(r.ID, services.TranscribeResult{Transcript: tr, Warnings: []string{services.WarnNoWordTimes},
		AlignError: &jobs.Error{Code: proto.CodeOOM}})
	tt.Frame()
	if !tt.HasText("Word timings are missing: Out of memory. Unload other models, or choose a smaller one.") {
		t.Errorf("warning: %q", tt.Texts())
	}
}

func TestTranscriptHistory(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	now := time.Now()
	putTranscript(t, a, sampleTranscript("t1", now.Add(-2*time.Hour)))
	d := core.Transcript{ID: "t2", CreatedAt: now, UpdatedAt: now, Source: core.SourceDictation, App: "Notes", Mode: "fast", Language: "zh",
		Duration: 2, Segments: []core.TranscriptSegment{{Start: 0, End: 2, Text: "你好世界"}}, Clean: "你好，世界。"}
	putTranscript(t, a, d)
	a.navigate("/transcripts")
	tt.Frame()
	if len(a.trList) != 2 || !tt.HasText("Dictation in Notes") || !tt.HasText("你好，世界。") || !tt.HasText("interview.m4a") {
		t.Fatalf("history: %q", tt.Texts())
	}
	a.trQuery = "kenobi"
	a.searchTranscripts()
	tt.Frame()
	if tt.HasText("Dictation in Notes") || !tt.HasText("interview.m4a") {
		t.Errorf("search: %q", tt.Texts())
	}
	a.trQuery = "zzz"
	a.searchTranscripts()
	tt.Frame()
	if !tt.HasText("No matching transcripts") {
		t.Errorf("no match: %q", tt.Texts())
	}
	a.trQuery = ""
	a.searchTranscripts()
	tt.Frame()
	tt.Click("Export all")
	data, err := os.ReadFile(filepath.Join(trExportDir, "transcripts_"+time.Now().Format("2006-01-02")+".txt"))
	if err != nil || !strings.Contains(string(data), "(zh)\n你好，世界。") || !strings.Contains(string(data), "---") {
		t.Fatalf("export all %q %v", data, err)
	}
	// A transcript is read whole when chosen: the dictation shows its clean-up.
	tt.Click("Dictation in Notes")
	tt.Frame()
	if a.trDetail == nil || a.trDetail.ID != "t2" || !tt.HasText("Show the original transcript") {
		t.Fatalf("detail: %q", tt.Texts())
	}
}

func TestTranscriptCleanUpWithLLM(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	tr := sampleTranscript("t1", time.Now())
	tr.Segments[0].Text = "Um, hello there, I mean, hello."
	putTranscript(t, a, tr)
	a.navigate("/transcripts")
	tt.Frame()
	a.selectTranscript("t1")
	tt.Frame()
	if ok, _ := a.llmAvailable(); ok {
		t.Fatal("a language model without any set up")
	}
	fake := &fakeLLM{local: true, answer: func(req llm.Request) (string, error) {
		return `{"text":"Hello. General Kenobi."}`, nil
	}}
	useFakeLLM(a, fake)
	tt.Frame()
	if err := tt.Click("Clean up"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.trClean != nil || a.trDetail.Clean != "Hello. General Kenobi." || !strings.HasPrefix(a.trDetail.CleanBy, "fake") || fake.calls == 0 {
		t.Fatalf("clean %+v calls %d", a.trDetail, fake.calls)
	}
	saved, _ := a.trSvc.Get(context.Background(), "t1")
	if saved.Clean != "Hello. General Kenobi." {
		t.Error("not saved")
	}
	// A model that fails: nothing changes, and it says so.
	fake.answer = func(llm.Request) (string, error) { return "", errors.New("down") }
	a.dropClean()
	tt.Frame()
	tt.Click("Clean up")
	tt.Frame()
	if a.trDetail.Clean != "" {
		t.Errorf("failed clean-up applied: %q", a.trDetail.Clean)
	}
}

func TestTranscriptLongPages(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	tr := sampleTranscript("long", time.Now())
	tr.Segments = nil
	for i := range 450 {
		tr.Segments = append(tr.Segments, core.TranscriptSegment{Start: float64(i), End: float64(i) + 0.9, Text: "Sentence " + itoa(i) + "."})
	}
	tr.Duration = 450
	putTranscript(t, a, tr)
	a.navigate("/transcripts")
	a.selectTranscript("long")
	tt.Frame()
	if !tt.HasText("Show 200 more segments") || !tt.HasText("Sentence 199.") {
		t.Fatalf("first page: %q", tt.Texts())
	}
	a.trShown += trPage // the button is far below the view
	tt.Frame()
	if !tt.HasText("Show 50 more segments") {
		t.Errorf("second page")
	}
	a.trShown += trPage
	tt.Frame()
	if tt.HasText("Show 50 more segments") {
		t.Errorf("all shown")
	}
}
