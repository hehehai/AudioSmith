package main

// Voice Design (/voices/new/design; the guided page is design_flow.go): a voice from a description and attribute picks
// (internal/voicedesign), with a seed for its identity, lowered to what
// the chosen model takes, previewed through TakeService and saved as a
// designed voice profile.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
	vd "github.com/hehehai/AudioSmith/internal/voicedesign"
)

// designState is the Design page's draft.
type designState struct {
	description string
	parsedFor   string    // the description parsed is of
	parsed      vd.Result // its mapping
	attrs       vd.Attributes
	seed        float64 // bound to the seed input; a whole number in [0, MaxSeed]
	model       string  // label of the model it is for
	script      string
	language    string // speech language; "" = from the attributes or the script
	linked      string // the saved designed voice the draft is, "" for a new design
	saveName    string
	preset      string // the starting point last chosen
	detailsOpen bool
	savedOpen   bool
	// The guided page (design_flow.go): the candidates, the one picked
	// (-1 none), the voice saved from it.
	cands   []designCand
	picked  int
	savedID string
}

// reset starts a new design: a random seed, the default line.
func (d *designState) reset() {
	*d = designState{seed: float64(vd.NewSeed()), script: i18n.T("Hello! This is the voice you designed. How does it sound?"), picked: -1}
}

// recipe is the draft's recipe.
func (d *designState) recipe() vd.Recipe {
	return vd.Recipe{Description: d.description, Attrs: d.attrs.Canonical(), Seed: vd.FoldSeed(int64(d.seed))}
}

// speechLang is the language the preview speaks.
func (d *designState) speechLang() string {
	return firstOf(d.language, d.attrs.Language(), vd.TextLang(d.script))
}

// reparse maps a changed description (capped at vd.MaxDescription
// characters). A pick the user made in a category holds until the
// description changes what it says about that category.
func (d *designState) reparse() {
	if r := []rune(d.description); len(r) > vd.MaxDescription {
		d.description = string(r[:vd.MaxDescription])
	}
	if d.description == d.parsedFor {
		return
	}
	old := d.parsed.Attrs
	d.parsed, d.parsedFor = vd.Parse(d.description), d.description
	now := d.parsed.Attrs
	merged := now
	for _, c := range vd.Categories {
		picked := !slices.Equal(d.attrs.Get(c), old.Get(c))
		unchanged := slices.Equal(old.Get(c), now.Get(c))
		if picked && unchanged {
			merged = merged.Without(c, "")
			for _, v := range d.attrs.Get(c) {
				merged, _, _ = merged.With(c, v)
			}
		}
	}
	if merged.Validate() != nil {
		merged = now
	}
	d.attrs = merged.Canonical()
}

// pick sets a category's value ("" = Auto) and returns what it cleared.
func (d *designState) pick(c vd.Category, value string) []vd.Cleared {
	if value == "" {
		d.attrs = d.attrs.Without(c, "")
		return nil
	}
	next, cleared, err := d.attrs.With(c, value)
	if err != nil {
		return nil
	}
	d.attrs = next
	return cleared
}

// clearedText says what a pick cleared, for a toast.
func clearedText(cs []vd.Cleared) string {
	loc := i18n.Locale()
	var parts []string
	for _, x := range cs {
		info, _ := vd.Info(x.Category)
		op, _ := vd.Lookup(x.Category, x.ID)
		parts = append(parts, info.Label.In(loc)+": "+op.Label.In(loc))
	}
	if len(parts) == 0 {
		return ""
	}
	return i18n.T("Cleared %s: it can't go with your pick.", strings.Join(parts, ", "))
}

// applyPreset replaces the recipe with a starting point's: its
// description fills the prompt (0.5 F4), its attributes the details.
func (a *app) applyPreset(p gallery.Preset) {
	d := &a.design
	d.description = p.Description.In(i18n.Locale())
	d.parsedFor, d.parsed = d.description, vd.Parse(d.description)
	d.attrs, d.preset = p.Attrs.Canonical(), p.ID
	if p.Demo && p.Script.En != "" {
		d.script = p.Script.In(p.Language)
		d.language = p.Language
	}
}

