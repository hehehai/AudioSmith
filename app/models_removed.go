package main

// Models AudioSmith removed (thread #43: KittenTTS, MOSS-TTS-Nano,
// Chatterbox multilingual, Marvis, Soprano, Pocket TTS; thread #46:
// Parakeet, Mel-RoFormer, Gemma 4 E4B and E2B, Qwen3 1.7B). The catalogue
// keeps an alias table (catalog.Removed): whatever still names one of
// them — the Studio's draft, a story or audiobook, a dub, a workflow, the
// settings, a history recipe, an API or MCP call — uses its replacement,
// and the app says so once per model while it runs ("X was removed; Y is
// used instead"). Renders map a recipe again as they start
// (replaceRemovedTake, services.Config.Replace), so nothing reaches the
// engine with a removed model.
//
// Their files stay in the model folder until the user cleans them up on
// the Models page (removedRow): AudioSmith never deletes them by itself.

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// removedState is the app's state about removed models.
type removedState struct {
	removedNoted map[string]bool // removed model ids already announced this run
	// removedLeft is what the removed models leave on disk (refreshModels);
	// removedKnown says it was read.
	removedLeft  []hub.Leftover
	removedKnown bool
	cleanAsk     bool
	cleaning     bool
}

// replaceRemoved maps ref (a repo, id or label) when it names a removed
// speech model: the entry to use instead (clone: the work clones voices).
func replaceRemoved(ref string, clone bool) (catalog.Entry, catalog.Removed, bool) {
	return catalog.Builtin().Replace(ref, clone)
}

// replaceRemovedAny maps ref when it names a removed model of any task:
// speech models as replaceRemoved (without cloning), speech-to-text ones
// to the default for detected speech, the others to their replacement.
func replaceRemovedAny(ref string) (catalog.Entry, catalog.Removed, bool) {
	c := catalog.Builtin()
	r, ok := c.RemovedModel(ref)
	if !ok {
		return catalog.Entry{}, catalog.Removed{}, false
	}
	if r.Did() == catalog.TaskTTS {
		return c.Replace(ref, false)
	}
	return c.ReplaceTask(ref, r.Did(), "")
}

// noteRemoved tells the user, once per model while the app runs, that r
// was removed and by replaces it.
func (a *app) noteRemoved(r catalog.Removed, by catalog.Entry) {
	if a.removedNoted == nil {
		a.removedNoted = map[string]bool{}
	}
	if a.removedNoted[r.ID] {
		return
	}
	a.removedNoted[r.ID] = true
	a.toast(i18n.T("%s was removed; %s is used instead.", r.Label, modelRef(by)))
	if a.logs != nil {
		a.logs.Append(core.LogEntry{Source: "models", Message: r.ID + " was removed; using " + by.ID})
	}
}

// replaceRemovedRepo replaces *repo when it names a removed model, tells
// the user, and reports whether it did.
func (a *app) replaceRemovedRepo(repo *string, clone bool) bool {
	by, r, ok := replaceRemoved(*repo, clone)
	if !ok {
		return false
	}
	*repo = by.Repo
	a.noteRemoved(r, by)
	return true
}

// clonesWith reports whether one of refs (voice references) is a saved
// voice that is not a built-in speaker: work that speaks with it clones.
func (a *app) clonesWith(refs ...string) bool {
	for _, ref := range refs {
		if v := a.findVoice(ref); v != nil && v.Kind != core.VoiceBuiltin {
			return true
		}
	}
	return false
}

// removedVoice reports whether v is a built-in speaker of a removed model
// (a KittenTTS or Pocket TTS voice saved in My voices): no model has it.
func removedVoice(v *core.Voice) bool {
	_, ok := catalog.Builtin().RemovedModel(v.Model)
	return ok && v.Kind == core.VoiceBuiltin
}

// keepSpeaker clears *ref when it names a built-in speaker model e lacks
// (one of the removed model's own): the default voice speaks instead.
func keepSpeaker(ref *string, e catalog.Entry) {
	if id, ok := strings.CutPrefix(*ref, services.SpeakerRef); ok && !slices.Contains(e.AllVoiceIDs(), id) {
		*ref = ""
	}
}

