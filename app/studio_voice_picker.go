package main

// The Studio's voice list in the shared voice picker (voice_picker.go,
// thread #60): the model's speakers, the saved voices it can speak
// with, then Clone a clip…, a portable copy of the speaker and Manage
// voices….

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/gallery"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// studioVoicePicker is the settings' voice row and its picker for model
// e. Opened by 「✦ 推荐」, recommended voices head it
// (studio_recommend.go); opened by 「换个音色」, a voice picked
// generates the take again at once.
func (a *app) studioVoicePicker(c *ui.Context, e catalog.Entry) {
	cur, opts, acts := a.studioVoiceChoices(e)
	st := &a.ttsUI
	if st.recommend {
		recs := a.recommendVoices(e, opts)
		if st.regen { // another voice: not the one it has
			recs = slices.DeleteFunc(recs, func(r voiceOption) bool { return r.value == "rec:"+cur })
		}
		for _, r := range recs {
			if orig, ok := strings.CutPrefix(r.value, "rec:"); ok {
				acts[r.value] = acts[orig]
			} else if id, ok := strings.CutPrefix(r.value, "rec-model:"); ok {
				acts[r.value] = func() {
					if m, ok := catalog.Builtin().ByID(id); ok {
						a.selectModel(m)
					}
				}
			}
		}
		if len(recs) == 0 {
			recs = []voiceOption{{value: "rec-none", label: i18n.T("No voice to recommend"), group: i18n.T("Recommended for this script"), warn: true}}
		}
		opts = append(recs, opts...)
	}
	o := voicePickerOpts{label: i18n.T("Voice"), text: a.studioVoiceLabel(e), voice: a.findVoice(a.studioVoice), width: 300,
		row: true, sub: a.studioVoiceAbout(e)}
	if st.regen {
		o.title = i18n.T("Pick another voice: the take is made again with it.")
	}
	v, ok := a.voicePicker(c, studioVoiceKey, cur, opts, o)
	if ok {
		if act := acts[v]; act != nil {
			act()
			if st.regen && !strings.HasPrefix(v, "rec-model:") && !isActionValue(opts, v) {
				st.regen = false
				a.generate()
			}
		}
	}
	if !a.voicePickers.open[studioVoiceKey] {
		st.recommend, st.regen = false, false
	}
}

// isActionValue reports whether value is an action row of opts.
func isActionValue(opts []voiceOption, value string) bool {
	for _, op := range opts {
		if op.value == value {
			return op.action
		}
	}
	return false
}

// studioVoiceAbout is the voice row's second line: a saved voice's kind,
// a curated speaker's description, else its gender and language.
func (a *app) studioVoiceAbout(e catalog.Entry) string {
	if v := a.findVoice(a.studioVoice); v != nil {
		if id, ok := strings.CutPrefix(v.Origin, "gallery:"); ok {
			if ar, ok := gallery.Default().ByID(id); ok && ar.Description.In(i18n.Locale()) != "" {
				return ar.Description.In(i18n.Locale())
			}
		}
		return voiceKindLabel(v)
	}
	sp, _ := splitVoice(a.voice, e.AllVoiceIDs())
	if sp == "" {
		return ""
	}
	if ar, ok := speakerArchetype(e, sp); ok {
		return ar.Description.In(i18n.Locale())
	}
	var parts []string
	for _, vo := range e.Voices {
		if vo.ID != sp {
			continue
		}
		switch vo.Gender {
		case "female":
			parts = append(parts, i18n.T("Female voice"))
		case "male":
			parts = append(parts, i18n.T("Male voice"))
		}
		if l := e.SpeakerLanguage(sp); l != "" {
			parts = append(parts, langPlain(l))
		}
	}
	return strings.Join(parts, " · ")
}

// studioVoiceChoices are the Studio's voice rows for model e, the chosen
// one's value and what picking each does.
func (a *app) studioVoiceChoices(e catalog.Entry) (cur string, opts []voiceOption, acts map[string]func()) {
	speaker, style := splitVoice(a.voice, e.AllVoiceIDs())
	saved := a.findVoice(a.studioVoice)
	clip := a.studioClip != nil && a.studioVoice == "" && e.Has(catalog.CapClone)
	ref := !clip && saved == nil && e.Has(catalog.CapClone) && strings.TrimSpace(a.refAudio) != ""
	acts = map[string]func(){}
	clearOthers := func() { a.studioVoice, a.studioClip, a.refAudio, a.refText = "", nil, "", "" }
	add := func(op voiceOption, act func()) {
		opts = append(opts, op)
		acts[op.value] = act
	}
	switch {
	case saved != nil:
		cur = "v:" + saved.ID
	case clip:
		cur = "clip"
	case ref:
	case speaker != "":
		cur = "sp:" + speaker
	default:
		cur = "default"
	}
	if !e.NeedsReference {
		group := i18n.T("Model voices")
		ids := e.VoiceIDsIn(i18n.Locale())
		if speaker != "" && !slices.Contains(ids, speaker) {
			ids = append(ids, speaker) // a hidden speaker chosen before stays shown
		}
		if len(ids) == 0 {
			add(voiceOption{value: "default", label: i18n.T("Model default"), group: group}, clearOthers)
		}
		rec := ""
		if p, ok := catalog.PresetFor(e.ID); ok {
			rec = p.VoiceFor(i18n.Locale())
		}
		for _, id := range ids {
			sub := ""
			if id == rec {
				sub = i18n.T("Recommended")
			}
			add(voiceOption{value: "sp:" + id, label: id, sub: sub, group: group, preview: speakerPreview(e, id)}, func() {
				clearOthers()
				a.voice = joinVoice(id, style)
			})
		}
	}
	mine := i18n.T("My voices")
	if clip {
		add(voiceOption{value: "clip", label: i18n.T("Unsaved clip: %s", a.studioClip.Name), group: mine}, func() {})
	}
	for i := range a.voices {
		v := &a.voices[i]
		if ok, _ := voiceUsable(v, e); ok {
			add(voiceOption{value: "v:" + v.ID, label: v.Name, sub: voiceKindLabel(v), group: mine, voice: v, preview: a.savedVoicePreview(v)}, func() {
				a.studioVoice, a.studioClip = v.ID, nil
			})
		}
	}
	if e.Has(catalog.CapClone) {
		label := i18n.T("Clone a clip…")
		if ref {
			label = i18n.T("Clip: %s", filepath.Base(a.refAudio))
		}
		add(voiceOption{value: "ref", label: label, sub: i18n.T("A few seconds of clean speech; not saved to your voices"), action: true, on: ref}, func() {
			a.studioVoice, a.studioClip = "", nil
			a.chooseRefAudio()
		})
	}
	if speaker != "" && saved == nil && !clip && !ref && !e.Has(catalog.CapClone) {
		add(voiceOption{value: "portable", label: i18n.T("Make a portable copy of %s…", speaker), sub: i18n.T("An approximate clone of it, for models that clone"), action: true}, func() {
			a.makeSpeakerPortable(e, speaker)
		})
	}
	add(voiceOption{value: "browse", label: i18n.T("Browse the voice library ›"), action: true}, func() { a.navigate("/voices/discover") })
	add(voiceOption{value: "manage", label: i18n.T("Manage voices…"), action: true}, func() { a.navigate("/voices/mine") })
	return cur, opts, acts
}

// studioVoiceKey is the Studio's voice picker's key (tests open it).
const studioVoiceKey = "studio-voice"
