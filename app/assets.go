package main

// Assets (/assets, 0.5 F5; History before): every output and imported
// file in one list — Text to speech's takes, the stories' and books'
// renders, the files exported (books, chapters, dubs, takes), the
// isolations' stems, the recordings transcribed and the media dubbed or
// isolated — with a type filter, a source filter and the search. A row
// plays inline, names the tool it came from (a click opens it) and its ⋯
// exports, shows in Finder, sends the file (or a take's script) to
// another tool, or deletes it. assets_view.go draws the page.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// assetKind is what an asset is (the type filter).
type assetKind string

const (
	assetSpeech  assetKind = "speech"
	assetStory   assetKind = "story"
	assetBook    assetKind = "audiobook"
	assetDub     assetKind = "dub"
	assetIsolate assetKind = "isolate"
	assetImport  assetKind = "import"
)

// assetKinds are the type filter's kinds, in order ("" all).
var assetKinds = []assetKind{"", assetSpeech, assetStory, assetBook, assetDub, assetIsolate, assetImport}

// assetSources are the source filter's tools, in order ("" all).
var assetSources = []string{"", "/studio", "/stories", "/audiobook", "/dub", "/isolate", "/transcripts"}

// assetKindLabel names a kind.
func assetKindLabel(k assetKind) string {
	switch k {
	case assetSpeech:
		return i18n.X("asset kind", "Speech")
	case assetStory:
		return i18n.T("Story")
	case assetBook:
		return i18n.T("Audiobook")
	case assetDub:
		return i18n.X("project kind", "Dub")
	case assetIsolate:
		return i18n.X("asset kind", "Stems")
	case assetImport:
		return i18n.X("asset kind", "Imported")
	}
	return i18n.T("All types")
}

// assetSourceLabel names a source tool.
func assetSourceLabel(tool string) string {
	if tool == "" {
		return i18n.T("All sources")
	}
	return pageTitle(tool)
}

// asset is a row of Assets.
type asset struct {
	key       string
	kind      assetKind
	title     string
	sub       string // its file or what it is
	path      string // the file ("" none)
	dur       float64
	at        time.Time
	tool      string // the tool it came from (its page)
	open      func() // opens it in its tool
	take      *core.Take
	text      string // a script to read in Text to speech ("" none)
	studio    func() // reads it in Text to speech otherwise (a transcript)
	render    string // a render's id ("How it was made")
	del       func() // deletes it; nil when it cannot be here
	delRecord bool   // del removes the record only (an export: the file stays)
}

// assetAudioExts are the files the row plays.
var assetAudioExts = []string{"wav", "mp3", "m4a", "m4b", "aac", "flac", "ogg", "opus", "caf", "aiff"}