// replaceRemovedDoc moves a story or audiobook off a removed model: to
// the clone replacement when it speaks with saved voices, and with the
// old model's own speakers back to the default voice.
func (a *app) replaceRemovedDoc(d *services.LongformDoc) bool {
	if _, ok := catalog.Builtin().RemovedModel(d.Model); !ok {
		return false
	}
	refs := []string{d.Voice}
	for _, m := range d.Cast {
		refs = append(refs, m.Voice)
	}
	for _, l := range d.Lines {
		refs = append(refs, l.Voice)
	}
	for _, v := range d.VoiceCast {
		refs = append(refs, v)
	}
	if !a.replaceRemovedRepo(&d.Model, a.clonesWith(refs...)) {
		return false
	}
	e := entryFor(d.Model)
	keepSpeaker(&d.Voice, e)
	for i := range d.Cast {
		keepSpeaker(&d.Cast[i].Voice, e)
	}
	for i := range d.Lines {
		keepSpeaker(&d.Lines[i].Voice, e)
	}
	for k, v := range d.VoiceCast {
		keepSpeaker(&v, e)
		d.VoiceCast[k] = v
	}
	return true
}

// replaceRemovedWorkflow moves a workflow's steps off removed models
// (Speak and Convert name a TTS model), as replaceRemovedDoc does.
func (a *app) replaceRemovedWorkflow(w *workflow.Workflow) bool {
	changed := false
	for i := range w.Nodes {
		p := &w.Nodes[i].P
		if p.Model == "" || !a.replaceRemovedRepo(&p.Model, a.clonesWith(p.Voice)) {
			continue
		}
		keepSpeaker(&p.Voice, entryFor(p.Model))
		changed = true
	}
	return changed
}

// replaceRemovedTake is services.Config.Replace: a recipe that names a
// removed model gets the replacement (the clone one when it has a
// reference), and loses what the replacement can't take: a built-in
// speaker it lacks (its default speaks), a language it does not take,
// guidance.
func replaceRemovedTake(t *core.Take) bool {
	by, _, ok := replaceRemoved(t.Model, services.HasReference(*t))
	if !ok {
		return false
	}
	t.Model = by.Repo
	switch {
	case len(by.Voices) > 0:
		if !slices.Contains(by.AllVoiceIDs(), t.Voice) {
			t.Voice = by.VoiceIDs()[0]
			if p, ok := catalog.PresetFor(by.ID); ok {
				if v := p.VoiceFor(t.Language); v != "" {
					t.Voice = v
				}
			}
		}
	case !by.Has(catalog.CapVoiceStyle) && !by.Has(catalog.CapDesign):
		t.Voice = ""
	}
	t.Language = replacementLanguage(by, t.Language)
	if !by.Has(catalog.CapGuidance) {
		t.Guidance = 0
	}
	if !by.Has(catalog.CapSteps) {
		t.Steps = 0
	}
	if !by.Has(catalog.CapSampling) {
		t.Temperature, t.TopP = 0, 0
	}
	return true
}

// replacementLanguage is language (a code or a model's value) as model e
// takes it: its value for that language, "" when e takes none or not
// that one.
func replacementLanguage(e catalog.Entry, language string) string {
	language = strings.TrimSpace(language)
	if language == "" || !e.Has(catalog.CapLanguage) {
		return ""
	}
	base := catalog.BaseLanguage(language)
	for _, l := range e.Languages {
		if strings.EqualFold(l.Value, language) || strings.EqualFold(l.Code, language) || catalog.BaseLanguage(l.Code) == base {
			if l.Value != "" {
				return l.Value
			}
			return l.Code
		}
	}
	return ""
}

