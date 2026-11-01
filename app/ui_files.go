package main

// Shared file dialogs (cleanup #140). The open and save panels block, so
// they run on a goroutine of their own; the choice comes back on the UI
// thread (a.post). A cancel or an error does nothing. Parent defaults to
// the window.
//
//	a.openFile(mygo.OpenDialogOptions{Title: i18n.T("Reference audio"),
//		Filters: fileTypes(i18n.T("Audio"), "wav", "mp3")}, func(p string) { … })
//	a.saveFileOr(myTestDir, mygo.SaveDialogOptions{Title: …, DefaultPath: name},
//		func(dst string) { a.writeInBackground(dst, func(dst string) error { … }) })

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// fileTypes is a dialog filter of one entry: name over exts (no dots).
func fileTypes(name string, exts ...string) []mygo.FileFilter {
	return []mygo.FileFilter{{Name: name, Extensions: exts}}
}

// openFile asks for a file (or a folder, with o.Directory) and hands the
// first one chosen to fn.
func (a *app) openFile(o mygo.OpenDialogOptions, fn func(path string)) {
	a.openFiles(o, func(paths []string) { fn(paths[0]) })
}

// openFiles asks for files (set o.Multiple for more than one) and hands
// those chosen to fn; fn is not called when none is.
func (a *app) openFiles(o mygo.OpenDialogOptions, fn func(paths []string)) {
	if o.Parent == nil {
		o.Parent = a.win
	}
	go func() {
		paths, err := mygo.Dialog.Open(o)
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { fn(paths) })
	}()
}

// saveFile asks where to save and hands the path to fn.
func (a *app) saveFile(o mygo.SaveDialogOptions, fn func(dst string)) {
	if o.Parent == nil {
		o.Parent = a.win
	}
	go func() {
		dst, err := mygo.Dialog.Save(o)
		if err != nil || dst == "" {
			return
		}
		a.post(func() { fn(dst) })
	}()
}

// saveFileOr is saveFile with a test seam: without a window (tests) no
// dialog shows and fn gets testDir/o.DefaultPath at once, or is not
// called when testDir is "".
func (a *app) saveFileOr(testDir string, o mygo.SaveDialogOptions, fn func(dst string)) {
	if a.win == nil {
		if testDir != "" {
			fn(filepath.Join(testDir, o.DefaultPath))
		}
		return
	}
	a.saveFile(o, fn)
}

// writeInBackground runs write(dst) in the background, then toasts "Saved <name>"
// or the error.
func (a *app) writeInBackground(dst string, write func(dst string) error) {
	a.background(func() {
		err := write(dst)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Export failed: %v", err))
			} else {
				a.toast(i18n.T("Saved %s", filepath.Base(dst)))
			}
		})
	})
}

// exportCopy saves a copy of src where the user chooses, proposing name
// (in tests: into isoSaveDir). With filters, a path typed without an
// extension gets the first filter's first one.
func (a *app) exportCopy(src, name string, filters []mygo.FileFilter) {
	o := mygo.SaveDialogOptions{Title: i18n.T("Save audio as"), DefaultPath: name, Filters: filters}
	a.saveFileOr(isoSaveDir, o, func(dst string) {
		if len(filters) > 0 && len(filters[0].Extensions) > 0 && filepath.Ext(dst) == "" {
			dst += "." + filters[0].Extensions[0]
		}
		a.writeInBackground(dst, func(dst string) error { return copyFile(src, dst) })
	})
}
