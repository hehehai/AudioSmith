package main

// The profile editor: the right pane of /voices for one voice. Name,
// transcript, style, language and tags; the portrait; the reference (play,
// replace); "Try this voice" with a lock to the take it makes; the
// ownership statement; where the voice was used; export.

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// voiceEdit is the editor's state for one voice.
type voiceEdit struct {
	id   string
	orig core.Voice // as stored when last loaded or saved

	name, transcript, style, language, tags string

	usage  *core.VoiceUsage
	saving bool

	tryOpen  bool
	tryText  string
	tryModel string // catalogue label

	unlockOpen, revokeOpen bool
	consentOpen            bool
	consent                micState

	exportOpen    bool
	exportRef     bool
	exportLicence string
}

func newVoiceEdit(v core.Voice) *voiceEdit {
	e := &voiceEdit{id: v.ID, exportRef: true, tryText: i18n.T("Hello — this is a test of this voice.")}
	e.load(v)
	return e
}

func (e *voiceEdit) load(v core.Voice) {
	e.orig = v
	e.name, e.transcript, e.style, e.language = v.Name, v.RefText, v.Style, v.Language
	e.tags = strings.Join(v.Tags, ", ")
}

// refresh takes a newer stored profile, keeping the fields being edited.
func (e *voiceEdit) refresh(v core.Voice) {
	old := e.orig
	keep := func(cur *string, was, now string) {
		if *cur == was {
			*cur = now
		}
	}
	keep(&e.name, old.Name, v.Name)
	keep(&e.transcript, old.RefText, v.RefText)
	keep(&e.style, old.Style, v.Style)
	keep(&e.language, old.Language, v.Language)
	keep(&e.tags, strings.Join(old.Tags, ", "), strings.Join(v.Tags, ", "))
	e.orig = v
}

// edited is the profile with the editor's fields.
func (e *voiceEdit) edited() core.Voice {
	v := e.orig
	v.Name, v.RefText, v.Style, v.Language = strings.TrimSpace(e.name), strings.TrimSpace(e.transcript), strings.TrimSpace(e.style), strings.TrimSpace(e.language)
	v.Tags = nil
	for _, tag := range strings.FieldsFunc(e.tags, func(r rune) bool { return r == ',' || r == '，' }) {
		if tag = strings.TrimSpace(tag); tag != "" && !slices.Contains(v.Tags, tag) {
			v.Tags = append(v.Tags, tag)
		}
	}
	return v
}

func (e *voiceEdit) dirty() bool {
	v, o := e.edited(), e.orig
	return v.Name != o.Name || v.RefText != o.RefText || v.Style != o.Style || v.Language != o.Language || !slices.Equal(v.Tags, o.Tags)
}

// loadUsage reads where a voice was used.
func (a *app) loadUsage(id string) {
	svc := a.lib.svc.Voices
	a.background(func() {
		u, err := svc.Usage(context.Background(), id)
		a.post(func() {
			if err == nil && a.edit != nil && a.edit.id == id {
				a.edit.usage = &u
			}
		})
	})
}

// saveVoiceEdit saves the editor's fields.
func (a *app) saveVoiceEdit() {
	e := a.edit
	if e == nil || e.saving {
		return
	}
	v := e.edited()
	if v.Name == "" {
		a.toast(i18n.T("Give the voice a name."))
		return
	}
	e.saving = true
	svc := a.lib.svc.Voices
	a.background(func() {
		saved, err := svc.Save(context.Background(), v)
		a.post(func() {
			if a.edit == e {
				e.saving = false
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not save the voice profile: %v", err))
				return
			}
			if a.edit == e {
				e.load(saved)
			}
			a.toast(i18n.T("Saved"))
		})
	})
}

// voiceAction runs a change of a voice's files in the background and
// reports it.
func (a *app) voiceAction(fn func(ctx context.Context, svc core.VoiceService) (core.Voice, error), ok string) {
	svc := a.lib.svc.Voices
	a.background(func() {
		_, err := fn(context.Background(), svc)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not change the voice: %v", err))
				return
			}
			if ok != "" {
				a.toast(ok)
			}
		})
	})
}

