package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dictation"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

const defaultAcc = "CmdOrCtrl+Shift+Space"

// finishDictation plays the transcription of the current dictation:
// the transcript saved, its job done.
func (m *m2Test) finishDictation(t *testing.T, text, language string) core.Transcript {
	t.Helper()
	s := m.a.dict
	if s == nil || s.trID == "" {
		t.Fatalf("no transcription: %+v", s)
	}
	r, req := transcribeReq(t, m.eng)
	now := time.Now()
	tr := core.Transcript{ID: req.ID, CreatedAt: now, UpdatedAt: now, Source: core.SourceDictation, Mode: "fast", Model: req.Model,
		Language: language, Duration: 1, App: req.App, Raw: text}
	if text != "" {
		tr.Segments = []core.TranscriptSegment{{Start: 0, End: 1, Text: text}}
	}
	putTranscript(t, m.a, tr)
	m.eng.succeed(r.ID, services.TranscribeResult{Transcript: tr})
	return tr
}

func TestDictationToggle(t *testing.T) {
	m := newM2Test(t)
	a, sys, keys := m.a, m.sys, m.keys
	if !keys.has(defaultAcc) || a.dictShortcut != defaultAcc {
		t.Fatalf("shortcut not registered: %v", keys.log)
	}
	sys.Write(transfer.TextData("what I had copied"))
	sys.Front = dictation.Target{PID: 77, Name: "Slack"}

	keys.press(t, defaultAcc)
	s := a.dict
	if s == nil || s.phase != dictListening || s.target.Name != "Slack" || !a.dictPill.visible || !a.dictVerified || !keys.has("Escape") {
		t.Fatalf("listening: %+v", s)
	}
	if !a.dictTray.recording || sys.Sounds[0] != "Tink" {
		t.Errorf("tray %v sounds %v", a.dictTray.recording, sys.Sounds)
	}
	start := m.eng.lastOf(t, proto.MethodCaptureStart)
	var cp proto.CaptureParams
	jsonUnmarshal(t, start.Params, &cp)
	if cp.SampleRate != services.TranscriptRate || cp.Channels != 1 {
		t.Errorf("capture %+v", cp)
	}
	// The pill shows the level and Stop.
	pill := ui.NewTester(a.pillView, pillWinWidth, pillWinHeight)
	pill.Frame()
	if !pill.HasText("Listening…") || !pill.HasText("Stop") {
		t.Errorf("pill: %q", pill.Texts())
	}
	// Only the pill shows: its window's corners stay clear, though Stop
	// (an ink button) swaps the theme, which resets the root's background.
	if _, _, _, alpha := pill.Image().At(2, 2).RGBA(); alpha != 0 {
		t.Errorf("the pill's window is painted at its corner (alpha %d): a dark box round the pill", alpha>>8)
	}

	keys.press(t, defaultAcc) // toggle: stop
	if s.phase != dictTranscribing {
		t.Fatalf("phase %v", s.phase)
	}
	_, req := transcribeReq(t, m.eng)
	if req.Source != core.SourceDictation || req.Model != qwenASRRepo || !req.RemoveInput || req.App != "Slack" || req.Mode != settings.TranscribeFast {
		t.Fatalf("request %+v", req)
	}
	// Presses while it transcribes are ignored.
	keys.press(t, defaultAcc)
	if a.dict != s {
		t.Fatal("a new dictation started while transcribing")
	}
	m.finishDictation(t, "um so this is the dictated text", "en")
	if s.phase != dictInserted || s.text != "So this is the dictated text." {
		t.Fatalf("delivered: %v %q %q", s.phase, s.text, s.note)
	}
	if len(sys.Pastes) != 1 || sys.Pastes[0] != "So this is the dictated text." || sys.Activated[0].PID != 77 {
		t.Errorf("pastes %v activated %v", sys.Pastes, sys.Activated)
	}
	if sys.ClipText() != "what I had copied" {
		t.Errorf("clipboard not restored: %q", sys.ClipText())
	}
	if keys.has("Escape") || a.dictTray.recording {
		t.Error("escape or tray left on")
	}
	saved, err := a.trSvc.Get(context.Background(), req.ID)
	if err != nil || !saved.Inserted || saved.Clean != "So this is the dictated text." || saved.CleanBy != "polish" || saved.App != "Slack" {
		t.Fatalf("saved %+v %v", saved, err)
	}
	pill.Frame()
	if !pill.HasText("Inserted") {
		t.Errorf("pill: %q", pill.Texts())
	}
	a.dictHide()
	if a.dictPill.visible {
		t.Error("pill shows")
	}
}

