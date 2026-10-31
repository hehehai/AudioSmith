package main

// Tests of the voice library and its pages: the library, try and lock,
// consent, portraits, voice files, and the Studio's seam. A voice from a
// clip is tested in clone_test.go.

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/gallery/persona"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

const (
	qwenBase = "mlx-community/Qwen3-TTS-12Hz-0.6B-Base-8bit"
	omni     = "mlx-community/OmniVoice-bf16"
)

func designAttrs(a vd.Attributes) json.RawMessage {
	b, _ := json.Marshal(a)
	return b
}

func TestVoicesLibrary(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	addVoice(t, a, core.Voice{Name: "Narrator", Kind: core.VoiceCloned, RefText: "Once upon a time.", Style: "calm"}, 6)
	addVoice(t, a, core.Voice{Name: "Grandma", Kind: core.VoiceDesigned, DesignAttrs: designAttrs(vd.Attributes{Gender: "female", Age: "elderly"}), Seed: 7}, 0)
	a.navigate("/voices")
	tt.Frame()
	if !tt.HasText("Narrator") || !tt.HasText("Grandma") || !tt.HasText("2 voices") {
		t.Fatalf("library: %q", tt.Texts())
	}
	if !tt.HasText("Cloned") || !tt.HasText("Designed") {
		t.Error("kind not shown")
	}

	// Search covers styles; the kind filter.
	a.voiceQuery = "calm"
	tt.Frame()
	if !tt.HasText("Narrator") || tt.HasText("Grandma") {
		t.Errorf("search: %q", tt.Texts())
	}
	a.voiceQuery = ""
	tt.Click("Designed")
	tt.Frame()
	if tt.HasText("Narrator") || !tt.HasText("Grandma") {
		t.Errorf("kind filter: %q", tt.Texts())
	}
	tt.Click("All")
	a.voiceQuery = "zzz"
	tt.Frame()
	if !tt.HasText("No matches") {
		t.Errorf("no matches: %q", tt.Texts())
	}
	a.voiceQuery = ""
	tt.Frame()

	// The editor: rename and save.
	if err := tt.Click("Narrator"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.edit == nil || !tt.HasText("Voice details") || !tt.HasText("Replace reference…") {
		t.Fatalf("editor: %q", tt.Texts())
	}
	a.edit.name = "Old Narrator"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.lib.svc.Voices.Get(context.Background(), a.edit.id); v.Name != "Old Narrator" || v.RefText != "Once upon a time." {
		t.Fatalf("saved %+v", v)
	}
	tt.Frame()
	if !tt.HasText("Old Narrator") {
		t.Errorf("snapshot not updated: %q", tt.Texts())
	}

	// Delete, after confirming.
	id := a.edit.id
	tt.Click("Delete")
	tt.Frame()
	if !a.deleteOpen {
		t.Fatal("no confirmation")
	}
	tt.Click("Cancel")
	tt.Frame()
	if a.findVoice(id) == nil {
		t.Fatal("deleted without confirming")
	}
	tt.Click("Delete")
	tt.Frame()
	tt.Click("Delete voice") // the alert's button
	tt.Frame()
	if a.findVoice(id) != nil || a.edit != nil {
		t.Fatal("not deleted")
	}
	if _, err := os.Stat(filepath.Join(a.lib.store.Voices().VoiceDir(), id)); !os.IsNotExist(err) {
		t.Error("files remain")
	}
}

func TestVoicesEmptyAndReload(t *testing.T) {
	dir := t.TempDir()
	a, _, _ := newTestAppIn(t, dir)
	tt := ui.NewTester(a.view, 1200, 1800)
	a.navigate("/voices")
	tt.Frame()
	if !tt.HasText("No saved voices yet") || !tt.HasText("Discover voices ›") {
		t.Fatalf("empty: %q", tt.Texts())
	}
	tt.Click("New voice")
	if a.router.Path() != "/voices/new" {
		t.Errorf("path %s", a.router.Path())
	}
	addVoice(t, a, core.Voice{Name: "Kept", Kind: core.VoiceCloned}, 3)

	// The next start reads it back.
	b, _, _ := newTestAppIn(t, dir)
	if len(b.voices) != 1 || b.voices[0].Name != "Kept" {
		t.Fatalf("reloaded %+v", b.voices)
	}
}

func TestVoiceTryAndLock(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	v := addVoice(t, a, core.Voice{Name: "Me", Kind: core.VoiceCloned, RefText: "Reference words.", Style: "whisper", Language: "en"}, 5)
	a.openVoiceEditor(v.ID)
	tt.Frame()
	if !tt.HasText("Test phrase") { // 「试一句」 is open in the detail
		t.Fatalf("try: %q", tt.Texts())
	}
	om, _ := catalog.Builtin().ByRepo(omni)
	a.edit.tryModel = om.Label
	acceptLicences(t, a)
	tt.Frame()
	if err := tt.Click("Generate"); err != nil {
		t.Fatal(err)
	}
	req := eng.last(t)
	if req.Cmd != "synthesize" || req.Model != omni || req.RefAudio != a.voicePath(&v, v.RefAudio) || req.RefText != "Reference words." {
		t.Fatalf("request %+v", req.SynthesizeParams)
	}
	// OmniVoice reads tags: the style becomes "whisper".
	if req.Voice != "whisper" {
		t.Errorf("style sent as %q", req.Voice)
	}
	j := a.voiceJob("try", v.ID)
	if !j.running() {
		t.Fatal("no job")
	}
	eng.progress(req.ID, "generating", 0.5, 1)
	tt.Frame()
	writeTestWAV(t, req.Output.Path, 24000, 1)
	seed := int64(1234)
	eng.succeed(req.ID, proto.SynthesizeResult{Output: req.Output.Path, SampleRate: 24000, AudioSeconds: 1, Seconds: 0.5, Seed: &seed})
	tt.Frame()
	if j.take == nil || j.take.VoiceID != v.ID || !tt.HasText("1.0 s · seed 1234") {
		t.Fatalf("take %+v %q", j.take, tt.Texts())
	}
	if err := tt.Click("Lock voice identity"); err != nil {
		t.Fatal(err)
	}
	got := a.findVoice(v.ID)
	if !got.Locked || got.Seed != 1234 || got.LockTake != j.take.ID || got.CreatedWith != omni {
		t.Fatalf("locked %+v", got)
	}
	// A locked voice clones its locked take in the Studio.
	a.studioVoice = v.ID
	e, _ := catalog.Builtin().ByRepo(qwenBase)
	tk := core.Take{Text: "x", Model: qwenBase}
	a.applyStudioVoice(&tk, e)
	if tk.RefAudio != a.voicePath(got, "locked.wav") || tk.Seed != 1234 || tk.VoiceID != v.ID || tk.Voice != "" {
		t.Errorf("studio take %+v", tk)
	}
	// Unlock, confirmed.
	tt.Frame()
	tt.Click("Unlock")
	tt.Frame()
	tt.Click("Unlock voice")
	if a.findVoice(v.ID).Locked {
		t.Error("still locked")
	}
}

func TestVoiceConsent(t *testing.T) {
	a, eng := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	v := addVoice(t, a, core.Voice{Name: "Me", Kind: core.VoiceCloned}, 5)
	a.openVoiceEditor(v.ID)
	tt.Frame()
	tt.Click("Voice ownership")
	tt.Frame()
	if !tt.HasText("“" + consentStatement() + "”") {
		t.Fatalf("statement: %q", tt.Texts())
	}
	if err := tt.Click("Record"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	start := params[proto.CaptureParams](t, eng.lastOf(t, proto.MethodCaptureStart))
	writeSpeech(start.Output.Path, 4, []span{{0.2, 3.8}})
	tt.Click("Stop recording")
	tt.Frame()
	got := a.findVoice(v.ID)
	if !got.Verified() || got.Consent.Statement != consentStatement() || got.Consent.Locale != "en" {
		t.Fatalf("consent %+v", got.Consent)
	}
	if !tt.HasText("Verified") {
		t.Errorf("badge: %q", tt.Texts())
	}
	tt.Click("Revoke")
	tt.Frame()
	tt.Click("Revoke verification")
	if a.findVoice(v.ID).Verified() {
		t.Error("not revoked")
	}
}

// testPNG writes a w×h PNG with EXIF-like text chunk and returns its path.
func testPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	path := filepath.Join(t.TempDir(), "face.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPortraits(t *testing.T) {
	data, err := portraitPNG(testPNG(t, 640, 360))
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || format != "png" || img.Bounds().Dx() != portraitSize || img.Bounds().Dy() != portraitSize {
		t.Fatalf("%v %s %v", err, format, img.Bounds())
	}
	// The centre square: the left 140 columns are cropped away, so the
	// first pixel's red is about 140.
	if r, _, _, _ := img.At(0, 0).RGBA(); r>>8 < 130 || r>>8 > 150 {
		t.Errorf("crop: red %d", r>>8)
	}
	if _, err := portraitPNG("/x/face.gif"); err != errPortraitType {
		t.Errorf("gif: %v", err)
	}
	big := filepath.Join(t.TempDir(), "big.png")
	os.WriteFile(big, make([]byte, portraitMaxBytes+1), 0o644)
	if _, err := portraitPNG(big); err != errPortraitLarge {
		t.Errorf("big: %v", err)
	}
	fake := filepath.Join(t.TempDir(), "fake.jpg")
	os.WriteFile(fake, []byte("not an image"), 0o644)
	if _, err := portraitPNG(fake); err != errPortraitType {
		t.Errorf("fake: %v", err)
	}

	// On a saved voice.
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	v := addVoice(t, a, core.Voice{Name: "Face", Kind: core.VoiceCloned}, 3)
	a.setVoicePortrait(v.ID, testPNG(t, 100, 100))
	got := a.findVoice(v.ID)
	if got.Portrait != "portrait.png" {
		t.Fatalf("portrait %+v", got)
	}
	a.navigate("/voices")
	tt.Frame()
	tt.Frame()
	if a.portraits[v.ID] == nil {
		t.Error("portrait not decoded")
	}
	a.setVoicePortrait(v.ID, fake)
	tt.Frame()
	if !tt.HasText("Choose a PNG, JPEG or WebP image.") {
		t.Errorf("toast: %q", tt.Texts())
	}
}

func TestVoiceFilesUI(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	tt := ui.NewTester(a.view, 1200, 1800)
	ctx := context.Background()
	v := addVoice(t, a, core.Voice{Name: "Shared", Kind: core.VoiceCloned, RefText: "Hello."}, 4)
	out := filepath.Join(t.TempDir(), "Shared.asvoice")
	if err := a.lib.svc.Voices.Export(ctx, v.ID, out, core.VoiceExport{IncludeReference: true}); err != nil {
		t.Fatal(err)
	}
	// Opened from Finder.
	a.openVoiceFile(out)
	tt.Frame()
	if len(a.voices) != 2 || a.edit == nil || a.edit.id == v.ID || a.router.Path() != "/voices/mine" {
		t.Fatalf("import: %d %+v", len(a.voices), a.edit)
	}
	if !tt.HasText("Imported “Shared”") {
		t.Errorf("toast: %q", tt.Texts())
	}
	// Dropped on the library: a voice file imports, a clip starts a voice.
	a.voicesDropped([]string{out})
	if len(a.voices) != 3 {
		t.Errorf("drop import: %d", len(a.voices))
	}
	clip := filepath.Join(t.TempDir(), "drop.wav")
	writeSpeech(clip, 6, []span{{0.2, 5.8}})
	a.voicesDropped([]string{clip})
	if a.router.Path() != "/voices/new/clone" || !a.clone.hasClip() {
		t.Errorf("drop clip: %s", a.router.Path())
	}
	// A broken file.
	bad := filepath.Join(t.TempDir(), "bad.asvoice")
	os.WriteFile(bad, []byte("zip?"), 0o644)
	a.importVoice(bad)
	tt.Frame()
	if !tt.HasText("bad.asvoice is not a voice file, or it is damaged.") {
		t.Errorf("bad: %q", tt.Texts())
	}

	// The non-commercial note on export.
	nc := core.Voice{CreatedWith: omni}
	if !strings.Contains(ncNote(&nc), "non-commercial") || ncNote(&core.Voice{CreatedWith: qwenBase}) != "" {
		t.Error("nc note")
	}
	labels, ids := licenceChoices()
	if len(labels) != len(persona.Licences) || ids[0] != "" || labels[0] != "Personal use (no sharing)" {
		t.Errorf("licences %v %v", labels, ids)
	}
}

func TestStudioSeam(t *testing.T) {
	a, _ := newVoiceTestApp(t)
	kok, _ := catalog.Builtin().ByRepo(kokoro)
	om, _ := catalog.Builtin().ByRepo(omni)
	base, _ := catalog.Builtin().ByRepo(qwenBase)
	d := addVoice(t, a, core.Voice{Name: "Elder", Kind: core.VoiceDesigned, Language: "en",
		DesignAttrs: designAttrs(vd.Attributes{Gender: "male", Age: "elderly", Accent: "british"}), Seed: 42}, 0)
	c := addVoice(t, a, core.Voice{Name: "Clone", Kind: core.VoiceCloned, RefText: "Words.", Style: "calm"}, 4)

	a.studioVoice = d.ID
	tk := core.Take{Model: omni}
	a.applyStudioVoice(&tk, om)
	if tk.VoiceID != d.ID || tk.Seed != 42 || !strings.Contains(tk.Voice, "elderly") || !strings.Contains(tk.Voice, "british accent") {
		t.Errorf("design on OmniVoice %+v", tk)
	}
	tk = core.Take{Model: kokoro, Voice: "af_heart"}
	a.applyStudioVoice(&tk, kok)
	if tk.Voice != "af_heart" || tk.VoiceID != "" || a.studioUsesSavedVoice(kok) {
		t.Errorf("design on Kokoro %+v", tk)
	}
	// Without a sample a designed voice can't be cloned by Qwen3 Base.
	if ok, _ := voiceUsable(a.findVoice(d.ID), base); ok {
		t.Error("design usable on base")
	}

	a.studioVoice = c.ID
	tk = core.Take{Model: qwenBase}
	a.applyStudioVoice(&tk, base)
	if tk.RefAudio != a.voicePath(&c, c.RefAudio) || tk.RefText != "Words." || tk.Voice != "" || tk.VoiceID != c.ID {
		t.Errorf("clone on Qwen3 Base %+v", tk)
	}
	tk = core.Take{Model: omni}
	a.applyStudioVoice(&tk, om)
	if tk.Voice != "" || tk.RefAudio == "" {
		// "calm" has no OmniVoice tag: nothing is sent as style.
		t.Errorf("clone on OmniVoice %+v", tk)
	}

	// A cached prompt is offered for models that reuse one.
	pr := filepath.Join(t.TempDir(), "p.bin")
	os.WriteFile(pr, []byte("prompt"), 0o644)
	cc, _ := a.lib.svc.Voices.SaveFiles(context.Background(), *a.findVoice(c.ID), core.VoiceFiles{Prompt: pr, PromptModel: qwenBase})
	withPrompt := base
	withPrompt.Capabilities = append(slices.Clone(withPrompt.Capabilities), catalog.CapVoicePrompt)
	without := base // the catalogue gives Qwen3 Base voicePrompt since M1-E: take it away
	without.Capabilities = slices.DeleteFunc(slices.Clone(without.Capabilities), func(c catalog.Capability) bool { return c == catalog.CapVoicePrompt })
	if got := a.studioVoicePrompt(withPrompt); got != a.voicePath(&cc, cc.Prompts[qwenBase]) || got == "" || a.studioVoicePrompt(without) != "" {
		t.Errorf("voice prompt %q", got)
	}

	// The picker lists only the voices the model can use.
	a.studioVoice = ""
	labels, _ := a.studioVoiceOptions(kok)
	if len(labels) != 1 {
		t.Errorf("Kokoro options %v", labels)
	}
	labels, ids := a.studioVoiceOptions(om)
	if len(labels) != 3 || ids[1] == "" {
		t.Errorf("OmniVoice options %v", labels)
	}
	// It renders in a view, and Manage voices… opens the library.
	a.studioVoice = c.ID
	tt := ui.NewTester(func(ctx *ui.Context) {
		ui.Column(ctx).Fill().Children(func() { a.studioVoiceField(ctx, kok) })
	}, 400, 1400) // the list shows all of Kokoro's voices
	if !tt.HasText("Kokoro 82M can't clone voices. Choose a model that clones, such as Qwen3-TTS Base or OmniVoice.") && !strings.Contains(strings.Join(tt.Texts(), "|"), "can't clone voices") {
		t.Errorf("picker: %q", tt.Texts())
	}
	a.voicePickerOpen(studioVoiceKey, true)
	tt.Frame()
	tt.Click("Manage voices…")
	if a.router.Path() != "/voices/mine" {
		t.Errorf("manage: %s", a.router.Path())
	}
	// Use in Studio picks a model that can use the voice.
	a.useVoiceInStudio(a.findVoice(d.ID))
	if a.preset().Repo != omni || a.studioVoice != d.ID || a.router.Path() != "/studio" {
		t.Errorf("use in studio: %s", a.preset().Repo)
	}
	// Deleting the voice clears the choice.
	a.lib.svc.Voices.Delete(context.Background(), d.ID)
	if a.studioVoice != "" {
		t.Error("deleted voice still chosen")
	}
}