// setVoicePortrait makes an image a saved voice's portrait.
func (a *app) setVoicePortrait(id, path string) {
	a.background(func() {
		png, err := portraitPNG(path)
		a.post(func() {
			if err != nil {
				a.toastWarn(portraitErrorText(err))
				return
			}
			v := a.findVoice(id)
			if v == nil {
				return
			}
			cur := *v
			a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
				return svc.SaveFiles(ctx, cur, core.VoiceFiles{Portrait: png})
			}, "")
		})
	})
}

// consentStatement is the statement a user reads to verify a voice is theirs.
func consentStatement() string {
	return i18n.T("I confirm that this voice is my own, and I consent to AudioSmith cloning it on my behalf.")
}

func (a *app) voiceEditor(c *ui.Context) {
	t := c.Theme()
	e := a.edit
	v := a.findVoice(e.id)
	if v == nil {
		return
	}
	if e.consent.phase != "" {
		c.After(100 * time.Millisecond)
	}
	k := tok(t)
	ui.Column(c.Key("editor-"+v.ID)).Padding(px(t, sp3), px(t, sp5), px(t, sp5)).Gap(px(t, sp4)).Children(func() {
		panelHeader(c, i18n.T("Edit voice profile"), func() { a.edit = nil })
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			id := v.ID
			a.portraitPicker(c, "editor-portrait", v.Name, a.portrait(v), func(p string) { a.setVoicePortrait(id, p) }, func() {
				cur := *v
				a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
					return svc.SaveFiles(ctx, cur, core.VoiceFiles{RemovePortrait: true})
				}, "")
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp1)).Children(func() {
				sectionHeading(c, v.Name).Tooltip(v.Name)
				ui.Row(c).Gap(px(t, sp2)).Wrap().Children(func() { voiceBadges(c, v) })
				truncText(c, voiceKindLabel(v)+" · "+i18n.DateTime(v.CreatedAt)).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			})
		})
		a.editorAudio(c, v)
		a.editorReference(c, v) // voice_editor_ref.go (#83)
		a.editorFields(c, v)
		a.editorTry(c, v)
		a.editorLock(c, v)
		a.editorConsent(c, v)
		a.editorUsage(c, v)
		a.editorExport(c, v)
		ui.Box(c).Height(1).Background(k.Line)
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			if iconButton(c.Key("editor-delete"), icons.TrashBinMinimalistic, i18n.T("Delete")).Clicked() {
				a.askDeleteVoice(v.ID)
			}
			a.voiceLongformMenu(c, v) // M3: Use in Stories, Narrate an audiobook (longform_voices.go, thread #18)
			ui.Spacer(c)
			if secondaryButton(c.Key("editor-studio"), i18n.T("Use in Studio")).Clicked() {
				a.useVoiceInStudio(v)
			}
			if primaryButton(c.Key("editor-save"), i18n.T("Save")).Disabled(!e.dirty() || e.saving).Clicked() {
				a.saveVoiceEdit()
			}
		})
	})
	a.editorAlerts(c, v)
}

// editorAudio plays the voice's reference or sample, and replaces it.
func (a *app) editorAudio(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	what := i18n.T("Reference clip")
	switch {
	case v.Locked && v.Sample != "":
		what = i18n.T("Locked take")
	case v.Kind == core.VoiceDesigned && v.Sample != "":
		what = i18n.T("Voice sample")
	case v.Kind == core.VoiceDesigned:
		what = i18n.T("No sample yet")
	}
	if v.Kind == core.VoiceCloned && !v.Locked && v.RefSeconds > 0 {
		what += " · " + i18n.T("%.1f s", v.RefSeconds)
	}
	ui.Row(c.Key("editor-audio")).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, sp2)).Radius(px(t, radiusCard)).Background(tok(t).Subtle).Children(func() {
		if j := a.voiceJob("sample", v.ID); j.running() {
			ui.Spinner(c)
			caption(c, j.status).Grow(1).MinWidth(0)
			return
		}
		playing := a.voicePlaying(v)
		b := playButton(c.Key("editor-play"), playing, true, 32)
		if !playing && a.voiceAudio(v) == "" {
			b.Label(i18n.T("Preview voice")).Tooltip(i18n.T("Preview voice"))
		}
		if b.Clicked() {
			a.playVoice(v)
		}
		truncText(c, what).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Grow(1)
		if v.Kind == core.VoiceCloned && iconButton(c.Key("editor-replace"), icons.UploadMinimalistic, i18n.T("Replace reference…")).Clicked() {
			a.startClone(v.ID)
		}
		if v.Kind == core.VoiceDesigned && iconButton(c.Key("editor-design"), icons.MagicStick3, i18n.T("Open in Design")).Clicked() {
			a.openDesignFor(v)
		}
	})
	if j := a.voiceJob("sample", v.ID); j != nil && j.err != "" {
		statusMark(c, icons.DangerCircle, t.Danger, j.err).Tooltip(j.err)
	}
}