func TestDictationHold(t *testing.T) {
	m := newM2Test(t)
	a, sys, keys := m.a, m.sys, m.keys
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Mode = settings.DictationHold })
	keys.press(t, defaultAcc)
	s := a.dict
	s.pressed = time.Now().Add(-time.Second) // held a while
	keys.press(t, defaultAcc)                // a second press while held does not stop it
	if s.phase != dictListening {
		t.Fatalf("phase %v", s.phase)
	}
	sys.Release()
	if s.phase != dictTranscribing {
		t.Fatalf("release did not stop: %v", s.phase)
	}
	// Empty transcript: no speech, and the entry goes.
	tr := m.finishDictation(t, "", "")
	if s.phase != dictNoSpeech {
		t.Fatalf("phase %v", s.phase)
	}
	if _, err := a.trSvc.Get(context.Background(), tr.ID); err == nil {
		t.Error("empty dictation kept")
	}

	// A tap keeps listening until the next press.
	keys.press(t, defaultAcc)
	s = a.dict
	sys.Release()
	if s.phase != dictListening || s.hold {
		t.Fatalf("tap: %v hold %v", s.phase, s.hold)
	}
	keys.press(t, defaultAcc)
	if s.phase != dictTranscribing {
		t.Fatalf("second press: %v", s.phase)
	}
}

func TestDictationCopiesAndCancels(t *testing.T) {
	m := newM2Test(t)
	a, sys, keys := m.a, m.sys, m.keys
	// Without Accessibility: copied, with the reason.
	sys.Trusted = false
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	m.finishDictation(t, "你好世界", "zh")
	s := a.dict
	if s.phase != dictCopied || sys.ClipText() != "你好世界。" || len(sys.Pastes) != 0 ||
		s.note != "To type into apps, allow AudioSmith in Privacy & Security → Accessibility." {
		t.Fatalf("copied: %v %q %q", s.phase, sys.ClipText(), s.note)
	}

	// Escape cancels while listening: no transcription.
	sys.Trusted = true
	keys.press(t, defaultAcc)
	s = a.dict
	keys.press(t, "Escape")
	if s.phase != dictIdle || a.dictPill.visible || keys.has("Escape") {
		t.Fatalf("cancel: %v", s.phase)
	}
	if s.trID != "" {
		t.Error("transcribed after cancel")
	}

	// No microphone permission: an error at once.
	sys.Mic = dictation.PermissionDenied
	keys.press(t, defaultAcc)
	if a.dict.phase != dictError || a.dict.note == "" {
		t.Fatalf("denied: %+v", a.dict)
	}
	sys.Mic = dictation.PermissionGranted

	// The model is not downloaded: the pill says so, with Open Models.
	removeModel(t, a, catalog.DefaultDictation)
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	if a.dict.phase != dictError || a.dict.need == nil {
		t.Fatalf("not downloaded: %+v", a.dict)
	}
	pill := ui.NewTester(a.pillView, pillWinWidth, pillWinHeight)
	pill.Frame()
	if !pill.HasText("Dictation failed") || !pill.HasText("Open Models") {
		t.Errorf("pill %q", pill.Texts())
	}
}

