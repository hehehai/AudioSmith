package main

// Transcribe's exports (TXT, SRT, VTT, JSON; one or all) and reusing a
// transcript as the Studio's script; split from transcribe.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/lang"
	"github.com/hehehai/AudioSmith/internal/services"
)

// transcriptTitle names a transcript in lists.
func transcriptTitle(t core.Transcript) string {
	switch {
	case t.Title != "":
		return t.Title
	case t.Source == core.SourceDictation && t.App != "":
		return i18n.T("Dictation in %s", t.App)
	case t.Source == core.SourceDictation:
		return i18n.T("Dictation")
	}
	return i18n.T("Recording")
}

// exportName is a file name for a transcript's export.
func transcriptFileName(t core.Transcript, ext string) string {
	base := strings.TrimSuffix(t.Title, filepath.Ext(t.Title))
	if base == "" {
		base = "transcript-" + t.CreatedAt.Format("2006-01-02-1504")
	}
	return base + "." + ext
}

// exportTranscript saves the transcript shown as format.
func (a *app) exportTranscript(format string) {
	a.trExport = false
	t := a.trDetail
	if t == nil {
		return
	}
	data, err := services.Export(*t, format)
	if err != nil {
		a.toastWarn(i18n.T("Export failed: %v", err))
		return
	}
	a.saveTranscriptFile(i18n.T("Export the transcript"), transcriptFileName(*t, format), format, data)
}

// exportAllTranscripts saves every transcript as one text file.
func (a *app) exportAllTranscripts() {
	svc := a.trSvc
	a.background(func() {
		l, err := svc.List(context.Background(), core.TranscriptQuery{})
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Export failed: %v", err))
				return
			}
			data := services.ExportAll(l, i18n.DateTime)
			a.saveTranscriptFile(i18n.T("Export all transcripts"), "transcripts_"+time.Now().Format("2006-01-02")+".txt", "txt", data)
		})
	})
}

// saveTranscriptFile asks where to save data, then writes it. In tests
// (no window) it writes to trExportDir.
func (a *app) saveTranscriptFile(title, name, ext string, data []byte) {
	o := mygo.SaveDialogOptions{Title: title, DefaultPath: name, Filters: fileTypes(strings.ToUpper(ext), ext)}
	a.saveFileOr(trExportDir, o, func(dst string) {
		a.background(func() {
			err := os.WriteFile(dst, data, 0o644)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Export failed: %v", err))
				} else {
					a.toast(i18n.T("Exported %s", filepath.Base(dst)))
				}
			})
		})
	})
}

// trExportDir is where exports go in tests.
var trExportDir string

// reuseTranscript makes the transcript the Studio's script.
func (a *app) reuseTranscript() {
	t := a.trDetail
	if t == nil {
		return
	}
	a.text = t.Preferred()
	a.script.set(a.text)
	if t.Language != "" {
		// The Studio's model takes its own language values.
		p := a.preset()
		codes, valueOf, all := languageSupport(p)
		if mc, ok := lang.Match(codes, t.Language); ok || all {
			if all {
				mc = t.Language
			}
			a.languageChosen(t.Language, valueOf(mc))
		}
	}
	a.navigate("/studio")
	a.toast(i18n.T("The transcript is the Studio's script."))
}
