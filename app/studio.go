package main

// The Studio's form and its actions. The form autosaves as a draft
// (core.DraftService, key "studio") and comes back at the next start.
// generate builds the take's recipe (text, model, voice, parameters and
// the output options of the render pipeline) and hands it to the engine
// link (startTake), which queues a take.render job through TakeService.
//
// Its parts:
//
//	studio_view.go     the page: script column, generate bar, job status
//	studio_panel.go    the right panel: 设置 | 历史 (0.5 F3)
//	studio_history.go  the 历史 tab and a take's hand-offs
//	studio_examples.go the example chips under an empty script
//	studio_recommend.go 「✦ 推荐」: voices for the script, by rule
//	studio_inspector.go the 设置 tab: voice, model, speed, rows, 高级
//	studio_editor.go   the script editor (markup highlighting, diagnostics, import)
//	studio_controls.go the insert menu, language picker, Audio quality and Voice controls popovers
//	studio_output.go   the latest take: details, Check audio, Save as…, drag out
//	studio_preview.go  the streaming preview and the render's chunk progress
//	studio_voice.go    the voice and cloning section (seam for the voice library)
//	studio_notices.go  translations of render and markup findings

import (
	"context"
	"crypto/rand"
	"math/big"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// studioBits is the bit depth takes are kept at: room for editing; the
// Export audio dialog chooses the file's (thread #42).
const studioBits = 24

// bitDepths are the WAV bit depths offered (a book's Save as).
var bitDepths = []int{16, 24, 32}

// studioState is the Studio form.
type studioState struct {
	text          string
	modelLabel    string // a catalog label, or catalog.CustomLabel
	customRepo    string
	voice         string
	language      string // the value the model's language parameter takes; "" = auto
	speed         float64
	modelDefaults bool // drafts before version 3: leave the sampling to the model
	// temperature and topP: 0 = the model's recommended (presets);
	// changes are kept per model (settings.Studio.Overrides).
	temperature float64
	topP        float64
	maxTokens   float64 // drafts before version 3 only
	refAudio    string
	refText     string
	duration    float64 // target seconds (models with CapDuration); 0 = natural
	// autoplay mirrors settings.Studio.Autoplay; the checkbox changes the setting.
	autoplay bool

	// Audio quality.
	steps     float64 // diffusion steps (models with Steps); 0 = the model's
	evenOut   bool    // master with services.MasterDefault
	normalize bool    // read numbers, dates… as words
	// Voice controls: a pinned seed makes takes repeatable.
	seedPinned bool
	seed       float64
	// Model-specific controls (catalogue capabilities); 0 = the model's.
	guidance float64
	// recentLangs are the languages chosen last, newest first (codes).
	recentLangs [4]string
	// studioAuto: the model was picked for a new user (nothing in the
	// settings, no draft); it follows their saved voices until they
	// choose one (studio_presets.go).
	studioAuto bool

	script      scriptEditor
	composer    composerState
	inspectorUI studioInspectorState // studio_inspector.go
	ttsUI       studioPanelState     // the right panel, studio_panel.go (0.5 F3)
	pron        pronState            // Settings → Pronunciation
	renderState                      // the render the Studio follows: chunk progress, preview

	// savedDraft is the form as last handed to the draft service (or restored).
	savedDraft studioDraft
}

// studioFreshScript is a fresh start's script: empty, so the example
// chips show under it (0.5 F3). The tests written before F3 set the old
// default (TestMain).
var studioFreshScript = ""

// newStudioState is the form of a fresh start: the settings' default
// model and autoplay (the draft, restored next, may change the model).
func newStudioState(prefs settings.Settings) studioState {
	first := catalog.Builtin().ForTask(catalog.TaskTTS)[0]
	if e, ok := catalog.Builtin().ByID(prefs.Studio.Model); ok && !e.Hidden && slices.Contains(e.Tasks, catalog.TaskTTS) {
		first = e
	} else if e, _, ok := replaceRemoved(prefs.Studio.Model, false); ok {
		first = e // the settings move to it as the shell opens (migrateRemovedSettings)
	}
	s := studioState{
		modelLabel:    first.Label,
		speed:         1,
		modelDefaults: true,
		autoplay:      prefs.Studio.Autoplay,
		evenOut:       true,
		normalize:     true,
		text:          studioFreshScript,
	}
	s.loadPreset(first, prefs.Studio.Overrides[first.ID])
	s.script = newScriptEditor()
	s.savedDraft = s.toDraft()
	return s
}

// studioDraft is the Studio form as saved between runs. Version 2 added
// the output options, the seed and the recent languages; version 3 the
// recommended settings (a temperature or top-p of 0 = the model's
// recommended; ModelDefaults and MaxTokens are read from older drafts).
type studioDraft struct {
	Version       int     `json:"version"`
	Text          string  `json:"text"`
	ModelLabel    string  `json:"modelLabel"`
	CustomRepo    string  `json:"customRepo,omitempty"`
	Voice         string  `json:"voice"`
	Language      string  `json:"language"`
	Speed         float64 `json:"speed"`
	ModelDefaults bool    `json:"modelDefaults"`
	Temperature   float64 `json:"temperature"`
	TopP          float64 `json:"topP"`
	MaxTokens     float64 `json:"maxTokens"`
	RefAudio      string  `json:"refAudio,omitempty"`
	RefText       string  `json:"refText,omitempty"`
	Duration      float64 `json:"duration,omitempty"`

	Steps       float64   `json:"steps,omitempty"`
	Bits        int       `json:"bits,omitempty"`
	RawVolume   bool      `json:"rawVolume,omitempty"`   // Even out volume off
	NoNormalize bool      `json:"noNormalize,omitempty"` // normalisation off
	SeedPinned  bool      `json:"seedPinned,omitempty"`
	Seed        float64   `json:"seed,omitempty"`
	Recent      [4]string `json:"recentLanguages"`
	Guidance    float64   `json:"guidance,omitempty"`
}

const studioDraftKey = "studio"

func (s *studioState) toDraft() studioDraft {
	return studioDraft{Version: 3, Text: s.text, ModelLabel: s.modelLabel, CustomRepo: s.customRepo,
		Voice: s.voice, Language: s.language, Speed: s.speed, ModelDefaults: s.modelDefaults,
		Temperature: s.temperature, TopP: s.topP, MaxTokens: s.maxTokens,
		RefAudio: s.refAudio, RefText: s.refText, Duration: s.duration,
		Steps: s.steps, Bits: studioBits, RawVolume: !s.evenOut,
		NoNormalize: !s.normalize, SeedPinned: s.seedPinned, Seed: s.seed, Recent: s.recentLangs,
		Guidance: s.guidance}
}

// applyDraft restores a draft, ignoring a model the catalogue no longer
// has (restoreStudioDraft first moves a removed one to its replacement).
func (s *studioState) applyDraft(d studioDraft) {
	if e, ok := catalog.Builtin().ByLabel(d.ModelLabel); d.ModelLabel != catalog.CustomLabel && (!ok || e.Hidden || !slices.Contains(e.Tasks, catalog.TaskTTS)) {
		return
	}
	s.text, s.modelLabel, s.customRepo = d.Text, d.ModelLabel, d.CustomRepo
	s.voice, s.language = d.Voice, d.Language
	if d.Speed > 0 {
		s.speed = d.Speed
	}
	s.modelDefaults = d.ModelDefaults
	s.temperature, s.topP, s.maxTokens = d.Temperature, d.TopP, 0
	if d.Version < 3 && d.ModelDefaults {
		// "Use the model's defaults" now means its recommended settings.
		s.temperature, s.topP = 0, 0
	}
	s.refAudio, s.refText, s.duration = d.RefAudio, d.RefText, d.Duration
	s.steps, s.evenOut, s.normalize = d.Steps, !d.RawVolume, !d.NoNormalize
	s.seedPinned, s.seed, s.recentLangs = d.SeedPinned, d.Seed, d.Recent
	s.guidance = d.Guidance
	s.script.reset(s.text)
	s.savedDraft = s.toDraft()
}

// applyStudioDraft restores a draft; one on a removed model moves to its
// replacement (the clone one when the draft has a reference clip), with
// that model's recommended voice, language and settings, and says so.
func (a *app) applyStudioDraft(d studioDraft) {
	by, r, removed := replaceRemoved(d.ModelLabel, d.RefAudio != "")
	if removed {
		d.ModelLabel = by.Label
	}
	a.applyDraft(d)
	if removed {
		a.loadPreset(by, a.prefs.Studio.Overrides[by.ID])
		a.savedDraft = studioDraft{} // saved again on the next autosave
		a.noteRemoved(r, by)
	}
}

// autosaveStudio hands the form to the draft service when it changed
// since the last call. The service only remembers it here; it writes
// after a pause in editing, on its own goroutine.
func (a *app) autosaveStudio() {
	a.syncOverrides()
	if d := a.toDraft(); d != a.savedDraft {
		a.savedDraft = d
		a.lib.svc.Drafts.Save(studioDraftKey, d)
	}
}

// restoreStudioDraft loads the saved form in the background and applies
// it unless the form was edited meanwhile.
func (a *app) restoreStudioDraft() {
	drafts := a.lib.svc.Drafts
	a.background(func() {
		var d studioDraft
		ok, err := drafts.Load(context.Background(), studioDraftKey, &d)
		if err != nil || !ok {
			a.post(func() { a.startAutoModel(false) })
			return
		}
		a.post(func() {
			if a.toDraft() == a.savedDraft {
				a.applyStudioDraft(d)
			}
			a.startAutoModel(true)
		})
	})
}

// preset is the catalogue entry the form has chosen.
func (a *app) preset() catalog.Entry {
	if e, ok := catalog.Builtin().ByLabel(a.modelLabel); ok {
		return e
	}
	return catalog.Custom(strings.TrimSpace(a.customRepo))
}

// traits are the render traits of the form's model.
func (a *app) traits() services.Traits { return modelInfo(a.preset().Repo).Traits }

// selectModel makes e the Studio's model, as its Model select does (the
// ⌘E switcher and the palette call it). The settings remember it as the
// default of the next fresh start.
func (a *app) selectModel(e catalog.Entry) {
	if a.modelLabel == e.Label {
		return
	}
	a.modelLabel = e.Label
	a.modelChanged()
}

// modelChanged resets the voice and language to the new model's defaults
// and remembers the model in the settings.
func (a *app) modelChanged() {
	p := a.preset()
	a.needDownload = nil
	if p.ID != "" && p.ID != "custom" && p.ID != a.prefs.Studio.Model {
		id := p.ID
		a.updateSettings(func(s *settings.Settings) { s.Studio.Model = id })
	}
	a.studioAuto = false
	a.loadPreset(p, a.prefs.Studio.Overrides[p.ID])
}

func (a *app) canGenerate() bool {
	return a.engState == engineReady && a.job == nil &&
		strings.TrimSpace(a.text) != "" && a.preset().Repo != "" && !a.studioMissingReference()
}

// studioMissingReference is true when the Studio's model only clones
// voices (catalog NeedsReference) and nothing gives it a reference clip:
// no clip chosen, no unsaved clip, no saved voice that carries one. It
// asks the voice the recipe would send, as the services do.
func (a *app) studioMissingReference() bool {
	p := a.preset()
	return takeVoice(a.studioVoiceTake(p), p) == voiceMissing
}

// studioDefaultVoice is true when the Studio's model clones voices but
// the recipe would give it nothing to speak with — no reference clip, no
// saved voice, no speaker or style — so it speaks in its default voice.
func (a *app) studioDefaultVoice() bool {
	p := a.preset()
	return takeVoice(a.studioVoiceTake(p), p) == voiceDefault
}

// voiceOutcome is what a model does with the voice fields of a recipe.
type voiceOutcome int

const (
	voiceGiven   voiceOutcome = iota // a voice to clone, a speaker, a style, or the model's own voices
	voiceMissing                     // the model only clones and has nothing to clone: refused
	voiceDefault                     // the model clones but has nothing to clone: its default voice
)

// takeVoice is what model e does with the voice of recipe t: the one
// predicate behind the Studio's hints and Generate, on the same fields
// the services check (services.HasReference, checkReference).
func takeVoice(t core.Take, e catalog.Entry) voiceOutcome {
	switch {
	case services.HasReference(t):
		return voiceGiven
	case e.NeedsReference:
		return voiceMissing
	case e.Has(catalog.CapClone) && len(e.Voices) == 0 && !e.Has(catalog.CapVoiceStyle) && strings.TrimSpace(t.Voice) == "":
		return voiceDefault
	}
	return voiceGiven
}

// studioVoiceTake is the voice part of the recipe the Studio would make
// for model e (recipe without the rest): what the hints look at.
func (a *app) studioVoiceTake(e catalog.Entry) core.Take {
	t := core.Take{Model: e.Repo, Voice: strings.TrimSpace(a.voice), Language: strings.TrimSpace(a.language)}
	a.setStudioVoice(&t, e)
	return t
}

// setStudioVoice sets the voice of recipe t for model e: the inspector's
// reference clip, unless the picker supplies the voice; then the
// picker's voice (applyStudioVoice) and its encoded prompt.
func (a *app) setStudioVoice(t *core.Take, e catalog.Entry) {
	if e.Has(catalog.CapClone) && strings.TrimSpace(a.refAudio) != "" && !a.studioUsesSavedVoice(e) {
		t.RefAudio, t.RefText = strings.TrimSpace(a.refAudio), strings.TrimSpace(a.refText)
	}
	// A voice from the library (voices_studio.go): its reference, design, seed,
	// style, and the encoded prompt the engine prefers to the reference.
	a.applyStudioVoice(t, e)
	t.VoicePrompt = a.studioVoicePrompt(e)
}

// generate makes a take of the form: TakeService checks the licence and
// that the model is downloaded, and queues the job (startTake).
func (a *app) generate() {
	if !a.canGenerate() {
		return
	}
	// A designed voice cloned for the first time gets its reference
	// first (#83, voice_reference.go); a reference made from the voice is
	// heard once, and made again when it doesn't say its text (#93).
	if v := a.findVoice(a.studioVoice); v != nil && clonesDesign(a.preset()) && a.studioUsesSavedVoice(a.preset()) {
		if a.refMaking(v.ID) != nil || a.refChecking(v.ID) {
			return // the take follows
		}
		if a.refWork(v) {
			if making, _ := a.ensureReferences([]*core.Voice{v}, a.generate); making > 0 {
				a.toast(i18n.T("Making a reference for “%s” first: the take follows.", v.Name))
			}
			return
		}
	}
	a.startRender()
	a.startTake(a.recipe(time.Now()))
}

// recipe is the take the form describes, with a new id: only what the
// model takes is set, so its defaults apply to the rest.
func (a *app) recipe(now time.Time) core.Take {
	p := a.preset()
	tr := a.traits()
	t := core.Take{
		ID:       core.NewID(now),
		Text:     strings.TrimSpace(a.text),
		Model:    p.Repo,
		Voice:    strings.TrimSpace(a.voice),
		Language: strings.TrimSpace(a.language),
		Render: &core.TakeRender{
			Bits:        studioBits, // the bit depth is chosen at export
			NoNormalize: !a.normalize,
		},
	}
	if a.evenOut {
		t.Render.Master = services.MasterDefault
	}
	if a.seedPinned && a.seed > 0 {
		t.Seed, t.Render.SeedPinned = int64(a.seed), true
	}
	if p.Has(catalog.CapSpeed) {
		t.Speed = a.speed
	}
	if p.Has(catalog.CapSampling) {
		// 0: the services give the model's recommended values.
		t.Temperature, t.TopP = a.temperature, a.topP
	}
	a.setStudioVoice(&t, p)
	if p.Has(catalog.CapDuration) && a.duration > 0 {
		t.Duration = a.duration
	}
	if tr.Steps && a.steps > 0 {
		t.Steps = int(a.steps)
	}
	if p.Has(catalog.CapGuidance) && a.guidance > 0 {
		t.Guidance = a.guidance
	}
	return t
}

// reuse copies a take's whole recipe back into the form: text, model,
// voice, language, parameters, seed (pinned) and output options.
func (a *app) reuse(t *core.Take) {
	a.text = t.Text
	a.script.set(t.Text)
	if p, ok := catalog.Builtin().ByRepo(t.Model); ok {
		a.modelLabel = p.Label
	} else if p, r, ok := replaceRemoved(t.Model, services.HasReference(*t)); ok {
		a.modelLabel = p.Label
		a.noteRemoved(r, p)
	} else {
		a.modelLabel, a.customRepo = catalog.CustomLabel, t.Model
	}
	a.voice, a.language = t.Voice, t.Language
	if m := *t; replaceRemovedTake(&m) {
		a.voice, a.language = m.Voice, m.Language // what the replacement takes
	}
	if t.Speed > 0 {
		a.speed = t.Speed
	}
	a.studioAuto = false
	a.temperature, a.topP = t.Temperature, t.TopP // as recommended: no change (studioOverride)
	a.refAudio, a.refText = t.RefAudio, t.RefText
	// The library voice it was spoken with, if it still exists.
	a.studioVoice, a.studioClip = "", nil
	if v := a.findVoice(t.VoiceID); v != nil {
		a.studioVoice = v.ID
		a.refAudio, a.refText = "", ""
	}
	a.duration, a.steps = t.Duration, float64(t.Steps)
	a.guidance = t.Guidance
	a.seedPinned, a.seed = t.Seed != 0, float64(t.Seed)
	if r := t.Render; r != nil {
		a.evenOut = r.Master != "" && r.Master != "raw"
		a.normalize = !r.NoNormalize
	} else {
		a.evenOut, a.normalize = false, true
	}
	a.showPage("studio")
}

// newSeed pins a new random seed.
func (a *app) newSeed() {
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<31-2))
	a.seed, a.seedPinned = float64(n.Int64()+1), true
}

func (a *app) chooseRefAudio() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Reference audio"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Audio"), Extensions: []string{"wav", "mp3", "m4a", "aac", "flac", "aiff", "caf"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			a.refAudio, a.refText = paths[0], ""
			if strings.EqualFold(filepath.Ext(paths[0]), ".wav") {
				a.transcribeRef(paths[0])
			}
		})
	}()
}

// snapped rounds a slider's value to a step, so labels and requests show
// 1.05 rather than 1.0473; the slider moves smoothly in between.
func snapped(e ui.Element, v *float64, step float64) ui.Element {
	if e.Changed() {
		*v = float64(int(*v/step+0.5)) * step
	}
	return e
}