func TestDictationCleanupWithLLM(t *testing.T) {
	m := newM2Test(t)
	a, sys, keys := m.a, m.sys, m.keys
	fake := &fakeLLM{local: true, answer: func(req llm.Request) (string, error) {
		return `{"text":"Meet me at five."}`, nil
	}}
	useFakeLLM(a, fake)
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Vocabulary = "Kenobi, MLX" })
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	_, req := transcribeReq(t, m.eng)
	if req.Prompt != "Kenobi, MLX" {
		t.Errorf("prompt %q", req.Prompt)
	}
	m.finishDictation(t, "meet me at four no wait at five", "en")
	if a.dict.text != "Meet me at five." || sys.Pastes[0] != "Meet me at five." {
		t.Fatalf("cleaned: %q %v", a.dict.text, sys.Pastes)
	}
	saved, _ := a.trSvc.Get(context.Background(), a.dict.trID)
	if saved.CleanBy == "polish" || saved.Clean != "Meet me at five." {
		t.Errorf("saved %+v", saved)
	}
	// Clean-up off: polish only, no model call.
	calls := fake.calls
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Cleanup = false })
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	m.finishDictation(t, "meet me at four no wait at five", "en")
	if fake.calls != calls || a.dict.text != "Meet me at four no wait at five." {
		t.Errorf("cleanup off: %d calls %q", fake.calls-calls, a.dict.text)
	}
	// Copy only.
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Output = settings.OutputCopy })
	keys.press(t, defaultAcc)
	keys.press(t, defaultAcc)
	m.finishDictation(t, "copy me", "en")
	if a.dict.phase != dictCopied || a.dict.note != "" || sys.ClipText() != "Copy me." {
		t.Errorf("copy only: %v %q", a.dict.phase, sys.ClipText())
	}
}

func TestDictationShortcutSettings(t *testing.T) {
	m := newM2Test(t)
	a, tt, keys := m.a, m.tt, m.keys
	a.navigate("/settings/dictation")
	tt.Frame()
	if !tt.HasText("⇧⌘Space") || !tt.HasText("Verified: it works on this Mac.") && !tt.HasText("Press it anywhere to check it works.") {
		t.Fatalf("page: %q", tt.Texts())
	}
	// Record ⌥D: the old shortcut is released while recording.
	if err := tt.Click("Record a shortcut"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if keys.has(defaultAcc) || !a.dictSet.recording {
		t.Fatal("old shortcut still registered while recording")
	}
	// ⌥ goes down first, on its own: nothing yet, no complaint.
	tt.Key(ui.Alt, ui.KeyUnknown)
	tt.Frame()
	if a.dictSet.pending != "" || a.dictSet.recErr != "" || !a.dictSet.recording {
		t.Fatalf("a modifier alone: pending %q, error %q", a.dictSet.pending, a.dictSet.recErr)
	}
	tt.Key(ui.Alt, ui.KeyD)
	tt.Frame()
	if a.dictSet.pending != "Alt+D" || !tt.HasText("⌥D") {
		t.Fatalf("pending %q %q", a.dictSet.pending, tt.Texts())
	}
	tt.Click("Save")
	tt.Frame()
	if a.prefs.Dictation.Shortcut != "Alt+D" || !keys.has("Alt+D") || keys.has(defaultAcc) || a.dictShortcut != "Alt+D" {
		t.Fatalf("saved %q %v", a.prefs.Dictation.Shortcut, keys.log)
	}

	// A key without a modifier, then one macOS uses: refused.
	tt.Click("Record a shortcut")
	tt.Frame()
	tt.Key(0, ui.KeyD)
	tt.Frame()
	if a.dictSet.pending != "" || !tt.HasText("Add a modifier: ⌘, ⌃ or ⌥ with another key.") {
		t.Errorf("no modifier: %q", tt.Texts())
	}
	tt.Key(ui.Super, ui.KeySpace)
	tt.Frame()
	a.saveShortcut(a.dictSet.pending)
	tt.Frame()
	if a.prefs.Dictation.Shortcut != "Alt+D" || !tt.HasText("macOS uses it (Spotlight).") {
		t.Errorf("conflict: %q %q", a.prefs.Dictation.Shortcut, tt.Texts())
	}
	tt.Click("Cancel")
	tt.Frame()
	if !keys.has("Alt+D") {
		t.Error("cancel did not restore the shortcut")
	}

	// A shortcut another app took: the working one stays.
	keys.fail["Ctrl+Alt+K"] = true
	a.saveShortcut("Ctrl+Alt+K")
	tt.Frame()
	if !keys.has("Alt+D") || a.dictShortcut != "Alt+D" || a.dictRegErr == "" {
		t.Errorf("rollback: %v %q", keys.log, a.dictRegErr)
	}
	// Off: unregistered.
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Enabled = false })
	a.applyDictShortcut()
	if keys.has("Alt+D") || keys.has("Ctrl+Alt+K") {
		t.Errorf("disabled: %v", keys.log)
	}
}