// replaceRemovedSettings moves the settings off removed models: the
// Studio's default, the dubbing default, the AI calls' model and the
// agents' bindings; the Studio's changes for a removed model go. Speech
// recognition (dictation, Transcribe's modes, calls) and the local
// language model go back to their defaults (""), which follow the
// language (Qwen3-ASR, or Whisper; thread #46); routes of writing help
// that named a removed model use the provider's default. It returns the
// models replaced, each with the first model that replaced it.
func replaceRemovedSettings(s *settings.Settings) []removedPair {
	var out []removedPair
	note := func(r catalog.Removed, by catalog.Entry) {
		if !slices.ContainsFunc(out, func(x removedPair) bool { return x.r.ID == r.ID }) {
			out = append(out, removedPair{r, by})
		}
	}
	id := func(ref *string, clone bool) {
		if by, r, ok := replaceRemoved(*ref, clone); ok {
			*ref = by.ID
			note(r, by)
		}
	}
	id(&s.Studio.Model, false)
	id(&s.Calls.Model, true) // the agent speaks with a saved (cloned or designed) voice
	for i := range s.API.Bindings {
		id(&s.API.Bindings[i].Model, s.API.Bindings[i].Voice != "")
	}
	if by, r, ok := replaceRemoved(s.Dubbing.Model, true); ok {
		s.Dubbing.Model = by.Repo
		note(r, by)
	}
	for k := range s.Studio.Overrides {
		if by, r, ok := replaceRemoved(k, false); ok {
			delete(s.Studio.Overrides, k)
			note(r, by)
		}
	}
	c := catalog.Builtin()
	reset := func(ref *string, task catalog.Task, language string) {
		if by, r, ok := c.ReplaceTask(*ref, task, language); ok {
			*ref = ""
			note(r, by)
		}
	}
	reset(&s.Dictation.Model, catalog.TaskSTT, s.Dictation.Language)
	reset(&s.Transcribe.FastModel, catalog.TaskSTT, s.Transcribe.Language)
	reset(&s.Transcribe.AccurateModel, catalog.TaskSTT, s.Transcribe.Language)
	reset(&s.Calls.STTModel, catalog.TaskSTT, "")
	reset(&s.LLM.LocalModel, catalog.TaskLLM, "")
	routes := func(rs []settings.LLMRoute) []settings.LLMRoute {
		if !slices.ContainsFunc(rs, func(r settings.LLMRoute) bool {
			_, ok := c.RemovedModel(r.Model)
			return ok && r.Provider == localProvider
		}) {
			return rs
		}
		rs = slices.Clone(rs)
		for i := range rs {
			if rs[i].Provider == localProvider {
				reset(&rs[i].Model, catalog.TaskLLM, "")
			}
		}
		return rs
	}
	s.LLM.Default = routes(s.LLM.Default)
	for k, v := range s.LLM.Skills {
		s.LLM.Skills[k] = routes(v)
	}
	return out
}

// removedPair is a removed model and what replaced it.
type removedPair struct {
	r  catalog.Removed
	by catalog.Entry
}

// migrateRemovedSettings runs replaceRemovedSettings on the app's
// settings at start, saving and announcing what changed.
func (a *app) migrateRemovedSettings() {
	probe := a.prefs
	probe.Studio.Overrides = maps.Clone(probe.Studio.Overrides)
	probe.API.Bindings = slices.Clone(probe.API.Bindings)
	probe.LLM.Skills = maps.Clone(probe.LLM.Skills)
	rs := replaceRemovedSettings(&probe)
	if len(rs) == 0 {
		return
	}
	a.updateSettings(func(s *settings.Settings) { replaceRemovedSettings(s) })
	if len(rs) == 1 {
		a.noteRemoved(rs[0].r, rs[0].by)
		return
	}
	// Several at once: one notice, not a stack of them.
	var pairs []string
	for _, p := range rs {
		pairs = append(pairs, p.r.Label+" → "+p.by.DisplayName())
		if a.removedNoted == nil {
			a.removedNoted = map[string]bool{}
		}
		a.removedNoted[p.r.ID] = true
	}
	a.toast(i18n.T("Removed models: %s. Your settings now use the replacements.", strings.Join(pairs, ", ")))
}

// --- the Models page's clean-up row -------------------------------------------

// removedRepos are the repos the removed models may have left on disk.
func removedRepos() []string {
	var out []string
	for _, r := range catalog.Builtin().Removed {
		out = append(out, r.Repos()...)
	}
	return out
}

// refreshRemoved measures what the removed models leave on disk, and
// which of it other ActNow apps may use (Clean up keeps that), in the
// background.
func (a *app) refreshRemoved() {
	opts := a.hubClient().Options()
	a.async(func() {
		ls, _ := hub.Leftovers(opts.CacheDir, removedRepos(), opts.Family)
		a.post(func() { a.removedLeft, a.removedKnown = ls, true })
	})
}

// removedBytes is the space Clean up would free.
func (a *app) removedBytes() int64 {
	var n int64
	for _, l := range a.removedLeft {
		if !l.Kept() {
			n += l.Bytes
		}
	}
	return n
}

// removedKind is the Models page's tab (modelKinds) of a removed model.
func removedKind(r catalog.Removed) int {
	return modelKindOf(catalog.Entry{Tasks: []catalog.Task{r.Did()}})
}

