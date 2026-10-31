package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/store"
)

// batchPage is /batch: the setup (videos, languages, voice) beside the
// queue, or above it when the window is narrow.
func (a *app) batchPage(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	b := &a.batch
	if len(b.jobs) > 0 {
		c.After(time.Second)
	}
	side := px(t, inspectorWidth+40)
	stacked := a.pageWidth(c)-side < px(t, 440)
	pageHeader(c, i18n.T("Batch dubbing"), func() {
		label := i18n.T("Dub page")
		var btn ui.Element
		if stacked {
			btn = iconButton(c.Key("batch-dub"), dubIcon, label)
		} else {
			btn = ghostIconButton(c.Key("batch-dub"), dubIcon, label).Label(label)
		}
		if btn.Clicked() {
			a.navigate("/dub")
		}
	})
	setup := func() {
		ui.Column(c).Padding(px(t, sp2), px(t, sp5), px(t, sp5)).Gap(px(t, sp3)).Children(func() {
			a.batchSetup(c)
			ui.Box(c).Height(1).Background(k.Line)
			a.batchWatchPanel(c)
		})
	}
	if stacked {
		ui.Scroll(c.Key("batch-stack")).Grow(1).MinHeight(0).Children(func() {
			setup()
			ui.Column(c).Padding(px(t, sp2), px(t, sp5), px(t, sp5)).Gap(px(t, sp2)).MinHeight(px(t, 240)).Children(func() {
				a.batchQueue(c, true)
			})
		}).ClipX()
	} else {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c.Key("batch-side")).Width(side).Shrink(0).Children(setup)
			ui.Box(c).Width(1).Background(k.Line)
			ui.Column(c).Grow(1).MinWidth(0).Padding(px(t, sp2), px(t, sp6), px(t, sp4)).Gap(px(t, sp3)).Children(func() {
				a.batchQueue(c, false)
			})
		})
	}
	a.batchConfirm(c)
}