// openDesignFor opens Design on a saved designed voice: its recipe,
// seed and language; the draft is linked to it until it changes.
func (a *app) openDesignFor(v *core.Voice) {
	d := &a.design
	r := voiceRecipe(v)
	script := d.script
	d.reset()
	d.script = script
	d.description, d.parsedFor, d.parsed = r.Description, r.Description, vd.Parse(r.Description)
	d.attrs, d.seed, d.language, d.linked, d.saveName = r.Attrs.Canonical(), float64(r.Seed), v.Language, v.ID, v.Name
	if e, ok := a.designModelFor(v.CreatedWith); ok {
		d.model = e.Label
	}
	a.navigate("/design")
}

// syncLinked unlinks the draft from its saved voice once it differs.
func (a *app) syncLinked() {
	d := &a.design
	v := a.findVoice(d.linked)
	if v == nil {
		d.linked = ""
		return
	}
	if voiceRecipe(v).Key() != d.recipe().Key() {
		d.linked = ""
	}
}

// designGenerate renders the script with the draft's voice.
func (a *app) designGenerate() {
	d := &a.design
	e, ok := voiceCatalog().ByLabel(d.model)
	if !ok || !canDesign(e) || strings.TrimSpace(d.script) == "" {
		return
	}
	lang := d.speechLang()
	low, err := vd.Lower(designTarget(e), d.recipe(), vd.Options{Lang: designTextLang(lang)})
	if err != nil {
		a.toastWarn(err.Error())
		return
	}
	t := core.Take{Text: strings.TrimSpace(d.script), Model: e.Repo, Voice: low.Voice, Seed: low.Seed,
		Language: modelLanguage(e, lang), VoiceID: d.linked}
	a.voiceGenerate("design", "", t, a.designGenerate)
}

// designSave saves the draft as a designed voice (or updates the linked
// one). Its sample is the last preview when that is of this very recipe;
// otherwise one is rendered when the engine is ready, else on the first
// preview. Its reference (voice_reference.go) is made after it, or when it
// is first needed.
func (a *app) designSave() {
	d := &a.design
	name := strings.TrimSpace(d.saveName)
	if name == "" {
		a.toast(i18n.T("Give the voice a name."))
		return
	}
	e, _ := voiceCatalog().ByLabel(d.model)
	r := d.recipe()
	attrs, _ := json.Marshal(r.Attrs)
	v := core.Voice{Name: name, Kind: core.VoiceDesigned, Design: strings.TrimSpace(d.description), DesignAttrs: attrs,
		Seed: r.Seed, Language: d.speechLang(), CreatedWith: e.Repo}
	var files core.VoiceFiles
	if old := a.findVoice(d.linked); old != nil && old.Name == name {
		cur := *old
		cur.Design, cur.DesignAttrs, cur.Seed, cur.Language, cur.CreatedWith = v.Design, v.DesignAttrs, v.Seed, v.Language, v.CreatedWith
		// Another design: its reference was of the old one.
		files.RemoveReference = voiceRecipe(old).Key() != voiceRecipe(&cur).Key() || old.CreatedWith != cur.CreatedWith
		v = cur
	}
	if tk := a.designSample(); tk != nil && tk.Model == e.Repo && tk.Seed == r.Seed {
		files.Sample = a.takeStore.AudioPath(*tk)
		v.SampleText = tk.Text
	}
	svc, ready := a.lib.svc.Voices, a.engState == engineReady
	a.background(func() {
		saved, err := svc.SaveFiles(context.Background(), v, files)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save the voice profile: %v", err))
				return
			}
			a.voiceChanged(core.VoiceChanged{Voice: saved})
			d.linked, d.savedID = saved.ID, saved.ID // the page's last step: the hand-offs
			a.toast(i18n.T("Voice profile saved"))
			if saved.Sample == "" && ready {
				a.renderSample(&saved)
			}
			// Its reference for clone and dialogue models (#83): made now,
			// or when it is first needed.
			if needsReference(&saved) && ready {
				a.makeReference(&saved, nil)
			}
		})
	})
}

// designKey identifies the draft's voice as sent: model, lowered voice
// and seed. A preview take with the same key is a sample of the draft.
func (a *app) designKey() string {
	d := &a.design
	e, ok := voiceCatalog().ByLabel(d.model)
	if !ok {
		return ""
	}
	low, err := vd.Lower(designTarget(e), d.recipe(), vd.Options{Lang: designTextLang(d.speechLang())})
	if err != nil {
		return ""
	}
	return vd.Key(e.Repo, low.Voice, itoa(int(low.Seed)))
}

func (a *app) designTakeKey(t *core.Take) string {
	return vd.Key(t.Model, t.Voice, itoa(int(t.Seed)))
}
