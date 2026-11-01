package main

// Workflow files: exporting a run's results, and the inputs (scripts,
// clips, a folder) chosen or dropped; split from workflow.go.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/workflow"
)

// wfExportAll copies every result into a folder the user picks.
func (a *app) wfExportAll() {
	if a.wf == nil {
		return
	}
	svc, id := a.wfSvc, a.wf.ID
	run := func(dir string) {
		a.background(func() {
			files, err := svc.ExportAll(context.Background(), id, dir, "")
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Could not export: %v", err))
					return
				}
				a.toast(i18n.N(len(files), "Exported %d file.", "Exported %d files.", len(files)))
			})
		})
	}
	if wfExportDir != "" {
		run(wfExportDir)
		return
	}
	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("Export every result to a folder"), Directory: true}, run)
}

// wfExportDir replaces the folder dialogs (tests).
var wfExportDir string

// --- inputs ----------------------------------------------------------------------------

// wfScriptExts are the documents a Start step imports.
var wfScriptExts = []string{"txt", "md", "markdown", "docx", "epub"}

// wfAudioExts are the clips a Source step takes.
var wfAudioExts = []string{"wav", "mp3", "m4a", "aac", "aiff", "aif", "flac", "ogg", "caf", "mp4", "mov"}

// wfImportScripts asks for documents and adds each as a script.
func (a *app) wfImportScripts(node string) {
	a.openFiles(mygo.OpenDialogOptions{Title: i18n.T("Import scripts"), Multiple: true,
		Filters: fileTypes(i18n.T("Documents"), wfScriptExts...)}, func(paths []string) { a.wfScriptFiles(node, paths) })
}

// wfScriptFiles reads documents into a Start step's scripts, one each
// (on a goroutine: doc.Open reads files).
func (a *app) wfScriptFiles(node string, paths []string) {
	a.background(func() {
		var scripts []workflow.Script
		var failed []string
		for _, p := range paths {
			d, err := doc.Open(context.Background(), p, doc.Limits{MaxChars: 4 * workflow.MaxScriptChars})
			if err != nil {
				failed = append(failed, filepath.Base(p))
				continue
			}
			scripts = append(scripts, workflow.Script{Name: filepath.Base(p), Text: d.Text()})
		}
		a.post(func() {
			if len(failed) > 0 {
				a.toastWarn(i18n.T("Could not read %s.", strings.Join(failed, ", ")))
			}
			if len(scripts) == 0 || a.wfLocked() || a.wfNode(node) == nil {
				return
			}
			a.wfEdit(func(w *workflow.Workflow) {
				n := w.Node(node)
				// An empty first script gives way to the imports.
				if len(n.P.Scripts) == 1 && strings.TrimSpace(n.P.Scripts[0].Text) == "" {
					n.P.Scripts = nil
				}
				n.P.Scripts = append(n.P.Scripts, scripts...)
				if len(n.P.Scripts) > workflow.MaxScripts {
					n.P.Scripts = n.P.Scripts[:workflow.MaxScripts]
					a.toast(i18n.T("A Start step takes at most %d scripts.", workflow.MaxScripts))
				}
			})
		})
	})
}

// wfAddClips asks for audio files.
func (a *app) wfAddClips(node string) {
	a.openFiles(mygo.OpenDialogOptions{Title: i18n.T("Add audio"), Multiple: true,
		Filters: fileTypes(i18n.T("Audio"), wfAudioExts...)}, func(paths []string) { a.wfClipFiles(node, paths) })
}

// wfClipFiles adds audio files to a Source step (it stats them: small,
// local files, as the Dub page does on drop).
func (a *app) wfClipFiles(node string, paths []string) {
	if a.wfLocked() || a.wfNode(node) == nil {
		return
	}
	var clips []workflow.Clip
	var skipped []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(p)), ".")
		switch {
		case err != nil || fi.IsDir() || !slices.Contains(wfAudioExts, ext):
			skipped = append(skipped, filepath.Base(p))
		case fi.Size() > workflow.MaxClipBytes:
			skipped = append(skipped, filepath.Base(p))
			a.toast(i18n.T("%s is over 64 MiB.", filepath.Base(p)))
		default:
			clips = append(clips, workflow.Clip{Name: filepath.Base(p), Path: p, Size: fi.Size(), Mod: fi.ModTime()})
		}
	}
	if len(skipped) > 0 && len(clips) == 0 {
		a.toast(i18n.T("Add audio files: %s can't be used.", strings.Join(skipped, ", ")))
	}
	if len(clips) == 0 {
		return
	}
	a.wfEdit(func(w *workflow.Workflow) {
		n := w.Node(node)
		n.P.Clips = append(n.P.Clips, clips...)
		if len(n.P.Clips) > workflow.MaxClips {
			n.P.Clips = n.P.Clips[:workflow.MaxClips]
			a.toast(i18n.T("A Source clips step takes at most %d clips.", workflow.MaxClips))
		}
	})
}

// wfChooseFolder asks for an export step's folder.
func (a *app) wfChooseFolder(node string) {
	set := func(dir string) {
		a.wfEdit(func(w *workflow.Workflow) {
			if n := w.Node(node); n != nil {
				n.P.Folder = dir
			}
		})
	}
	if wfExportDir != "" {
		set(wfExportDir)
		return
	}
	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("Choose the export folder"), Directory: true}, set)
}
