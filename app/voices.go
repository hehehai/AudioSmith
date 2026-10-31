package main

// The voice library as the UI sees it: a snapshot of the saved voices,
// kept current by VoiceChanged events, their portraits, the pages
// /voices (library + profile editor), /voices/new (a voice from a clip),
// /design and /gallery, the takes those pages generate (previews), and
// the Studio's seam: voicePicker and applyStudioVoice.
//
// Files of the area: voices.go (this: state, loading, the router),
// voices_takes.go (previews and playing), voices_studio.go (models, the
// Studio's seam), voices_view.go (library page), voice_editor.go
// (profile editor), voice_engine.go (what it needs of the
// engine), voice_portrait.go, voice_persona.go (.asvoice import/export),
// clone*.go (reference flow), design*.go, gallery*.go.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// voicesState is the voice area's view state.
type voicesState struct {
	voices       []core.Voice // the library, by name
	voicesLoaded bool
	// portraits are decoded portraits by voice id; portraitFor the
	// portrait file + update time each was decoded from.
	portraits   map[string]*ui.Bitmap
	portraitFor map[string]string

	// The library page.
	voiceQuery  string
	voiceKind   int // 0 all, 1 cloned, 2 designed
	voiceGrid   ui.GridState
	deleteVoice string // id the delete confirmation asks about
	deleteOpen  bool
	deleteUsage *voiceUsage     // where it is used, once known (voices_delete.go)
	undoing     map[string]bool // deleted voices whose Undo runs: their trash stays
	edit        *voiceEdit      // the profile editor; nil when closed

	clone   cloneState
	design  designState
	gallery galleryState
	persona personaState

	// The Studio's voice (applyStudioVoice): a saved voice, or a clip used
	// without saving it. Both "" / nil: the model's own voices.
	studioVoice string
	studioClip  *clipRef
	studioGone  string // the Studio's voice deleted last (Undo restores it)

	// voiceJobs are the takes the voice pages generate, by take id;
	// voiceWork the other engine jobs they wait for, by owner.
	voiceJobs map[string]*voiceJob
	voiceWork map[string]func(jobs.Job)
	// refMakes are the references being made from voices (a designed
	// voice's, a portable copy's), by key (voice_reference.go).
	refMakes map[string]*refMake
	// refChecks are the references being checked, by voice id;
	// refCheckSkip those not to check again this session
	// (voice_reference_repair.go).
	refChecks    map[string]*refCheck
	refCheckSkip map[string]bool
}

// clipRef is a reference clip used without saving it as a voice.
type clipRef struct {
	Path, Text, Name, Style string
}

// openVoices connects the voice pages to the library: it subscribes to
// voice, take and job events (once, here) and loads the first snapshot.
func (a *app) openVoices(lib *library) {
	a.portraits, a.portraitFor = map[string]*ui.Bitmap{}, map[string]string{}
	a.voiceJobs = map[string]*voiceJob{}
	a.voiceWork = map[string]func(jobs.Job){}
	a.voiceGrid.Key = func(i int) any {
		if l := a.shownVoices(); i < len(l) {
			return l[i].ID
		}
		return i
	}
	core.On(lib.bus, func(ev core.VoiceChanged) { a.post(func() { a.voiceChanged(ev) }) })
	core.On(lib.bus, func(ev core.TakeCreated) { a.post(func() { a.voiceTakeCreated(ev.Take) }) })
	core.On(lib.bus, func(u jobs.Updated) {
		switch {
		case u.Chunk != nil:
		case strings.HasPrefix(u.Job.Owner, takeOwner):
			a.post(func() { a.voiceJobUpdated(u.Job) })
		case strings.HasPrefix(u.Job.Owner, voiceWorkOwner) && u.Job.State.Terminal():
			a.post(func() { a.voiceWorkDone(u.Job) })
		}
	})
	a.openGallery(lib)
	a.design.reset()
	mygo.App.OnOpenFile(func(path string) { a.post(func() { a.openVoiceFile(path) }) })
	a.undoing = map[string]bool{}
	svc := lib.svc.Voices
	a.background(func() { svc.PurgeTrash(context.Background()) }) // voices_delete.go: an earlier run's deletes
	a.loadVoices()
}

