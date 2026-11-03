package main

// Voice files: importing .asvoice (and VoiceStudio's .ovsvoice) from the
// Import button, a drop on the library or Finder (App.OnOpenFile), and
// exporting a voice as .asvoice.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/gallery/persona"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// personaState is the import report dialog.
type personaState struct {
	reportOpen bool
	reportName string
	warnings   []string
}

func isVoiceFile(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == persona.Ext || ext == persona.OVSExt
}

func baseName(p string) string { return filepath.Base(p) }

// openVoiceFile handles a file opened with the app from Finder.
func (a *app) openVoiceFile(path string) {
	if isVoiceFile(path) {
		a.importVoice(path)
	}
}

// chooseVoiceFile asks for voice files to import.
func (a *app) chooseVoiceFile() {
	o := mygo.OpenDialogOptions{Title: i18n.T("Import voices"), Multiple: true, Filters: fileTypes(i18n.T("Voice files"), "asvoice", "ovsvoice")}
	a.openFiles(o, func(paths []string) {
		for _, p := range paths {
			a.importVoice(p)
		}
	})
}

// importVoice imports a voice file in the background, then opens it in
// the library; warnings and an unverified consent are reported.
func (a *app) importVoice(path string) {
	svc := a.lib.svc.Voices
	a.background(func() {
		res, err := svc.Import(context.Background(), path)
		a.post(func() {
			if err != nil {
				a.toastWarn(importErrorText(filepath.Base(path), err))
				return
			}
			v := res.Voice
			a.voiceChanged(core.VoiceChanged{Voice: v})
			a.openVoiceEditor(v.ID)
			a.cleanImportedReference(v) // checked and cleaned, the original kept (#83)
			msg := i18n.T("Imported “%s”", v.Name)
			if v.Consent.State == core.ConsentIncluded {
				msg = i18n.T("Imported “%s” (consent included, not verified)", v.Name)
			}
			a.toast(msg)
			if len(res.Warnings) > 0 {
				a.persona = personaState{reportOpen: true, reportName: v.Name, warnings: res.Warnings}
			}
		})
	})
}

func importErrorText(name string, err error) string {
	switch {
	case errors.Is(err, persona.ErrTooLarge):
		return i18n.T("%s is too large (100 MB at most).", name)
	case errors.Is(err, persona.ErrUnsupported):
		return i18n.T("%s was made by a newer AudioSmith.", name)
	case errors.Is(err, persona.ErrNotPersona), errors.Is(err, persona.ErrCorrupt), errors.Is(err, persona.ErrInvalid):
		return i18n.T("%s is not a voice file, or it is damaged.", name)
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("%s was not found.", name)
	}
	return i18n.T("Could not import %s: %v", name, err)
}

// personaDialogs show what an import could not carry over.
func (a *app) personaDialogs(c *ui.Context) {
	t := c.Theme()
	p := &a.persona
	ui.Modal(c, &p.reportOpen, func() {
		ui.Column(c).Width(px(t, 460)).Gap(px(t, 10)).Children(func() {
			ui.Text(c, i18n.T("Imported “%s” with notes", p.reportName)).FontSize(px(t, 15)).Bold()
			ui.Text(c, i18n.T("Some parts of the file could not be read and were left out:")).TextColor(t.TextMuted)
			for _, w := range p.warnings {
				ui.Text(c, "• "+w).FontSize(px(t, 12)).Selectable()
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if primaryButton(c.Key("persona-ok"), i18n.T("OK")).Clicked() {
					p.reportOpen = false
				}
			})
		})
	})
}

// licenceChoices are the export licences: labels and SPDX ids.
func licenceChoices() (labels, ids []string) {
	for _, id := range persona.Licences {
		label := id
		if id == persona.DefaultLicence {
			label = i18n.T("Personal use (no sharing)")
		}
		labels, ids = append(labels, label), append(ids, id)
	}
	ids[0] = "" // the default
	return labels, ids
}

// exportVoice asks where to save a voice file and writes it. A voice
// with nothing to share as its preview is told so before the save dialog
// (code review 1103 L3), with a way to its page to lock a take.
func (a *app) exportVoice(id string, includeRef bool, licence string) {
	v := a.findVoice(id)
	if v == nil {
		return
	}
	voice, svc := *v, a.lib.svc.Voices
	a.background(func() {
		ok := svc.HasPreview(voice)
		a.post(func() {
			if !ok {
				a.toastAction(ui.Toast{ID: "voice-no-preview", Title: i18n.T("This voice has no sample to share as its preview (its takes stay private): lock a take as its identity on its page first."),
					Timeout: 10 * time.Second, Action: i18n.T("Open voice"), OnAction: func() { a.openVoiceEditor(id) }})
				return
			}
			a.exportVoiceTo(id, voice.Name, includeRef, licence)
		})
	})
}

// exportVoiceTo asks where to save voice id's file and writes it.
func (a *app) exportVoiceTo(id, voiceName string, includeRef bool, licence string) {
	name := exportVoiceName(voiceName)
	svc, version := a.lib.svc.Voices, a.version
	o := mygo.SaveDialogOptions{Title: i18n.T("Export voice"), DefaultPath: name, Filters: fileTypes(i18n.T("Voice files"), "asvoice")}
	a.saveFile(o, func(dst string) {
		if !strings.EqualFold(filepath.Ext(dst), persona.Ext) {
			dst += persona.Ext
		}
		a.background(func() {
			err := svc.Export(context.Background(), id, dst, core.VoiceExport{IncludeReference: includeRef, Licence: licence, AppVersion: version})
			if err == nil {
				a.recordVoiceExport(id, dst) // M3: the Projects page's Exports (projects.go, thread #18)
			}
			a.post(func() {
				switch {
				case errors.Is(err, services.ErrNoPreview):
					a.toast(i18n.T("This voice has no sample to share as its preview (its takes stay private): lock a take as its identity on its page first."))
				case err != nil:
					a.toastWarn(i18n.T("Export failed: %v", err))
				default:
					a.toast(i18n.T("Exported %s", filepath.Base(dst)))
				}
			})
		})
	})
}

// exportVoiceName is a file name for a voice.
func exportVoiceName(name string) string {
	base := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if base == "" {
		base = "voice"
	}
	return base + persona.Ext
}