// batchSetup: the videos to add and the batch settings.
func (a *app) batchSetup(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	b := &a.batch
	s := &b.settings
	sectionHeading(c, i18n.T("Videos"))
	zone := ui.Column(c.Key("batch-drop")).Padding(px(t, sp2)).Gap(px(t, 2)).Radius(px(t, radiusCard)).Border(1, k.G[5]).BorderStyle(ui.BorderDashed)
	if files := zone.DroppedFiles(); len(files) > 0 {
		a.batchDropped(files)
	}
	if zone.FileDragOver() {
		zone.Border(2, k.Accent).Background(k.AccentSoft)
	}
	zone.Children(func() {
		if len(b.files) == 0 {
			caption(c, i18n.T("Drop videos here to dub them in a batch.")).TextAlign(ui.Center).Padding(px(t, sp2))
		}
		for _, f := range b.files {
			ff := f
			ui.Row(c.Key("file-"+f)).Gap(px(t, 6)).AlignItems(ui.Center).Padding(0, 0, 0, px(t, sp2)).Children(func() {
				iconMark(c, dubIcon, k.Text2, "")
				truncText(c, filepath.Base(ff)).FontSize(px(t, fsCaption)).Grow(1)
				if iconButton(c.Key("x"), icons.Close, i18n.T("Remove %s", filepath.Base(ff))).Clicked() {
					b.files = slices.DeleteFunc(b.files, func(x string) bool { return x == ff })
				}
			})
		}
		ui.Row(c).Justify(ui.Center).Children(func() {
			if ghostIconButton(c.Key("batch-add"), icons.AddCircle, i18n.T("Add videos…")).Label(i18n.T("Add videos…")).Clicked() {
				a.batchAddFiles()
			}
		})
	})
	// Target languages.
	ui.Text(c, i18n.T("Dub into"))
	ui.Row(c.Key("batch-langs")).Gap(px(t, 4)).Wrap().AlignItems(ui.Center).Children(func() {
		for _, l := range s.Targets {
			ll := l
			ui.Row(c.Key("lang-"+l)).Gap(px(t, 2)).AlignItems(ui.Center).Padding(px(t, 2), px(t, 2), px(t, 2), px(t, sp2)).Radius(px(t, radiusControl)).Background(k.Hover).Children(func() {
				ui.Text(c, dubLangName(ll)).FontSize(px(t, fsCaption)).SingleLine()
				if iconButton(c.Key("x"), icons.Close, i18n.T("Remove %s", dubLangName(ll))).Size(px(t, 22), px(t, 22)).Clicked() {
					a.batchSetSettings(func(s *services.BatchSettings) {
						s.Targets, s.TargetsAuto = slices.DeleteFunc(s.Targets, func(x string) bool { return x == ll }), false
					})
				}
			})
		}
		dropButton(c.Key("batch-lang-add"), icons.AddCircle, i18n.T("Add")).Shrink(0).Menu(func(m *ui.Menu) {
			for _, l := range dubTargetLangs {
				if !slices.Contains(s.Targets, l) && m.Item(dubLangName(l)).Chosen() {
					ll := l
					a.batchSetSettings(func(s *services.BatchSettings) { s.Targets, s.TargetsAuto = append(s.Targets, ll), false })
				}
			}
		})
	})
	if s.TargetsAuto {
		caption(c, dubAutoTargetHint()).TextColor(k.Text3)
	}
	// Voice.
	choices := []dubVoiceChoice{{"", i18n.T("Auto · each speaker's own voice"), ""}, {dub.VoiceDefault, i18n.T("Default voice"), ""}}
	model := firstOf(s.Model, a.dubDefaultModel())
	e := entryFor(model)
	for i := range a.voices {
		v := &a.voices[i]
		if ok, _ := voiceUsable(v, e); ok {
			choices = append(choices, dubVoiceChoice{v.ID, v.Name, i18n.T("Saved voices")})
		}
	}
	for _, pr := range dubPresets() {
		choices = append(choices, dubVoiceChoice{dub.PresetPrefix + pr.ID, pr.Name.In(i18n.Locale()), i18n.T("Design presets")})
	}
	// A preset no longer offered (thread #94) that the settings still name.
	if id, ok := strings.CutPrefix(s.Voice, dub.PresetPrefix); ok && !slices.ContainsFunc(choices, func(ch dubVoiceChoice) bool { return ch.ref == s.Voice }) {
		if pr, ok := dubPreset(id); ok {
			choices = append(choices, dubVoiceChoice{s.Voice, pr.Name.In(i18n.Locale()), i18n.T("Design presets")})
		}
	}
	labels := make([]string, len(choices))
	refs := make([]string, len(choices))
	for i, ch := range choices {
		labels[i], refs[i] = ch.label, ch.ref
		if ch.group != "" {
			labels[i] = ch.group + " · " + ch.label
		}
	}
	cur := labels[max(slices.Index(refs, s.Voice), 0)]
	ui.Field(c, i18n.T("Voice"), func() {
		if ui.Select(c.Key("batch-voice"), &cur, labels).Label(i18n.T("Voice")).Changed() {
			ref := refs[slices.Index(labels, cur)]
			a.batchSetSettings(func(s *services.BatchSettings) { s.Voice = ref })
		}
	})
	models := dubTTSModels()
	mnames := []string{i18n.T("Automatic")}
	mrepos := []string{""}
	for _, e := range models {
		mnames = append(mnames, e.DisplayName())
		mrepos = append(mrepos, e.Repo)
	}
	mcur := mnames[max(slices.Index(mrepos, s.Model), 0)]
	ui.Field(c, i18n.T("Voice model"), func() {
		if ui.Select(c.Key("batch-model"), &mcur, mnames).Label(i18n.T("Voice model")).Changed() {
			repo := mrepos[slices.Index(mnames, mcur)]
			a.batchSetSettings(func(s *services.BatchSettings) { s.Model = repo })
		}
	})
	keep := s.KeepBackground
	if dubSwitch(c, "batch-bg", i18n.T("Keep background (music and effects)"), "", &keep, false) {
		a.batchSetSettings(func(s *services.BatchSettings) { s.KeepBackground = keep })
	}
	if es := dubDialogueEntries(); len(es) > 0 { // thread #89: on unless turned off, as a new dub
		on := s.Dialogue != dub.DialogueOff
		info := i18n.T("Conversations in Chinese and English tracks are recorded together by %s, each line hearing the one before it (slower); other lines line by line.", modelRef(es[0]))
		if dubSwitch(c, "batch-dialogue", i18n.T("Multi-speaker dialogue"), info, &on, false) {
			v := dub.DialogueOff
			if on {
				v = ""
			}
			a.batchSetSettings(func(s *services.BatchSettings) { s.Dialogue = v })
		}
	}
	if ok, name := a.dubTranslatorReady(dub.Settings{Translator: s.Translator}); ok {
		caption(c, i18n.T("Translator: %s", name)).TextColor(k.Text3)
	} else {
		fillCard(c.Key("batch-needs")).Gap(px(t, sp2)).Padding(px(t, sp3)).Children(func() {
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				iconMark(c, icons.DangerTriangle, t.Warning, "")
				caption(c, i18n.T("Translating needs a translation engine: a local model (Ollama, LM Studio) or a provider in Settings → Writing help.")).TextColor(k.Text).Grow(1).MinWidth(0)
			})
			if secondaryButton(c.Key("batch-llm"), i18n.T("Set up writing help")).Clicked() {
				a.navigate("/settings/writing")
			}
		})
	}
	n, m := len(b.files), len(s.Targets)
	ui.Column(c).Gap(px(t, sp2)).Margin(px(t, sp1), 0, 0, 0).Children(func() {
		caption(c, i18n.N(n, "%d video", "%d videos", n)+" × "+i18n.N(m, "%d language", "%d languages", m)+" = "+i18n.N(n*m, "%d dub", "%d dubs", n*m))
		if primaryButton(c.Key("batch-queue"), i18n.T("Add to queue")).Disabled(n == 0 || m == 0).Clicked() {
			a.batchEnqueue()
		}
	})
	for _, e := range b.errs {
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			iconMark(c, icons.DangerCircle, t.Danger, "")
			caption(c, e).TextColor(t.Danger).Grow(1).MinWidth(0)
		})
	}
}