func (a *app) editorFields(c *ui.Context, v *core.Voice) {
	e := a.edit
	ui.Field(c, i18n.T("Profile name"), func() {
		ui.TextInput(c.Key("editor-name"), &e.name).Label(i18n.T("Profile name"))
	})
	if v.Kind == core.VoiceCloned {
		ui.Field(c, i18n.T("Transcript"), func() {
			ui.TextArea(c.Key("editor-transcript"), &e.transcript).Lines(2, 5).
				Placeholder(i18n.T("(Optional) What the clip says. Leave it empty to let the engine listen.")).Label(i18n.T("Transcript"))
		})
		a.styleField(c, "editor-style", &e.style)
	} else if v.Design != "" {
		ui.Field(c, i18n.T("Description"), func() {
			ui.Text(c, v.Design).FontSize(px(c.Theme(), 12)).Selectable()
		})
	}
	ui.Field(c, i18n.T("Language"), func() {
		ui.Autocomplete(c.Key("editor-language"), &e.language, voiceLanguages()).Placeholder(i18n.T("Auto")).Label(i18n.T("Language"))
	})
	ui.Field(c, i18n.T("Tags"), func() {
		ui.TextInput(c.Key("editor-tags"), &e.tags).Placeholder(i18n.T("e.g. narration, warm")).Label(i18n.T("Tags"))
	})
}

// tryModels are the models the voice can be tried with.
func tryModels(v *core.Voice) []catalog.Entry {
	var out []catalog.Entry
	for _, e := range voiceCatalog().ForTask(catalog.TaskTTS) {
		if ok, _ := voiceUsable(v, e); ok && v.Kind != core.VoiceBuiltin {
			out = append(out, e)
		}
	}
	return out
}

// editorTry is "Try this voice": a phrase, a model, a take; the take can
// become the voice's locked identity.
func (a *app) editorTry(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	e := a.edit
	collapsible(c.Key("editor-try"), i18n.T("Try this voice"), &e.tryOpen, nil, func() {
		ms := tryModels(v)
		if len(ms) == 0 {
			caption(c, i18n.T("No model can speak with this voice."))
			return
		}
		labels := make([]string, len(ms))
		for i, m := range ms {
			labels[i] = m.Label
		}
		if !slices.Contains(labels, e.tryModel) {
			e.tryModel = labels[0]
			if p := a.preset(); slices.Contains(labels, p.Label) {
				e.tryModel = p.Label
			}
		}
		// The select shows friendly names; e.tryModel keeps the label.
		names := make([]string, len(ms))
		for i, m := range ms {
			names[i] = m.DisplayName()
		}
		pick := names[slices.Index(labels, e.tryModel)]
		ui.Column(c).Gap(px(t, 8)).Children(func() {
			ui.TextArea(c.Key("try-text"), &e.tryText).Lines(2, 4).Label(i18n.T("Test phrase"))
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				if ui.Select(c.Key("try-model"), &pick, names).Label(i18n.T("Model")).Tooltip(modelTip(ms[slices.Index(labels, e.tryModel)])).Grow(1).MinWidth(0).Changed() {
					if i := slices.Index(names, pick); i >= 0 {
						e.tryModel = labels[i]
					}
				}
				j := a.voiceJob("try", v.ID)
				if secondaryButton(c.Key("try-go"), i18n.T("Generate preview")).Disabled(j.running() || strings.TrimSpace(e.tryText) == "").Clicked() {
					a.tryVoice(v.ID)
				}
			})
			j := a.voiceJob("try", v.ID)
			switch {
			case j == nil:
			case j.running():
				ui.Progress(c, j.fraction).Label(j.status)
				caption(c, j.status)
			case j.err != "":
				ui.Text(c, j.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Selectable()
				if j.needDownload != nil && secondaryIconButton(c.Key("try-download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Clicked() {
					a.download(*j.needDownload)
				}
			default:
				take := j.take
				ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
					if playButton(c.Key("try-play"), a.pl.Playing(take.ID), true, 28).Clicked() {
						a.play(take)
					}
					caption(c, i18n.T("%.1f s · seed %d", take.AudioSeconds, take.Seed)).SingleLine().Grow(1)
					if v.LockTake != take.ID && ghostIconButton(c.Key("try-lock"), icons.LockKeyhole, i18n.T("Lock voice identity")).Label(i18n.T("Lock voice identity")).
						Tooltip(i18n.T("Every take with this voice reuses this take's audio and seed")).Clicked() {
						a.lockVoice(v.ID, take.ID)
					}
				})
			}
		})
	})
}