func TestPermissionsPage(t *testing.T) {
	m := newM2Test(t)
	a, tt, sys := m.a, m.tt, m.sys
	tallSettings(tt) // the permissions are at the foot of Dictation
	sys.Mic, sys.Trusted = dictation.PermissionNotDetermined, false
	a.recheckPermissions()
	a.navigate("/settings/permissions")
	tt.Frame()
	if !tt.HasText("Not asked yet") || !tt.HasText("Not allowed") {
		t.Fatalf("page %q", tt.Texts())
	}
	tt.Click("Ask") // the microphone's
	tt.Frame()
	if !tt.HasText("Allowed") {
		t.Errorf("after asking: %q", tt.Texts())
	}
	if err := tt.Click("Open System Settings"); err != nil {
		t.Fatal(err)
	}
	if u := tt.OpenedURLs(); len(u) != 1 || u[0] != privacyAccessibility {
		t.Errorf("opened %v", u)
	}
	sys.Trusted = true
	tt.Click("Check again")
	tt.Frame()
	if tt.HasText("Not allowed") {
		t.Errorf("recheck: %q", tt.Texts())
	}
}

func TestWritingHelpSettings(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	a.navigate("/settings/writing")
	tt.Frame()
	if ok, _ := a.llmAvailable(); ok || !tt.HasText("Not running on this Mac.") {
		t.Fatalf("page %q", tt.Texts())
	}
	// Ollama found: automatic routing uses it, with a chat model.
	a.llmOllama = []string{"nomic-embed-text", "qwen3:4b"}
	a.applyLLM()
	tt.Frame()
	if ok, who := a.llmAvailable(); !ok || who != "Ollama · qwen3:4b" || !tt.HasText("Running with 2 models; it needs no setup.") {
		t.Fatalf("auto: %v %q %q", ok, who, tt.Texts())
	}
	// Add OpenAI: not used until chosen.
	a.llmForm.add = "OpenAI"
	tt.Frame()
	tt.Click("Add")
	tt.Frame()
	if len(a.prefs.LLM.Providers) != 1 || a.prefs.LLM.Providers[0].ID != "openai" || !tt.HasText("Cloud: text is sent to this provider") {
		t.Fatalf("added %+v", a.prefs.LLM.Providers)
	}
	if _, who := a.llmAvailable(); who != "Ollama · qwen3:4b" {
		t.Errorf("cloud used without a choice: %q", who)
	}
	a.chooseLLMRoute("openai")
	tt.Frame()
	if !tt.HasText("OpenAI") {
		t.Errorf("route: %q", tt.Texts())
	}
	if !a.prefs.LLM.Manual || len(a.prefs.LLM.Default) != 1 || a.prefs.LLM.Default[0].Provider != "openai" {
		t.Fatalf("route %+v", a.prefs.LLM)
	}
	if _, who := a.llmAvailable(); who != "OpenAI · gpt-4.1-mini" {
		t.Errorf("chosen: %q", who)
	}
	// Local only skips it.
	a.updateSettings(func(s *settings.Settings) { s.Privacy.LocalOnlyLLM = true })
	tt.Frame()
	if ok, _ := a.llmAvailable(); ok || !a.prefs.Privacy.LocalOnlyLLM {
		t.Error("local only still routes to the cloud")
	}
	// Test without a key: says so.
	a.testProvider("openai")
	tt.Frame()
	if !tt.HasText("No API key: paste one and save it.") {
		t.Errorf("test: %q", tt.Texts())
	}
	tt.Click("Remove")
	tt.Frame()
	if len(a.prefs.LLM.Providers) != 0 || len(a.prefs.LLM.Default) != 0 {
		t.Errorf("removed %+v", a.prefs.LLM)
	}
}

func TestPickOllamaModel(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		"nomic-embed-text":                 "",
		"mistral:7b,qwen3:8b":              "qwen3:8b",
		"phi4,custom:latest":               "phi4",
		"custom:latest,nomic-embed-text":   "custom:latest",
		"llama3.2:3b,gemma3:4b,qwen2.5:7b": "qwen2.5:7b",
	} {
		var l []string
		if in != "" {
			l = splitComma(in)
		}
		if got := pickOllamaModel(l); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