func assetPlayable(path string) bool {
	return path != "" && slices.Contains(assetAudioExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")))
}

// refreshAssetExports reads the export records when they changed.
func (a *app) refreshAssetExports() {
	h := &a.history
	if h.exLoading || (h.exLoaded && !h.dirty) || a.lib == nil || a.lib.svc.Library == nil {
		return
	}
	h.exLoading, h.dirty = true, false
	lib := a.lib.svc.Library
	a.background(func() {
		ex, err := lib.Exports(context.Background(), "", 1000)
		a.post(func() {
			h.exLoading = false
			if err == nil {
				h.exports, h.exLoaded = ex, true
			}
		})
	})
}

// assets are every asset, newest first, before the filters.
func (a *app) assets() []asset {
	a.refreshAssetExports()
	var out []asset
	for i := range a.takes {
		tk := &a.takes[i]
		sub := modelName(tk.Model)
		if tk.Voice != "" {
			sub += " · " + tk.Voice
		}
		out = append(out, asset{key: "take:" + tk.ID, kind: assetSpeech, title: oneLine(tk.Text), sub: sub,
			path: a.takeStore.AudioPath(*tk), dur: tk.AudioSeconds, at: tk.CreatedAt, tool: "/studio", take: tk, text: tk.Text,
			open: func() { a.reuse(tk) }, del: func() { a.deleteClip(tk) }})
	}
	for _, id := range a.lfRecent {
		r := a.lfRenders[id]
		if r == nil || r.rec.Audition || r.rec.State != core.RenderDone || r.body.Output == nil || r.body.Output.Book == "" {
			continue
		}
		kind, tool := assetStory, "/stories"
		if r.rec.Kind == core.ProjectAudiobook {
			kind, tool = assetBook, "/audiobook"
		}
		rec := r.rec
		out = append(out, asset{key: "render:" + rec.ID, kind: kind, title: firstOf(rec.Title, i18n.T("Untitled")), sub: a.renderRecipe(r),
			path: r.body.Output.Book, dur: rec.AudioSeconds, at: rec.UpdatedAt, tool: tool, render: rec.ID,
			open: func() { a.openRenderAsset(rec) }, del: func() { a.deleteRenderAsset(rec.ID) }})
	}
	for _, e := range a.history.exports {
		if as, ok := a.exportAsset(e); ok {
			out = append(out, as)
		}
	}
	for _, x := range a.isoList {
		x := x
		openIso := func() { a.navigate("/isolate/" + x.ID) }
		for _, s := range isoStems(x) {
			out = append(out, asset{key: "stem:" + x.ID + ":" + s.key, kind: assetIsolate, title: x.Title + " · " + s.name, sub: s.about,
				path: a.isoSvc.Path(x.ID, s.file), dur: x.Duration, at: x.UpdatedAt, tool: "/isolate", open: openIso})
		}
		if x.Input != "" {
			out = append(out, asset{key: "in:iso:" + x.ID, kind: assetImport, title: filepath.Base(x.Input), sub: shortPath(x.Input),
				path: x.Input, dur: x.Duration, at: x.CreatedAt, tool: "/isolate", open: openIso})
		}
	}
	for _, tr := range a.trList {
		if tr.Source == core.SourceDictation || a.trSvc == nil {
			continue
		}
		id := tr.ID
		path := tr.SourcePath
		if tr.Audio != "" {
			path = a.trSvc.AudioPath(tr)
		}
		if path == "" {
			continue
		}
		out = append(out, asset{key: "in:tr:" + id, kind: assetImport, title: transcriptTitle(tr), sub: trFirstWords(tr), path: path,
			dur: tr.Duration, at: tr.CreatedAt, tool: "/transcripts", open: func() { a.selectTranscript(id) }, studio: func() { a.trToStudio(id) }})
	}
	for _, r := range a.dubList {
		if r.Source == "" || dubSourceLabel(r.Source) != filepath.Base(r.Source) { // a link: no file here
			continue
		}
		id := r.ID
		out = append(out, asset{key: "in:dub:" + id, kind: assetImport, title: filepath.Base(r.Source), sub: shortPath(r.Source),
			path: r.Source, dur: r.Duration, at: r.CreatedAt, tool: "/dub", open: func() { a.openDub(id) }})
	}
	slices.SortStableFunc(out, func(x, y asset) int { return y.at.Compare(x.at) })
	return out
}

// exportAsset is an export record as an asset: its file, filed under
// what it was exported from; false for what is not audio or video (a
// voice, captions, a transcript's text).
func (a *app) exportAsset(e core.Export) (asset, bool) {
	kind, tool := assetSpeech, "/studio"
	switch e.Kind {
	case "take":
	case "dub":
		kind, tool = assetDub, "/dub"
	case "book", "chapter", "stem":
		kind, tool = assetBook, "/audiobook"
		if r := a.lfRenders[e.SourceID]; r != nil && r.rec.Kind == core.ProjectStory {
			kind, tool = assetStory, "/stories"
		}
	default:
		return asset{}, false
	}
	id := e.ID
	as := asset{key: "export:" + id, kind: kind, title: firstOf(e.Title, filepath.Base(e.Path)), sub: i18n.T("Exported · %s", shortPath(e.Path)),
		path: e.Path, at: e.CreatedAt, tool: tool, delRecord: true, del: func() { a.deleteExportRecord(id) }}
	switch kind {
	case assetDub:
		src := e.SourceID
		as.open = func() { a.openDub(src) }
	case assetSpeech:
		if tk := a.findTake(e.SourceID); tk != nil {
			as.open, as.text = func() { a.reuse(tk) }, tk.Text
		}
	default:
		if e.ProjectID != "" {
			kind := projStory
			if as.kind == assetBook {
				kind = projBook
			}
			pid := e.ProjectID
			as.open = func() { a.openProj(projItem{kind: kind, id: pid, title: e.Title}) }
		}
	}
	if as.open == nil {
		as.open = func() { a.navigate(tool) }
	}
	return as, true
}

// oneLine is s on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// openRenderAsset opens a render's project (its page shows the render).
func (a *app) openRenderAsset(rec core.Render) {
	kind := projStory
	if rec.Kind == core.ProjectAudiobook {
		kind = projBook
	}
	if rec.ProjectID != "" {
		a.openProj(projItem{kind: kind, id: rec.ProjectID, title: rec.Title})
		return
	}
	p := a.lfPageOf(rec.Kind)
	p.render, p.opened, p.listing = rec.ID, true, false
	a.navigate("/" + lfRoute(rec.Kind))
}

// deleteRenderAsset deletes a render (its files too).
func (a *app) deleteRenderAsset(id string) {
	a.pl.Stop()
	svc := a.lib.svc.Longform
	a.background(func() {
		err := svc.Delete(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not delete: %v", err))
				return
			}
			delete(a.lfRenders, id)
			a.lfRecent = slices.DeleteFunc(a.lfRecent, func(x string) bool { return x == id })
		})
	})
}

