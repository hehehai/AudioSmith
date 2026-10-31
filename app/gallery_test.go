package main

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/gallery"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

func TestGalleryPage(t *testing.T) {
	dir := t.TempDir()
	a, eng, _ := newTestAppIn(t, dir)
	tt := ui.NewTester(a.view, 1200, 1800)
	g := &a.gallery
	a.navigate("/gallery")
	tt.Frame()
	all := gallery.Default().Len()
	first := gallery.Default().All()[0]
	if !tt.HasText(itoa(all)+" voices") || !tt.HasText(first.Name.En) {
		t.Fatalf("gallery: %q", tt.Texts())
	}
	// No preview model to choose, nothing more to load: all of them fit.
	if len(g.page.Items) != all || g.page.Next != -1 {
		t.Errorf("page: %d of %d, next %d", len(g.page.Items), all, g.page.Next)
	}

	// Search, a use case, a facet.
	g.text = "grandma"
	tt.Frame()
	if g.page.Total == 0 || g.page.Total >= all {
		t.Errorf("search: %d", g.page.Total)
	}
	g.text = ""
	tt.Frame()
	chars := gallery.Default().Search(gallery.Query{Category: "characters"}).Total
	if err := tt.Click("Characters & kids " + itoa(chars)); err != nil { // a use case chip
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if g.page.Total != chars || g.tab == 0 {
		t.Errorf("tab: %d %d", g.page.Total, g.tab)
	}
	if err := tt.Click("Gender"); err != nil {
		t.Fatal(err)
	}
	var female string
	for _, it := range tt.Menu() {
		if strings.HasPrefix(it, "Female") {
			female = it
		}
	}
	if female == "" {
		t.Fatalf("menu %q", tt.Menu())
	}
	if err := tt.ChooseMenuItem(female); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if g.filters[gallery.FacetGender] != "female" || !tt.HasText("Gender: Female") {
		t.Errorf("facet: %v %q", g.filters, tt.Texts())
	}
	for _, it := range g.page.Items {
		if !slices.Contains(it.Uses, "characters") || it.Gender != "female" {
			t.Fatalf("filtered item %+v", it)
		}
	}
	// The filters are the use case, language, gender and age: no pitch,
	// accent or style any more.
	for _, gone := range []string{"Pitch", "Accent or dialect", "Style"} {
		if tt.HasText(gone) {
			t.Errorf("filter %q is still there", gone)
		}
	}
	tt.Click("Reset")
	tt.Frame()
	if g.page.Total != all {
		t.Errorf("reset: %d", g.page.Total)
	}

	// Favorites persist (the star is in the voice's detail).
	a.openArchetype(first.ID)
	tt.Frame()
	if err := tt.Click("Add to favorites"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !g.favs.Has(first.ID) {
		t.Fatal("not a favorite")
	}
	tt.Click("Favorites")
	tt.Frame()
	if g.page.Total != 1 {
		t.Errorf("favorites only: %d", g.page.Total)
	}
	g.favOnly = false
	b, _, _ := newTestAppIn(t, dir)
	if !b.gallery.favs.Has(first.ID) {
		t.Error("favorite not saved")
	}
	tt.Frame()

	// Preview plays the bundled clip: no job, no licence.
	sent := len(eng.sent)
	g.text = "Osmanthus"
	tt.Frame()
	arch, _ := gallery.Default().ByID("c-grandma-osmanthus")
	if p := archetypePreview(arch); !p.ok() || len(p.data) == 0 {
		t.Fatalf("no bundled preview for %s", arch.ID)
	}
	if err := tt.Click("Preview"); err != nil {
		t.Fatal(err)
	}
	if len(eng.sent) != sent || a.licenceOpen {
		t.Fatalf("a preview sent %d jobs (licence %v)", len(eng.sent)-sent, a.licenceOpen)
	}
	a.pl.Stop()

	// Save to My voices: a designed voice with its persona, seed, the
	// bundled reference (heard saying its text: nothing to make, nothing
	// to check) and preview.
	tt.Click("More actions")
	if err := tt.ChooseMenuItem("Save to My voices"); err != nil {
		t.Fatal(err)
	}
	if len(a.voices) != 1 {
		t.Fatalf("voices %+v", a.voices)
	}
	v := a.voices[0]
	if v.Origin != "gallery:"+arch.ID || v.Kind != core.VoiceDesigned || v.Seed != arch.Seed || v.Design != arch.Persona || v.Name != arch.Name.En ||
		v.RefAudio == "" || v.RefText != arch.RefText || v.Sample == "" || v.RefCheck == nil || !v.RefCheck.TextChecked || v.RefSeconds < 8 {
		t.Fatalf("voice %+v", v)
	}
	if needsReference(&v) || a.refUnchecked(&v) || a.refWork(&v) {
		t.Error("a curated voice still has reference work")
	}
	if b, _, err := wav.ReadFile(a.voicePath(&v, v.RefAudio)); err != nil || b.Seconds() < 8 {
		t.Fatalf("reference: %v", err)
	}
	if a.router.Path() != "/voices/discover" {
		t.Errorf("save moved to %s", a.router.Path())
	}
	// Using it finds it; 「使用」 picks it in the Studio, on the clone
	// model the Studio speaks designed voices with: its reference as it is.
	tt.Frame()
	if err := tt.Click("Use"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.voices) != 1 || a.studioVoice != v.ID || a.router.Path() != "/studio" || a.preset().ID != "qwen3-tts-1.7b-base-8bit" {
		t.Errorf("use in studio: %d %s %s", len(a.voices), a.router.Path(), a.preset().Repo)
	}
	var tk core.Take
	a.fillVoice(&tk, a.findVoice(v.ID), a.preset())
	if tk.RefAudio != a.voicePath(&v, v.RefAudio) || tk.RefText != arch.RefText {
		t.Errorf("studio take: %q %q", tk.RefAudio, tk.RefText)
	}
}

// TestGalleryBuiltinVoice: a built-in speaker of the gallery previews
// its bundled clip and is saved as that model's speaker, used in the
// Studio.
func TestGalleryBuiltinVoice(t *testing.T) {
	a, eng, _ := newTestAppIn(t, t.TempDir())
	tt := ui.NewTester(a.view, 1200, 1800)
	arch, ok := gallery.Default().ByID("c-af_heart")
	if !ok {
		t.Fatal("no c-af_heart")
	}
	if p := archetypePreview(arch); !p.ok() {
		t.Fatal("no preview")
	}
	a.navigate("/gallery")
	a.gallery.text = "af_heart"
	tt.Frame()
	sent := len(eng.sent)
	if err := tt.Click("Use"); err != nil { // the row's 「使用」
		t.Fatal(err)
	}
	if len(eng.sent) != sent || len(a.voices) != 1 {
		t.Fatalf("sent %d, voices %d", len(eng.sent)-sent, len(a.voices))
	}
	v := a.voices[0]
	e := entry(t, arch.Model)
	if v.Kind != core.VoiceBuiltin || v.Model != e.Repo || v.Speaker != "af_heart" || v.Origin != "gallery:c-af_heart" {
		t.Fatalf("voice %+v", v)
	}
	if a.router.Path() != "/studio" || a.studioVoice != v.ID || a.preset().ID != arch.Model {
		t.Errorf("studio: %s %s %s", a.router.Path(), a.studioVoice, a.preset().ID)
	}
}

// TestGalleryOldIDs: the earlier gallery's ids are not listed but still
// resolve (a dub cast, an agent naming one), and a voice saved from one
// keeps working: it carries its own recipe.
func TestGalleryOldIDs(t *testing.T) {
	a, _, _ := newTestAppIn(t, t.TempDir())
	g := gallery.Default()
	for _, id := range []string{"f-grandma-osmanthus", "f-monsoon-stories", "g-quest-giver-gravelly-zh-mm"} {
		arch, ok := g.ByID(id)
		if !ok || !arch.Legacy || !arch.Designed() {
			t.Fatalf("%s: %v %+v", id, ok, arch)
		}
		if g.Search(gallery.Query{Text: id}).Total != 0 {
			t.Errorf("%s is listed", id)
		}
		// A dub cast naming it: its recipe, as before.
		r, _, ok := dubDesignRecipe("gallery:" + id)
		if !ok || r.Seed != arch.Seed || r.Attrs.Gender != arch.Attrs.Gender {
			t.Errorf("%s: dub recipe %+v", id, r)
		}
	}
	// A voice saved from an old archetype before (its own attributes and
	// seed, no reference yet): the Studio speaks it, a reference is made
	// from its own recipe when a clone model needs one.
	old, _ := g.ByID("g-quest-giver-gravelly-zh-mm")
	attrs, _ := json.Marshal(old.Attrs)
	saved, err := a.lib.svc.Voices.SaveFiles(context.Background(), core.Voice{Name: "沙哑的任务 NPC", Kind: core.VoiceDesigned, DesignAttrs: attrs,
		Seed: old.Seed, Language: "zh", Origin: "gallery:" + old.ID}, core.VoiceFiles{})
	if err != nil {
		t.Fatal(err)
	}
	a.voiceChanged(core.VoiceChanged{Voice: saved})
	v := a.findVoice(saved.ID)
	if r := voiceRecipe(v); r.Seed != old.Seed || r.Attrs.Gender != "male" || r.Description != "" {
		t.Errorf("recipe %+v", r)
	}
	vdm := withDesignCatalog(t)
	var tk core.Take
	a.fillVoice(&tk, v, vdm)
	if tk.Voice == "" || tk.Seed != old.Seed {
		t.Errorf("design take %+v", tk)
	}
	if !needsReference(v) {
		t.Error("an old voice without a reference should get one made")
	}
	// Hidden presets resolve too.
	for _, id := range []string{"whisper", "kid", "upbeat", "northeast-buddy", "calm-guide"} {
		if _, _, ok := dubDesignRecipe("preset:" + id); !ok {
			t.Errorf("preset %s does not resolve", id)
		}
		if a.dubVoiceLabel("preset:"+id) == "Missing voice" {
			t.Errorf("preset %s has no label", id)
		}
	}
}

// TestGalleryCuratedAssets: every curated voice is complete: a designed
// one has its persona (sent as written), a tested seed, a bundled
// reference that decodes to 8–20 s with its text, and a preview; a
// built-in one names a visible speaker of a catalogue model with a
// bundled preview. The bundle stays small.
func TestGalleryCuratedAssets(t *testing.T) {
	langs := map[string]int{}
	for _, arch := range gallery.Default().All() {
		langs[arch.Languages[0]]++
		if p := archetypePreview(arch); !p.ok() || len(p.data) == 0 {
			t.Errorf("%s: no bundled preview", arch.ID)
		}
		switch arch.Kind {
		case gallery.KindBuiltin:
			e, ok := catalog.Builtin().ByID(arch.Model)
			if !ok || !slices.Contains(e.VoiceIDs(), arch.Speaker) {
				t.Errorf("%s: %s has no visible speaker %s", arch.ID, arch.Model, arch.Speaker)
			}
			if lang := e.SpeakerLanguage(arch.Speaker); lang != arch.Languages[0] {
				t.Errorf("%s: speaker language %q, listed as %q", arch.ID, lang, arch.Languages[0])
			}
		case gallery.KindDesigned:
			r := arch.Recipe()
			low, err := vd.Lower(vd.TargetQwen3Design, r, vd.Options{Lang: designTextLang(arch.Languages[0])})
			if err != nil || low.Voice != arch.Persona || low.Seed != arch.Seed {
				t.Errorf("%s: the persona is not sent as written: %q", arch.ID, low.Voice)
			}
			ref := galleryClip(arch.ID, "ref")
			b, err := decodeVorbis(ref)
			if err != nil || b.Seconds() < 8 || b.Seconds() > 20 {
				t.Errorf("%s: reference %v, %.1f s", arch.ID, err, b.Seconds())
			}
			if (arch.Age == "child") != (r.Attrs.Register() == vd.RegisterChild) {
				t.Errorf("%s: register %s for age %s", arch.ID, r.Attrs.Register(), arch.Age)
			}
		}
	}
	if langs["zh"] < 10 || langs["en"] < 10 || len(langs) < 6 {
		t.Errorf("languages %v", langs)
	}
	var total int64
	entries, _ := galleryVoicesFS.ReadDir(galleryVoicesDir)
	for _, f := range entries {
		info, _ := f.Info()
		total += info.Size()
	}
	if total > 6<<20 {
		t.Errorf("bundle %d bytes, over 6 MB", total)
	}
}

// TestGalleryDubUsesBundledReference: a dub cast naming a curated
// designed voice clones its bundled reference on a model that clones; a
// curated built-in speaker speaks as itself on its own model.
func TestGalleryDubUsesBundledReference(t *testing.T) {
	a, _, _ := newTestAppIn(t, t.TempDir())
	resolve := a.dubVoiceResolver(a.lib)
	base := entry(t, "qwen3-tts-0.6b-base-8bit")
	spec, err := resolve(context.Background(), "gallery:c-grandma-osmanthus", base.Repo, "zh")
	arch, _ := gallery.Default().ByID("c-grandma-osmanthus")
	if err != nil || spec.RefAudio == "" || spec.RefText != arch.RefText || spec.Seed != arch.Seed {
		t.Fatalf("spec %+v %v", spec, err)
	}
	if _, err := os.Stat(spec.RefAudio); err != nil {
		t.Fatal(err)
	}
	kokoro := entry(t, "kokoro-82m-bf16")
	if spec, err := resolve(context.Background(), "gallery:c-af_heart", kokoro.Repo, "en"); err != nil || spec.Voice != "af_heart" {
		t.Errorf("builtin %+v %v", spec, err)
	}
}