// batchWatchPanel is the watch folder.
func (a *app) batchWatchPanel(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	w := a.batch.watch
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		sectionHeading(c, i18n.T("Watch folder"))
		infoTip(c.Key("watch-tip"), i18n.T("New videos dropped into the folder join the queue with the settings above (checked every 5 s, once a file stops growing). Files already there are left alone."))
	})
	if !w.Active {
		ui.Row(c).Children(func() {
			if ghostIconButton(c.Key("watch-start"), icons.FolderOpen, i18n.T("Watch a folder…")).Label(i18n.T("Watch a folder…")).Disabled(len(a.batch.settings.Targets) == 0).Clicked() {
				a.batchWatchChoose()
			}
		})
		return
	}
	fillCard(c.Key("watch-on")).Gap(px(t, 4)).Padding(px(t, sp3)).Children(func() {
		state := i18n.T("Watching")
		col := k.Success
		if w.Paused {
			state, col = i18n.T("Paused"), k.G[5]
		}
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			statusDot(c, col)
			truncText(c, state+" · "+shortPath(w.Dir)).FontSize(px(t, fsCaption)).Grow(1)
		})
		caption(c, i18n.N(w.Added, "%d video added", "%d videos added", w.Added))
		if w.Err != "" {
			caption(c, w.Err).TextColor(t.Danger)
		}
		ui.Row(c).Gap(px(t, 6)).Children(func() {
			if w.Paused {
				if secondaryButton(c.Key("watch-resume"), i18n.T("Resume watching")).Clicked() {
					a.batchWatchPause(false)
				}
			} else if secondaryButton(c.Key("watch-pause"), i18n.T("Pause")).Clicked() {
				a.batchWatchPause(true)
			}
			if ghostButton(c.Key("watch-stop"), i18n.T("Stop")).Clicked() {
				a.batchWatchStop()
			}
		})
	})
}

// batchQueue is the tabs (a segmented control) and the job cards;
// stacked, the page scrolls as a whole.
func (a *app) batchQueue(c *ui.Context, stacked bool) {
	t := c.Theme()
	b := &a.batch
	var tabs [3][]store.DubRecord
	for _, r := range b.items {
		switch a.batchStatus(r.ID) {
		case "active":
			tabs[0] = append(tabs[0], r)
		case "done":
			tabs[1] = append(tabs[1], r)
		default:
			tabs[2] = append(tabs[2], r)
		}
	}
	ui.Row(c).Children(func() {
		underlineTabs(c.Key("batch-tabs"), &b.tab, i18n.T("Active (%d)", len(tabs[0])), i18n.T("Completed (%d)", len(tabs[1])), i18n.T("Failed (%d)", len(tabs[2]))).
			Label(i18n.T("Queue")).Grow(1).Shrink(1).MinWidth(0)
	})
	list := tabs[min(max(b.tab, 0), 2)]
	if len(list) == 0 {
		empty := []string{i18n.T("No active dubs. Add videos on the left."), i18n.T("Nothing has completed yet."), i18n.T("No failed dubs.")}[min(max(b.tab, 0), 2)]
		emptyState(c.Key("batch-empty"), batchIcon, empty, "", nil).MinHeight(px(t, 160))
		return
	}
	cards := func() {
		ui.Column(c).Gap(px(t, sp2)).Children(func() {
			for _, r := range list {
				a.batchCard(c, r)
			}
		})
	}
	if stacked {
		cards()
		return
	}
	ui.Scroll(c.Key("batch-list")).Grow(1).Children(cards)
}

