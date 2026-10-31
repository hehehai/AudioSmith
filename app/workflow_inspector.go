package main

// The step details panel: a step's name and settings, its inputs (the
// scripts of a Start step, the clips of a Source step) and what is wrong
// with it; with no step selected, the workflow's name and problems.

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfInspector builds the Step panel.
func (a *app) wfInspector(c *ui.Context) {
	t := c.Theme()
	locked := a.wfLocked()
	n := a.wfNode(a.wfSel)
	if n == nil {
		a.wfWorkflowDetails(c)
		return
	}
	id := n.ID
	changed := func() { a.wfEdit(func(*workflow.Workflow) {}) }
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		wfKindTile(c, n.Kind, px(t, 26), px(t, 16))
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, n.Kind.Label()).Bold()
			ui.Text(c, n.Kind.About()).FontSize(px(t, 11.5)).TextColor(t.TextMuted)
		})
	})
	a.wfDiagnostics(c, a.wfReport().For(id))
	ui.Field(c, i18n.T("Step name"), func() {
		if ui.TextInput(c.Key("wf-step-name-"+id), &n.Name).Placeholder(n.Kind.Label()).Label(i18n.T("Step name")).Disabled(locked).Changed() {
			changed()
		}
	})
	switch n.Kind {
	case workflow.KindStart:
		a.wfScriptsPanel(c, n)
	case workflow.KindSource:
		a.wfClipsPanel(c, n)
	case workflow.KindSpeak, workflow.KindConvert:
		a.wfVoicePanel(c, n)
	case workflow.KindTranscribe:
		a.wfLangSelect(c, "wf-tr-lang-"+id, i18n.T("Language spoken"), &n.P.From, true)
		stt := dubModels(a.lib).STT
		if stt != "" {
			ui.Text(c, i18n.T("Uses %s for speech to text.", modelName(stt))).FontSize(px(t, 12)).TextColor(t.TextMuted)
		}
	case workflow.KindTranslate:
		a.wfLangSelect(c, "wf-tl-from-"+id, i18n.T("From"), &n.P.From, true)
		a.wfLangSelect(c, "wf-tl-to-"+id, i18n.T("Into"), &n.P.To, false)
		a.wfTranslatorSelect(c, n)
	case workflow.KindNormalize:
		a.wfNormalizePanel(c, n)
	case workflow.KindCondition:
		labels := make([]string, len(workflow.Matches))
		for i, m := range workflow.Matches {
			labels[i] = m.Label()
		}
		cur := n.P.Match.Label()
		ui.Field(c, i18n.T("Match"), func() {
			if ui.Select(c.Key("wf-cond-match-"+id), &cur, labels).Label(i18n.T("Match")).Disabled(locked).Changed() {
				if i := slices.Index(labels, cur); i >= 0 {
					m := workflow.Matches[i]
					a.wfEdit(func(w *workflow.Workflow) { w.Node(id).P.Match = m })
				}
			}
		})
		ui.Field(c, i18n.T("Text to look for"), func() {
			if ui.TextInput(c.Key("wf-cond-text-"+id), &n.P.Text).Label(i18n.T("Text to look for")).Disabled(locked).Changed() {
				changed()
			}
		})
		ui.Text(c, i18n.T("The yes branch runs when the text matches; otherwise the no branch runs. Capitals are ignored.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
	case workflow.KindExport:
		a.wfExportPanel(c, n)
	}
	ui.Box(c).Height(1).Background(tok(t).Line)
	ui.Row(c).Children(func() {
		if ghostIconButton(c.Key("wf-step-remove-"+id), icons.TrashBinMinimalistic, i18n.T("Remove step")).Label(i18n.T("Remove step")).Disabled(locked).Clicked() {
			a.wfSel = id
			a.wfRemoveSelected()
		}
	})
}

// wfWorkflowDetails is the panel without a selected step.
func (a *app) wfWorkflowDetails(c *ui.Context) {
	t := c.Theme()
	ui.Field(c, i18n.T("Workflow name"), func() {
		if ui.TextInput(c.Key("wf-name"), &a.wf.Name).Label(i18n.T("Workflow name")).Disabled(a.wfLocked()).Changed() {
			a.wfEdit(func(*workflow.Workflow) {})
		}
	})
	var general []workflow.Diagnostic
	for _, d := range a.wfReport().Diags {
		if d.Node == "" {
			general = append(general, d)
		}
	}
	a.wfDiagnostics(c, general)
	ui.Text(c, i18n.T("Select a step to set it up.")).TextColor(t.TextMuted)
	ui.Text(c, i18n.T("Drag from a step's right-hand port to another step to link them. Arrow keys move the selected step; Delete removes it. ⌘-scroll zooms, scroll or drag the background to move around.")).
		FontSize(px(t, 12)).TextColor(t.TextMuted)
}

// wfDiagnostics lists problems.
func (a *app) wfDiagnostics(c *ui.Context, ds []workflow.Diagnostic) {
	t := c.Theme()
	for i, d := range ds {
		col := t.Danger
		if d.Severity == workflow.Warning {
			col = t.Warning
		}
		ui.Row(c.Key(fmt.Sprintf("wf-diag-%d", i))).Gap(px(t, 6)).Padding(px(t, 6), px(t, 8)).Radius(6).Border(1, col).Children(func() {
			ui.Text(c, d.Text()).FontSize(px(t, 12)).TextColor(col).Grow(1).MinWidth(0)
			if d.Code == wfCodeNotDownloaded {
				if secondaryButton(c.Key("wf-diag-models"), i18n.T("Models")).Clicked() {
					a.navigate("/models")
				}
			}
		})
	}
}

// --- inputs ---------------------------------------------------------------------------------

// wfScriptsPanel edits a Start step's scripts.
func (a *app) wfScriptsPanel(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	locked := a.wfLocked()
	id := n.ID
	items := 0
	for _, s := range n.P.Scripts {
		items += len(workflow.SplitScript(s.Text))
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Scripts")).Bold().Grow(1)
		ui.Text(c, i18n.T("%d of %d items", items, workflow.MaxScripts)).FontSize(px(t, 12)).TextColor(t.TextMuted)
	})
	ui.Text(c, i18n.T("Each script is one item. A line with only --- splits a script into several.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
	for j := range n.P.Scripts {
		s := &n.P.Scripts[j]
		jj := j
		ui.Column(c.Key(fmt.Sprintf("wf-script-%s-%d", id, j))).Gap(px(t, 4)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(px(t, 6)).Children(func() {
				name := s.Name
				if name == "" {
					name = i18n.T("Script %d", jj+1)
				}
				ui.Text(c, name).FontSize(px(t, 12)).Bold().SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				chars := utf8.RuneCountInString(s.Text)
				col := t.TextMuted
				if chars > workflow.MaxScriptChars {
					col = t.Danger
				}
				ui.Text(c, fmt.Sprintf("%d / %d", chars, workflow.MaxScriptChars)).FontSize(px(t, 11)).TextColor(col)
				if len(n.P.Scripts) > 1 && iconButton(c.Key("x"), icons.Close, i18n.T("Remove %s", name)).Disabled(locked).Clicked() {
					a.wfEdit(func(w *workflow.Workflow) {
						p := &w.Node(id).P
						p.Scripts = slices.Delete(p.Scripts, jj, jj+1)
					})
				}
			})
			if ui.TextArea(c.Key("text"), &s.Text).Lines(3, 10).Label(i18n.T("Script %d", jj+1)).
				Placeholder(i18n.T("Welcome to this lesson. Let's get started.")).Disabled(locked).Changed() {
				a.wfEdit(func(*workflow.Workflow) {})
			}
		})
	}
	ui.Row(c).Gap(px(t, 6)).Wrap().Children(func() {
		if secondaryButton(c.Key("wf-script-add-"+id), i18n.T("Add script")).Disabled(locked || len(n.P.Scripts) >= workflow.MaxScripts).Clicked() {
			a.wfEdit(func(w *workflow.Workflow) {
				p := &w.Node(id).P
				p.Scripts = append(p.Scripts, workflow.Script{})
			})
		}
		if secondaryButton(c.Key("wf-script-import-"+id), i18n.T("Import scripts…")).Tooltip(i18n.T("TXT, Markdown, DOCX or EPUB: one script per file")).
			Disabled(locked).Clicked() {
			a.wfImportScripts(id)
		}
	})
}

// wfClipsPanel edits a Source step's clips.
func (a *app) wfClipsPanel(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	locked := a.wfLocked()
	id := n.ID
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Audio clips")).Bold().Grow(1)
		ui.Text(c, i18n.T("%d of %d", len(n.P.Clips), workflow.MaxClips)).FontSize(px(t, 12)).TextColor(t.TextMuted)
	})
	for j, cl := range n.P.Clips {
		jj := j
		ui.Row(c.Key(fmt.Sprintf("wf-clip-%s-%d", id, j))).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, cl.Name).FontSize(px(t, 12.5)).SingleLine().Ellipsis("…")
				ui.Text(c, fmt.Sprintf("%.1f MB", float64(cl.Size)/1e6)).FontSize(px(t, 11)).TextColor(t.TextMuted)
			})
			if iconButton(c.Key("x"), icons.Close, i18n.T("Remove %s", cl.Name)).Disabled(locked).Clicked() {
				a.wfEdit(func(w *workflow.Workflow) {
					p := &w.Node(id).P
					p.Clips = slices.Delete(p.Clips, jj, jj+1)
				})
			}
		})
	}
	zone := ui.Column(c.Key("wf-clip-drop-"+id)).Gap(px(t, 6)).Padding(px(t, 12)).Radius(8).Border(1, t.Border).AlignItems(ui.Center)
	if zone.FileDragOver() {
		zone.Border(2, t.Accent)
	}
	if files := zone.DroppedFiles(); files != nil {
		a.wfClipFiles(id, files)
	}
	zone.Children(func() {
		ui.Text(c, i18n.T("Drop audio files here, up to 50 clips of 64 MiB each.")).FontSize(px(t, 12)).TextColor(t.TextMuted).TextAlign(ui.Center)
		if secondaryButton(c.Key("wf-clip-add-"+id), i18n.T("Add audio…")).Disabled(locked || len(n.P.Clips) >= workflow.MaxClips).Clicked() {
			a.wfAddClips(id)
		}
	})
}