// tryVoice generates the test phrase with the chosen model.
func (a *app) tryVoice(id string) {
	e := a.edit
	v := a.findVoice(id)
	if e == nil || v == nil {
		return
	}
	m, ok := voiceCatalog().ByLabel(e.tryModel)
	if !ok {
		return
	}
	t := core.Take{Text: strings.TrimSpace(e.tryText), Model: m.Repo}
	a.fillVoice(&t, v, m)
	a.voiceGenerate("try", id, t, func() { a.tryVoice(id) })
}

func (a *app) lockVoice(id, takeID string) {
	a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
		return svc.Lock(ctx, id, takeID)
	}, i18n.T("Voice locked: every take reuses the same audio and seed."))
}

func (a *app) editorLock(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	if !v.Locked {
		return
	}
	ui.Column(c.Key("editor-lock")).Children(func() {
		banner(c, icons.LockKeyhole, tok(t).Text2, i18n.T("This voice is reproducible: every take uses the same locked audio and seed (%d).", v.Seed), func() {
			if ghostButton(c.Key("editor-unlock"), i18n.T("Unlock")).Clicked() {
				a.edit.unlockOpen = true
			}
		})
	})
}

// editorConsent is the voice ownership section: the statement, recorded.
func (a *app) editorConsent(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	e := a.edit
	collapsible(c.Key("editor-consent"), i18n.T("Voice ownership"), &e.consentOpen, func() {
		if v.Verified() {
			iconMark(c, icons.VerifiedCheck, t.Success, i18n.T("Verified"))
		}
	}, func() {
		a.loadDevices() // once
		ui.Column(c).Gap(px(t, 8)).Children(func() {
			switch {
			case v.Verified():
				ui.Text(c, i18n.T("You recorded the consent statement on %s.", i18n.Date(v.Consent.At))).FontSize(px(t, fsCaption))
				caption(c, "“"+v.Consent.Statement+"”")
				ui.Row(c).Children(func() {
					if ghostButton(c.Key("consent-revoke"), i18n.T("Revoke")).Clicked() {
						e.revokeOpen = true
					}
				})
				return
			case v.Consent.State == core.ConsentIncluded:
				statusMark(c, icons.DangerTriangle, t.Warning, i18n.T("The imported file says the speaker consented. Record the statement yourself to verify the voice here.")).
					Tooltip(i18n.T("The imported file says the speaker consented. Record the statement yourself to verify the voice here."))
			}
			ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("Read this statement aloud:")).FontSize(px(t, fsCaption)).TextColor(tok(t).Text2).Grow(1).MinWidth(0)
				infoTip(c.Key("consent-why"), i18n.T("Record yourself reading the statement below to show the voice is yours. Verification will be needed for sharing and agent features, never for making speech on this Mac."))
			})
			ui.Text(c, "“"+consentStatement()+"”").FontWeight(600).Padding(px(t, sp2), px(t, sp3)).Radius(px(t, radiusControl)).Background(tok(t).Subtle)
			id, statement, loc := v.ID, consentStatement(), i18n.Locale()
			ui.Column(c).AlignItems(ui.Center).Gap(px(t, 6)).Children(func() {
				a.micControls(c, &e.consent, func(path string) {
					a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
						return svc.SetConsent(ctx, id, core.ConsentRecording{Audio: path, Statement: statement, Locale: loc})
					}, i18n.T("Voice ownership verified"))
				})
			})
		})
	})
}