// batchCard is one video of the queue: a flat card with its title,
// languages and state, Export ▾ / Retry, Open, and ⋯ (Delete).
func (a *app) batchCard(c *ui.Context, r store.DubRecord) {
	t := c.Theme()
	k := tok(t)
	b := &a.batch
	p := b.docs[r.ID]
	id := r.ID
	status := a.batchStatus(id)
	card(c.Key("card-"+id)).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, 6)).Children(func() {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			iconMark(c, dubIcon, k.Text2, "")
			ui.Text(c, firstOf(r.Title, filepath.Base(r.Source))).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
			for _, l := range r.Languages {
				tagChip(c, l)
			}
			moreMenu(c.Key("more"), i18n.T("Dub actions"), func(m *ui.Menu) {
				if m.Item(i18n.T("Open in the Dub page")).Chosen() {
					a.openDubAny(id)
				}
				m.Separator()
				if m.Item(i18n.T("Delete…")).Chosen() {
					b.confirm = id
				}
			})
		})
		meta := []string{i18n.DateTime(r.CreatedAt)}
		if r.Duration > 0 {
			meta = append(meta, tlClock(r.Duration))
		}
		caption(c, strings.Join(meta, " · ")).SingleLine().Ellipsis("…")
		switch status {
		case "active":
			j := batchRunning(b.jobs[id])
			stage := i18n.T("Queued")
			if j.ID != "" && j.State == jobs.StateRunning {
				stage = dubStageName(dubStageOfKind(j.Kind))
				var pp dubJobParams
				dubJSON(j.Params, &pp)
				if pp.Lang != "" {
					stage = dubLangName(pp.Lang) + " · " + stage
				}
				if j.Progress.Message != "" && strings.Contains(j.Progress.Message, "/") {
					stage += " " + j.Progress.Message
				}
			}
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				if j.ID != "" && j.State == jobs.StateRunning {
					ui.Spinner(c)
				} else {
					statusDot(c, k.G[5])
				}
				ui.Text(c, stage).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
				if j.Progress.Fraction > 0 {
					ui.Progress(c, j.Progress.Fraction).Width(px(t, 120)).Shrink(1)
				}
				if ghostButton(c.Key("cancel"), i18n.T("Cancel")).Clicked() {
					a.batchCancel(id)
				}
			})
		case "done":
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
				iconMark(c, icons.CheckCircle, k.Success, "")
				caption(c, i18n.T("Done")).Grow(1)
				if p != nil {
					dropButton(c.Key("export"), icons.Export, i18n.T("Export")).Shrink(0).Menu(func(m *ui.Menu) {
						for _, l := range p.Targets {
							ll := l
							if m.Item(i18n.T("Export %s", dubLangName(ll))).Disabled(p.Track(ll).File == "").Chosen() {
								a.batchExport(id, ll)
							}
						}
					})
				}
			})
		default:
			why := i18n.T("Stopped before it finished.")
			if p != nil {
				for _, k := range p.Failed() {
					st := p.Stages[k]
					why = fmt.Sprintf("%s: %s", dubStageName(dubStageOfKind("dub."+strings.Split(k, ":")[0])), dubErrorText(&jobs.Error{Code: st.Code, Message: st.Message}))
					break
				}
			}
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				iconMark(c, icons.DangerCircle, t.Danger, "")
				caption(c, why).TextColor(t.Danger).MaxLines(3).Ellipsis("…").Tooltip(why).Grow(1).MinWidth(0)
				if secondaryButton(c.Key("retry"), i18n.T("Retry")).Clicked() {
					a.batchRetry(id)
				}
			})
		}
	})
}

// batchRunning is the running (else the oldest) job of a project.
func batchRunning(m map[string]jobs.Job) jobs.Job {
	var best jobs.Job
	for _, j := range m {
		if j.State == jobs.StateRunning {
			return j
		}
		if best.ID == "" || j.Created.Before(best.Created) {
			best = j
		}
	}
	return best
}

// openDubAny opens a project (the batch's too) on the Dub page.
func (a *app) openDubAny(id string) { a.openDub(id) }

// batchConfirm asks before deleting a queued video's dub.
func (a *app) batchConfirm(c *ui.Context) {
	b := &a.batch
	if b.confirm == "" {
		return
	}
	open := true
	switch ui.AlertDialog(c.Key("batch-confirm"), &open, i18n.T("Delete this dub?"), i18n.T("The project and every file made for it are deleted. The source file is kept."), i18n.T("Delete"), i18n.T("Cancel")) {
	case 0:
		id := b.confirm
		b.confirm = ""
		a.deleteDub(id)
	case 1:
		b.confirm = ""
	}
	if !open {
		b.confirm = ""
	}
}