// --- settings -----------------------------------------------------------------------------

// wfVoicePanel sets a Speak or Convert step's model, voice, language and speed.
func (a *app) wfVoicePanel(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	locked := a.wfLocked()
	id := n.ID
	models := dubTTSModels()
	labels := make([]string, 0, len(models)+1)
	repos := make([]string, 0, len(models)+1)
	for _, e := range models {
		labels, repos = append(labels, e.DisplayName()), append(repos, e.Repo)
	}
	if n.P.Model != "" && !slices.Contains(repos, n.P.Model) {
		labels, repos = append(labels, modelName(n.P.Model)), append(repos, n.P.Model)
	}
	if n.P.Model == "" {
		labels, repos = append([]string{i18n.T("Choose a voice model")}, labels...), append([]string{""}, repos...)
	}
	cur := labels[max(slices.Index(repos, n.P.Model), 0)]
	ui.Field(c, i18n.T("Voice model"), func() {
		if ui.Select(c.Key("wf-model-"+id), &cur, labels).Label(i18n.T("Voice model")).Disabled(locked).Changed() {
			if i := slices.Index(labels, cur); i >= 0 && repos[i] != "" {
				repo := repos[i]
				a.wfEdit(func(w *workflow.Workflow) {
					p := &w.Node(id).P
					if p.Model != repo {
						p.Model, p.Voice = repo, ""
					}
				})
			}
		}
	})
	if n.P.Model != "" {
		if e := entryFor(n.P.Model); e.Repo != "" {
			if ready, known := a.modelOnDisk(e); known && !ready {
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, i18n.T("%s is not downloaded yet.", modelRef(e))).FontSize(px(t, 12)).TextColor(t.Warning).Grow(1)
					if secondaryButton(c.Key("wf-download-"+id), i18n.T("Download")).Clicked() {
						a.askLicence(e, func() { a.download(e) })
					}
				})
			}
		}
	}
	choices := a.wfVoiceChoices(n.P.Model)
	vl := make([]string, len(choices))
	for i, ch := range choices {
		vl[i] = ch.label
	}
	vi := slices.IndexFunc(choices, func(ch dubVoiceChoice) bool { return ch.ref == n.P.Voice })
	if vi < 0 {
		vl = append(vl, a.wfVoiceLabel(n.P.Voice))
		choices = append(choices, dubVoiceChoice{ref: n.P.Voice})
		vi = len(vl) - 1
	}
	vcur := vl[vi]
	ui.Field(c, i18n.T("Voice"), func() {
		if ui.Select(c.Key("wf-voice-"+id), &vcur, vl).Label(i18n.T("Voice")).Disabled(locked || n.P.Model == "").Changed() {
			if i := slices.Index(vl, vcur); i >= 0 {
				ref := choices[i].ref
				a.wfEdit(func(w *workflow.Workflow) { w.Node(id).P.Voice = ref })
			}
		}
	})
	a.wfLangSelect(c, "wf-lang-"+id, i18n.T("Language"), &n.P.Language, true)
	ui.Field(c, i18n.T("Speed"), func() {
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
			if slimSlider(c.Key("wf-speed-"+id), &n.P.Speed, workflow.MinSpeed, workflow.MaxSpeed).Step(0.05).Label(i18n.T("Speed")).
				Disabled(locked).Grow(1).Changed() {
				a.wfEdit(func(*workflow.Workflow) {})
			}
			ui.Text(c, fmt.Sprintf("%.2f×", n.P.Speed)).FontSize(px(t, 12)).Width(px(t, 44))
		})
	})
	if n.Kind == workflow.KindConvert {
		e := entryFor(n.P.Model)
		hint := i18n.T("Time-stretched to the original's length.")
		if e.Has(catalog.CapDuration) {
			hint = i18n.T("The model speaks to the original's length.")
		}
		if ui.Checkbox(c.Key("wf-match-"+id), &n.P.MatchDuration, i18n.T("Keep the original length")).Disabled(locked).Changed() {
			a.wfEdit(func(*workflow.Workflow) {})
		}
		if n.P.MatchDuration {
			ui.Text(c, hint).FontSize(px(t, 12)).TextColor(t.TextMuted)
		}
		ui.Text(c, i18n.T("Transcribes each clip, then speaks its words in this voice.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
	}
}

// wfVoiceChoices are the voices a step can use with a model: its
// default, saved voices it can speak with, design presets and its own
// speakers.
func (a *app) wfVoiceChoices(model string) []dubVoiceChoice {
	out := []dubVoiceChoice{{"", i18n.T("Default voice"), ""}}
	if model == "" {
		return out
	}
	e := entryFor(model)
	seen := map[string]int{}
	for i := range a.voices {
		v := &a.voices[i]
		if ok, _ := voiceUsable(v, e); ok {
			label := v.Name
			if k := seen[v.Name]; k > 0 {
				label = fmt.Sprintf("%s (%d)", v.Name, k+1)
			}
			seen[v.Name]++
			out = append(out, dubVoiceChoice{v.ID, label, i18n.T("Saved voices")})
		}
	}
	if canDesign(e) {
		for _, pr := range dubPresets() {
			out = append(out, dubVoiceChoice{dub.PresetPrefix + pr.ID, pr.Name.In(i18n.Locale()), i18n.T("Design presets")})
		}
	}
	for _, id := range e.VoiceIDsIn(i18n.Locale()) {
		out = append(out, dubVoiceChoice{dub.SpeakerPrefix + id, id, i18n.T("Model voices")})
	}
	return out
}

// wfVoiceLabel names a voice reference.
func (a *app) wfVoiceLabel(ref string) string {
	if ref == "" {
		return i18n.T("Default voice")
	}
	return a.dubVoiceLabel(ref)
}

// wfLangSelect picks a language (auto: with "Auto-detect").
func (a *app) wfLangSelect(c *ui.Context, key, label string, v *string, auto bool) {
	ids := slices.Clone(dubTargetLangs)
	if auto {
		ids = append([]string{""}, ids...)
	}
	if *v != "" && !slices.Contains(ids, *v) {
		ids = append(ids, *v)
	}
	names := make([]string, len(ids))
	for i, l := range ids {
		names[i] = dubLangName(l)
	}
	cur := names[max(slices.Index(ids, *v), 0)]
	ui.Field(c, label, func() {
		if ui.Select(c.Key(key), &cur, names).Label(label).Disabled(a.wfLocked()).Changed() {
			if i := slices.Index(names, cur); i >= 0 {
				l := ids[i]
				a.wfEdit(func(*workflow.Workflow) { *v = l })
			}
		}
	})
}

// wfTranslatorSelect picks a Translate step's translator.
func (a *app) wfTranslatorSelect(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	id := n.ID
	ids := []string{"", "deepl", "google", "microsoft"}
	names := []string{i18n.T("Language model (Settings → Writing help)"), "DeepL", "Google Cloud Translation", "Microsoft Translator"}
	cur := names[max(slices.Index(ids, n.P.Translator), 0)]
	ui.Field(c, i18n.T("Translator"), func() {
		if ui.Select(c.Key("wf-translator-"+id), &cur, names).Label(i18n.T("Translator")).Disabled(a.wfLocked()).Changed() {
			if i := slices.Index(names, cur); i >= 0 {
				tr := ids[i]
				a.wfEdit(func(w *workflow.Workflow) { w.Node(id).P.Translator = tr })
			}
		}
	})
	if ok, name := a.dubTranslatorReady(dub.Settings{Translator: n.P.Translator}); ok && name != "" {
		ui.Text(c, i18n.T("Translates with %s.", name)).FontSize(px(t, 12)).TextColor(t.TextMuted)
	} else if n.P.Translator == "" {
		if secondaryButton(c.Key("wf-llm-settings"), i18n.T("Set up a language model…")).Clicked() {
			a.navigate("/settings/writing")
		}
	}
	if n.P.Translator != "" {
		ui.Text(c, i18n.T("Text goes to this online service; its key is in Settings → Writing help.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
	}
}

// wfNormalizePanel sets a Normalize step's level.
func (a *app) wfNormalizePanel(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	locked := a.wfLocked()
	id := n.ID
	mode := 0
	if n.P.Level == workflow.LevelPeak {
		mode = 1
	}
	ui.Field(c, i18n.T("Level"), func() {
		if segmentedFit(c.Key("wf-norm-mode-"+id), &mode, i18n.T("Loudness"), i18n.T("Peak")).Label(i18n.T("Level")).Disabled(locked).Changed() {
			m := mode
			a.wfEdit(func(w *workflow.Workflow) {
				p := &w.Node(id).P
				if m == 1 {
					p.Level, p.Target = workflow.LevelPeak, workflow.DefaultPeak
				} else {
					p.Level, p.Target = workflow.LevelLoudness, workflow.DefaultLoudness
				}
			})
		}
	})
	lo, hi, unit := workflow.MinLoudness, workflow.MaxLoudness, "LUFS"
	if n.P.Level == workflow.LevelPeak {
		lo, hi, unit = workflow.MinPeak, workflow.MaxPeak, "dBFS"
	}
	ui.Field(c, i18n.T("Target (%s)", unit), func() {
		if numberInput(c.Key("wf-norm-target-"+id), &n.P.Target, lo, hi, 1, numOpts{Label: i18n.T("Target (%s)", unit), Disabled: locked}) {
			a.wfEdit(func(*workflow.Workflow) {})
		}
	})
	if n.P.Level == workflow.LevelPeak {
		ui.Text(c, i18n.T("Raises or lowers each file so its loudest sample is at this level.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
	} else {
		ui.Text(c, i18n.T("Brings each file to this loudness (−16 for podcasts, −19 for audiobooks, −23 for broadcast), peaks under −1 dBTP.")).
			FontSize(px(t, 12)).TextColor(t.TextMuted)
	}
}

// wfExportPanel sets an Export step's folder, names and format.
func (a *app) wfExportPanel(c *ui.Context, n *workflow.Node) {
	t := c.Theme()
	locked := a.wfLocked()
	id := n.ID
	ui.Field(c, i18n.T("Folder"), func() {
		ui.Column(c).Gap(px(t, 4)).Children(func() {
			if n.P.Folder == "" {
				ui.Text(c, i18n.T("None: the results stay in the Results list, ready to export.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
			} else {
				ui.Text(c, n.P.Folder).FontSize(px(t, 12)).MaxLines(2).Ellipsis("…")
			}
			ui.Row(c).Gap(px(t, 6)).Children(func() {
				if secondaryButton(c.Key("wf-folder-"+id), i18n.T("Choose…")).Disabled(locked).Clicked() {
					a.wfChooseFolder(id)
				}
				if n.P.Folder != "" && secondaryButton(c.Key("wf-folder-clear-"+id), i18n.T("Clear")).Disabled(locked).Clicked() {
					a.wfEdit(func(w *workflow.Workflow) { w.Node(id).P.Folder = "" })
				}
			})
		})
	})
	ui.Field(c, i18n.T("File names"), func() {
		if ui.TextInput(c.Key("wf-template-"+id), &n.P.Template).Label(i18n.T("File names")).Disabled(locked).Changed() {
			a.wfEdit(func(*workflow.Workflow) {})
		}
	})
	ui.Text(c, i18n.T("Tokens: %s", strings.Join(workflow.TemplateTokens, " "))).FontSize(px(t, 11.5)).TextColor(t.TextMuted)
	in := a.wfReport().In[id]
	formats := workflow.FormatsFor(in)
	if len(formats) == 0 {
		ui.Text(c, i18n.T("Link a step into this one to choose a format.")).FontSize(px(t, 12)).TextColor(t.TextMuted)
		return
	}
	names := make([]string, len(formats))
	for i, f := range formats {
		names[i] = wfFormatName(f)
	}
	cur := names[max(slices.Index(formats, workflow.ExportFormat(n.P.Format, in)), 0)]
	ui.Field(c, i18n.T("Format"), func() {
		if ui.Select(c.Key("wf-format-"+id), &cur, names).Label(i18n.T("Format")).Disabled(locked).Changed() {
			if i := slices.Index(names, cur); i >= 0 {
				f := formats[i]
				a.wfEdit(func(w *workflow.Workflow) { w.Node(id).P.Format = f })
			}
		}
	})
}

// wfFormatName names an export format.
func wfFormatName(f string) string {
	switch f {
	case workflow.FormatWAV:
		return i18n.T("WAV (24-bit)")
	case workflow.FormatM4A:
		return i18n.T("M4A (AAC)")
	case workflow.FormatTXT:
		return i18n.T("Text (TXT)")
	case workflow.FormatSRT:
		return i18n.T("Subtitles (SRT)")
	case workflow.FormatVTT:
		return i18n.T("Subtitles (WebVTT)")
	case workflow.FormatJSON:
		return i18n.T("Transcript (JSON)")
	}
	return strings.ToUpper(f)
}