// removedShows reports whether the clean-up row shows on tab kind: on
// All, and on the tab of a removed model with files to clean up.
func (a *app) removedShows(kind int) bool {
	if kind == 0 {
		return true
	}
	for _, r := range catalog.Builtin().Removed {
		if removedKind(r) == kind && slices.ContainsFunc(a.removedLeft, func(l hub.Leftover) bool { return !l.Kept() && slices.Contains(r.Repos(), l.Repo) }) {
			return true
		}
	}
	return false
}

// removedOnDisk are the removed models with files to clean up, and the
// bytes of each (its auxiliary repos count as its own); kept are those
// with files another ActNow app uses, with why ("Kept: shared across ActNow"),
// when the app can be named.
func (a *app) removedOnDisk() (names []string, bytes []int64, kept []string) {
	for _, r := range catalog.Builtin().Removed {
		var n int64
		found := false
		var users []string
		for _, l := range a.removedLeft {
			if !slices.Contains(r.Repos(), l.Repo) {
				continue
			}
			if l.Kept() {
				for _, u := range a.familyNames(l.Users) { // named only as spec §7.10 allows
					if !slices.Contains(users, u) {
						users = append(users, u)
					}
				}
				continue
			}
			n += l.Bytes
			found = true
		}
		if found {
			names, bytes = append(names, r.Label), append(bytes, n)
		}
		if len(users) > 0 {
			kept = append(kept, r.Label+" · "+i18n.T("Kept: shared across ActNow"))
		}
	}
	return names, bytes, kept
}

// listJoin joins names as the interface's language lists them.
func listJoin(names []string) string {
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return strings.Join(names, "、")
	}
	return strings.Join(names, ", ")
}

// removedRow is the Models page's row about removed models still on
// disk, with Clean up (after a confirmation that lists them).
func (a *app) removedRow(c *ui.Context) {
	n := a.removedBytes()
	if n <= 0 {
		return
	}
	t := c.Theme()
	k := tok(t)
	card(c.Key("models-removed")).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			iconMark(c, icons.TrashBinMinimalistic, k.Text2, "")
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				ui.Text(c, i18n.T("Removed models still take %s", mb(n))).FontWeight(600)
				names, _, _ := a.removedOnDisk()
				caption(c, i18n.T("%s: AudioSmith no longer uses them.", listJoin(names))).TextColor(k.Text2).MaxLines(2)
			})
			if secondaryIconButton(c.Key("models-clean"), icons.TrashBinMinimalistic, i18n.T("Clean up")).Label(i18n.T("Clean up")).
				Disabled(a.cleaning).Clicked() {
				a.cleanAsk = true
			}
		})
	})
}

// removedAlert confirms the clean-up.
func (a *app) removedAlert(c *ui.Context) {
	var lines []string
	names, bytes, kept := a.removedOnDisk()
	for i, n := range names {
		lines = append(lines, "• "+n+" · "+mb(bytes[i]))
	}
	for _, k := range kept {
		lines = append(lines, "• "+k)
	}
	body := i18n.T("Their folders leave the model folder; your other models and files stay:") + "\n" + strings.Join(lines, "\n")
	if ui.AlertDialog(c, &a.cleanAsk, i18n.T("Clean up removed models (%s)?", mb(a.removedBytes())), body, i18n.T("Cancel"), i18n.T("Clean up")) == 1 {
		a.cleanRemoved()
	}
}

// cleanRemoved deletes what the removed models left on disk, except what
// other ActNow apps may use or are downloading.
func (a *app) cleanRemoved() {
	if a.cleaning {
		return
	}
	a.cleaning = true
	opts := a.hubClient().Options()
	repos := removedRepos()
	a.async(func() {
		freed, kept, err := hub.RemoveLeftovers(context.Background(), opts.CacheDir, repos, opts.Family)
		ls, _ := hub.Leftovers(opts.CacheDir, repos, opts.Family)
		a.post(func() {
			a.cleaning, a.removedLeft, a.removedKnown = false, ls, true
			if err != nil {
				a.toastWarn(i18n.T("Could not clean up: %v", err))
				return
			}
			msg := "cleaned up removed models: " + mb(freed)
			for _, l := range kept {
				msg += "; kept " + l.Repo + " (" + keptReason(l) + ")"
			}
			a.logs.Append(core.LogEntry{Source: "hub", Message: msg})
			a.toast(i18n.T("Freed %s", mb(freed)))
			for _, l := range kept {
				if l.Busy {
					a.toastWarn(busyText(l.BusyBy))
					break
				}
			}
		})
	})
}