// deleteExportRecord removes an export from the list (the file stays).
func (a *app) deleteExportRecord(id string) {
	lib := a.lib.svc.Library
	a.background(func() {
		err := lib.DeleteExport(context.Background(), id)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not delete: %v", err))
			}
			a.history.dirty = true
		})
	})
}

// playAsset plays or pauses an asset.
func (a *app) playAsset(as asset) {
	if as.take != nil {
		a.play(as.take)
		return
	}
	if err := a.pl.Toggle(as.key, as.path); err != nil {
		a.toastWarn(i18n.T("Can't play: %v", err))
	}
}

// assetPlaying reports whether an asset plays.
func (a *app) assetPlaying(as asset) bool {
	if as.take != nil {
		return a.pl.Playing(as.take.ID)
	}
	return a.pl.Playing(as.key)
}

// sendAsset hands an asset's file to a tool's composer (Speech to text,
// Dub, Voice isolator), or its script to Text to speech.
func (a *app) sendAsset(as asset, tool string) {
	if tool != "/studio" && !fileExists(as.path) {
		a.toast(i18n.T("The file is gone: %s", as.path))
		return
	}
	switch tool {
	case "/transcripts":
		a.trPend = as.path
	case "/dub":
		a.dubPend = as.path
	case "/isolate":
		a.isoPend = as.path
	case "/studio":
		if as.studio != nil {
			as.studio()
		} else {
			a.homeToStudio(as.text)
		}
		return
	}
	a.navigate(tool)
}

// saveAssetCopy saves a copy of an asset's file where the user chooses
// (a take has its Export… dialog).
func (a *app) saveAssetCopy(as asset) {
	if as.take != nil {
		a.openExport(as.take)
		return
	}
	name := filepath.Base(as.path)
	save := func(dst string) {
		a.background(func() {
			err := copyFile(as.path, dst)
			a.post(func() {
				if err != nil {
					a.toastWarn(i18n.T("Export failed: %v", err))
				} else {
					a.toast(i18n.T("Saved %s", filepath.Base(dst)))
				}
			})
		})
	}
	if a.win == nil {
		if isoSaveDir != "" {
			save(filepath.Join(isoSaveDir, name))
		}
		return
	}
	go func() {
		dst, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save audio as"), DefaultPath: name})
		if err != nil || dst == "" {
			return
		}
		a.post(func() { save(dst) })
	}()
}

// assetIcon is a kind's icon.
func assetIcon(k assetKind) icons.Icon {
	switch k {
	case assetStory:
		return storiesIcon
	case assetBook:
		return audiobookIcon
	case assetDub:
		return dubIcon
	case assetIsolate:
		return isolateIcon
	case assetImport:
		return icons.Import
	}
	return studioIcon
}