// loadVoices reads the snapshot in the background.
func (a *app) loadVoices() {
	svc := a.lib.svc.Voices
	a.background(func() {
		vs, err := svc.List(context.Background(), core.VoiceQuery{})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not read the voices: %v", err))
				return
			}
			// Keep voices that events added while the list was read.
			for _, v := range a.voices {
				if !slices.ContainsFunc(vs, func(x core.Voice) bool { return x.ID == v.ID }) {
					vs = append(vs, v)
				}
			}
			sortVoices(vs)
			a.voices, a.voicesLoaded = vs, true
			a.autoStudioModel() // a new user with saved voices
			a.autoLfModels()
			a.importStarterVoices()
		})
	})
}

func sortVoices(vs []core.Voice) {
	slices.SortStableFunc(vs, func(x, y core.Voice) int {
		if c := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
}

// voiceChanged applies a VoiceChanged event to the snapshot.
func (a *app) voiceChanged(ev core.VoiceChanged) {
	i := slices.IndexFunc(a.voices, func(v core.Voice) bool { return v.ID == ev.Voice.ID })
	switch {
	case ev.Deleted:
		if i >= 0 {
			a.voices = slices.Delete(a.voices, i, i+1)
		}
		delete(a.portraits, ev.Voice.ID)
		delete(a.portraitFor, ev.Voice.ID)
		if a.edit != nil && a.edit.id == ev.Voice.ID {
			a.edit = nil
		}
		if a.studioVoice == ev.Voice.ID {
			// Never another voice in silence (#108): the Studio says so,
			// and Undo puts it back.
			a.studioVoice, a.studioGone = "", ev.Voice.ID
			a.toastWarn(i18n.T("“%s” was the Studio's voice: choose another one there.", ev.Voice.Name))
		}
		if a.design.linked == ev.Voice.ID {
			a.design.linked = ""
		}
		return
	case i >= 0:
		a.voices[i] = ev.Voice
	default:
		a.voices = append(a.voices, ev.Voice)
		if a.studioGone == ev.Voice.ID && a.studioVoice == "" && a.studioClip == nil { // its delete undone
			a.studioVoice = ev.Voice.ID
		}
	}
	sortVoices(a.voices)
	if a.edit != nil && a.edit.id == ev.Voice.ID {
		a.edit.refresh(ev.Voice)
	}
	a.autoStudioModel() // a new user's first saved voice
	a.autoLfModels()
}

// findVoice is the snapshot's voice with id, or nil.
func (a *app) findVoice(id string) *core.Voice {
	if id == "" {
		return nil
	}
	for i := range a.voices {
		if a.voices[i].ID == id {
			return &a.voices[i]
		}
	}
	return nil
}

// voicePath is the absolute path of a voice's file ("" for none).
func (a *app) voicePath(v *core.Voice, name string) string {
	return a.lib.svc.Voices.FilePath(*v, name)
}

// portrait is the decoded portrait of a voice, nil while there is none
// (or it is being read: requested here, at most once per version).
func (a *app) portrait(v *core.Voice) *ui.Bitmap {
	if v.Portrait == "" {
		return nil
	}
	version := v.Portrait + "@" + v.UpdatedAt.String()
	if a.portraitFor[v.ID] == version {
		return a.portraits[v.ID]
	}
	a.portraitFor[v.ID] = version
	id, path := v.ID, a.voicePath(v, v.Portrait)
	a.background(func() {
		data, err := os.ReadFile(path)
		var bmp *ui.Bitmap
		if err == nil {
			bmp, _ = ui.DecodeBitmap(data)
		}
		a.post(func() {
			if a.portraitFor[id] == version {
				a.portraits[id] = bmp
			}
		})
	})
	return a.portraits[v.ID]
}

// voiceKindText names a voice's kind.
func voiceKindText(k core.VoiceKind) string {
	switch k {
	case core.VoiceCloned:
		return i18n.T("Cloned voice")
	case core.VoiceDesigned:
		return i18n.T("Designed voice")
	}
	return i18n.T("Built-in voice")
}

// --- the area's pages in the router (shell_pages.go) ---------------------

var (
	voicesIcon  = icons.UserSpeakRounded
	designIcon  = icons.MagicStick3
	galleryIcon = icons.Widget
	cloneIcon   = icons.UserPlusRounded
)

// referencesDir holds clips used without saving them ("Use without
// saving") and the working copies of the reference flow.
func (a *app) referencesDir() string { return filepath.Join(a.lib.store.Dir(), "references") }

// voiceCacheDir is the cache of rendered gallery previews.
func (a *app) voiceCacheDir() string {
	return filepath.Join(filepath.Dir(a.lib.peaks.Dir), "voice-previews")
}
