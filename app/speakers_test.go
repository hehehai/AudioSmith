package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// TestTranscribeSpeakersOption (thread #48): the Speakers option sends
// the speakers model (with the aligner in Accurate, the dictation
// vocabulary as hotwords) and asks for its download while it is missing;
// the transcript shows its speakers, renames one, and exports and copies
// with names.
func TestTranscribeSpeakersOption(t *testing.T) {
	simulateDarwin(t)
	m := newM2Test(t)
	a, eng, tt := m.a, m.eng, m.tt
	moss, ok := catalog.Builtin().SpeakersModel()
	if !ok {
		t.Fatal("no speakers model")
	}
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Vocabulary = "AudioSmith, MLX" })
	a.navigate("/transcripts")
	tt.Frame()
	if err := tt.Click("Speakers"); err != nil {
		t.Fatalf("toggle: %v (%q)", err, tt.Texts())
	}
	tt.Frame()
	if !a.prefs.Transcribe.Speakers || !tt.HasText("Transcription with speakers + word timings") ||
		!tt.HasText("Transcribing needs “Transcription with speakers”, which is not downloaded yet.") {
		t.Fatalf("speakers on: %v %q", a.prefs.Transcribe.Speakers, tt.Texts())
	}
	// Dictation and the default stay on Qwen3-ASR.
	if a.dictModel().ID == moss.ID || sttEntry(moss.ID, "").ID == moss.ID || slices.ContainsFunc(sttEntries(), func(e catalog.Entry) bool { return e.ID == moss.ID }) {
		t.Error("the speakers model is offered for dictation")
	}
	fakeOnDisk(t, a, moss)
	tt.Frame()
	src := filepath.Join(t.TempDir(), "panel.m4a")
	os.WriteFile(src, []byte("m4a"), 0o644)
	a.transcribeFile(src)
	r, req := transcribeReq(t, eng)
	if req.Model != moss.Repo || req.Aligner != alignerRepo || req.Prompt != "AudioSmith, MLX" || req.Mode != settings.TranscribeAccurate {
		t.Fatalf("request %+v", req)
	}
	tr := speakersTranscript(time.Now())
	tr.ID = req.ID
	putTranscript(t, a, tr)
	eng.succeed(r.ID, services.TranscribeResult{Transcript: tr})
	tt.Frame()
	for _, s := range []string{"主持人", "S2", "S3", "7 segments"} {
		if !tt.HasText(s) {
			t.Fatalf("detail lacks %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("No speaker labels") {
		t.Error("says no speakers")
	}
	// Rename S2.
	if err := tt.Click("Rename S2"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	tt.Frame()
	a.trRenBuf = "李工"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	saved, _ := a.trSvc.Get(context.Background(), req.ID)
	if saved.Speakers["S2"] != "李工" || saved.Speakers["S1"] != "主持人" || !tt.HasText("李工") {
		t.Fatalf("names %v", saved.Speakers)
	}
	// Copy and TXT/SRT exports name the speakers.
	tt.Click("Copy")
	if c := tt.Clipboard(); !strings.HasPrefix(c, "主持人: 今天我们请到了两位嘉宾") || !strings.Contains(c, "\n\n李工: 谢谢邀请。") {
		t.Errorf("clipboard %q", c)
	}
	a.exportTranscript(services.ExportSRT)
	data, _ := os.ReadFile(filepath.Join(trExportDir, "访谈-第12期.srt"))
	if !strings.Contains(string(data), "李工: 谢谢邀请。") {
		t.Errorf("srt %q", data)
	}
	// Renaming back to the label drops the name.
	a.renameSpeaker("S2", " S2 ")
	tt.Frame()
	saved, _ = a.trSvc.Get(context.Background(), req.ID)
	if _, ok := saved.Speakers["S2"]; ok {
		t.Errorf("names %v", saved.Speakers)
	}
}

// TestDubSpeakersModelApp: the dub page recommends the speakers model
// while it is missing (no download without the click), and once it is
// downloaded the transcript stage labels the lines with it, without
// Sortformer.
func TestDubSpeakersModelApp(t *testing.T) {
	simulateDarwin(t)
	d := newDubTest(t, 6)
	a, tt := d.a, d.tt
	moss, _ := catalog.Builtin().SpeakersModel()
	a.dubSourceFile(d.src)
	a.refreshModels()
	tt.Frame()
	hint := "Lines get the right speaker far more often with “Transcription with speakers” (≈ 1.3 GB): it transcribes and tells speakers apart in one pass."
	if !tt.HasText(hint) {
		t.Fatalf("no hint: %q", tt.Texts())
	}
	if m := a.model(moss); m.downloading {
		t.Fatal("downloading without a click")
	}
	// Without it: Sortformer, and the warning.
	a.dubStart()
	tt.Frame()
	if n := d.fe.Count(dub.MethodDiarize); n != 1 || !slices.Contains(a.dub.Stage(dub.StageTranscribe, "").Warnings, services.DubWarnSpeakersModel) {
		t.Fatalf("fallback: %d diarize calls, stage %+v", n, a.dub.Stage(dub.StageTranscribe, ""))
	}
	// Downloaded: transcribe again with it.
	d.fe.Speakers = 3
	fakeOnDisk(t, a, moss)
	tt.Frame()
	a.dubStep = dubStepTranscribe
	tt.Frame()
	if !tt.HasText("“Transcription with speakers” is ready: transcribe again to tell the speakers apart with it.") {
		t.Fatalf("no transcribe-again hint: %q", tt.Texts())
	}
	tt.Click("Transcribe again")
	tt.Frame()
	if a.dubConfirm != "transcribe" || !a.dubConfirmOpen {
		t.Fatalf("confirm %q %v", a.dubConfirm, a.dubConfirmOpen)
	}
	a.dubConfirmOpen = false
	a.dubRun(services.RunRequest{Through: dub.StageReferences, Force: []dub.Stage{dub.StageTranscribe}})
	tt.Frame()
	p := a.dub
	if n := d.fe.Count(dub.MethodDiarize); n != 1 || !slices.Equal(p.SpeakerLabels(), []string{"S1", "S2", "S3"}) ||
		slices.Contains(p.Stage(dub.StageTranscribe, "").Warnings, services.DubWarnSpeakersModel) {
		t.Fatalf("with the speakers model: %d diarize calls, speakers %v, stage %+v", n, p.SpeakerLabels(), p.Stage(dub.StageTranscribe, ""))
	}
	var tp proto.TranscribeParams
	if raw := d.fe.Last(proto.MethodSTTTranscribe); raw != nil {
		_ = json.Unmarshal(raw, &tp)
	}
	if tp.Model != moss.Repo {
		t.Errorf("transcribed with %q", tp.Model)
	}
}