// editorUsage says where the voice was used.
func (a *app) editorUsage(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	u := a.edit.usage
	if u == nil || (u.Takes == 0 && u.Segments == 0) {
		return
	}
	ui.Column(c.Key("editor-usage")).Gap(px(t, sp1)).Children(func() {
		ui.Text(c, i18n.T("Where this voice has been used")).FontWeight(600)
		parts := []string{i18n.N(u.Takes, "%d take", "%d takes", u.Takes)}
		if len(u.Projects) > 0 {
			parts = append(parts, i18n.N(len(u.Projects), "%d project", "%d projects", len(u.Projects)),
				i18n.N(u.Segments, "%d segment", "%d segments", u.Segments))
		}
		caption(c, strings.Join(parts, " · "))
		for _, p := range u.Projects {
			a.projectUsageLink(c, p) // M3: opens the project (longform_projects.go, thread #18)
		}
		if u.Takes > 0 {
			ui.Row(c).Children(func() {
				if ghostIconButton(c.Key("usage-history"), icons.History, i18n.T("Show in History")).Label(i18n.T("Show in History")).Clicked() {
					a.navigate("/history")
				}
			})
		}
	})
}

// editorExport writes the voice as a .asvoice file.
func (a *app) editorExport(c *ui.Context, v *core.Voice) {
	t := c.Theme()
	e := a.edit
	collapsible(c.Key("editor-export"), i18n.T("Export voice file"), &e.exportOpen, nil, func() {
		ui.Column(c).Gap(px(t, sp2)).Children(func() {
			if v.Kind == core.VoiceCloned {
				ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("export-ref"), &e.exportRef, i18n.T("Include the reference clip")).MinWidth(0)
					infoTip(c.Key("export-ref-info"), i18n.T("Off: the file carries only a short preview, and the voice can't be cloned as faithfully."))
				})
			}
			labels, ids := licenceChoices()
			cur := labels[0]
			if i := slices.Index(ids, e.exportLicence); i >= 0 {
				cur = labels[i]
			}
			ui.Field(c, i18n.T("Licence"), func() {
				if ui.Select(c.Key("export-licence"), &cur, labels).Label(i18n.T("Licence")).Changed() {
					e.exportLicence = ids[slices.Index(labels, cur)]
				}
			})
			if note := ncNote(v); note != "" {
				banner(c, icons.DangerTriangle, t.Warning, note, nil)
			}
			ui.Row(c).Children(func() {
				if secondaryIconButton(c.Key("export-go"), icons.Export, i18n.T("Export…")).Label(i18n.T("Export…")).Clicked() {
					a.exportVoice(v.ID, e.exportRef && v.Kind == core.VoiceCloned, e.exportLicence)
				}
			})
		})
	})
}

// ncNote warns when the voice was made with a model whose weights allow
// only non-commercial use.
func ncNote(v *core.Voice) string {
	for _, repo := range []string{v.CreatedWith, v.Model} {
		if repo == "" {
			continue
		}
		if e, ok := catalog.Builtin().ByRepo(repo); ok && !e.Licence.Commercial {
			return i18n.T("Made with %s, whose licence (%s) allows only non-commercial use: what you make with this voice may be bound by it too.", modelRef(e), e.Licence.Name)
		}
	}
	return ""
}

func (a *app) editorAlerts(c *ui.Context, v *core.Voice) {
	e := a.edit
	id := v.ID
	if ui.AlertDialog(c, &e.unlockOpen, i18n.T("Unlock this voice?"), i18n.T("Takes will no longer reuse the locked audio and seed, so they may sound a little different each time."),
		i18n.T("Cancel"), i18n.T("Unlock voice")) == 1 {
		a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) { return svc.Unlock(ctx, id) }, i18n.T("Voice unlocked"))
	}
	if ui.AlertDialog(c, &e.revokeOpen, i18n.T("Revoke the verification?"), i18n.T("The consent recording is deleted. You can record it again at any time."),
		i18n.T("Cancel"), i18n.T("Revoke verification")) == 1 {
		a.voiceAction(func(ctx context.Context, svc core.VoiceService) (core.Voice, error) {
			return svc.RevokeConsent(ctx, id)
		}, i18n.T("Verification revoked"))
	}
}
