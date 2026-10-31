package main

// Dubbing's sources: choose, drop or import a URL (and its cookies.txt),
// remove, and the waveform peaks; split from dub.go and dub_view.go.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// dubExts are the files the Dub page takes.
var dubExts = dub.MediaExts

// chooseDubSource asks for a video or audio file: the composer holds it
// (on the list), or it becomes the open dub's source (a dub without one).
func (a *app) chooseDubSource() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Choose a video or audio file"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Video or audio"), Extensions: dubExts}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			if a.router.Path() == "/dub" {
				a.dubPend = paths[0]
				return
			}
			a.dubSourceFile(paths[0])
		})
	}()
}

// dubDropped takes the first video or audio file dropped.
func (a *app) dubDropped(paths []string) {
	for _, p := range paths {
		if dub.IsMedia(p, dubExts) {
			a.dubSourceFile(p)
			return
		}
	}
	a.toast(i18n.T("Drop a video or audio file."))
}

// dubSourceFile starts a dub of a file, or replaces the source of the
// open one when it has none.
func (a *app) dubSourceFile(path string) {
	src := dub.Source{Kind: dub.SourceFile, Path: path, Name: filepath.Base(path)}
	if fi, err := os.Stat(path); err == nil {
		src.Size = fi.Size()
	}
	if a.dub != nil && a.dub.Source.Path == "" && a.dub.Source.URL == "" {
		a.dubSet(func(p *dub.Project) {
			p.Source = src
			p.Title = strings.TrimSuffix(src.Name, filepath.Ext(src.Name))
		})
		a.dubAfterSaves(a.dub.ID, a.dubProbe)
		return
	}
	a.newDub(src)
}

// dubImportURL starts a dub of a URL (yt-dlp).
func (a *app) dubImportURL() {
	u := strings.TrimSpace(a.dubURL)
	if _, err := dub.CheckURL(context.Background(), u, nil); err != nil {
		a.dubErr = dubURLErrorText(err)
		return
	}
	if !a.prefs.Dubbing.URLImport || a.prefs.Dubbing.YtDlp == "" {
		a.dubErr, a.dubErrCode = i18n.T("Importing from a URL is off. Turn it on in Settings → Dubbing (it downloads yt-dlp, about 35 MB)."), dub.CodeNoYtDlp
		return
	}
	a.dubErr = ""
	a.newDub(dub.Source{Kind: dub.SourceURL, URL: u, Name: u, WantCaptions: a.dubCaptions})
	a.dubURL = ""
}

// dubProbe reads what project id's source is (Dubs.Probe: a link's
// details and cover, a file's length, picture and poster) in the
// background; the page shows it as it is saved.
func (a *app) dubProbe(id string) {
	if a.dubSvc == nil || a.dubProbes[id] {
		return
	}
	if a.dubProbes == nil {
		a.dubProbes = map[string]bool{}
	}
	a.dubProbes[id] = true
	svc := a.dubSvc
	a.async(func() {
		svc.Probe(context.Background(), id)
		a.post(func() { delete(a.dubProbes, id) })
	})
}

// dubProbing reports whether the open project's source is being read.
func (a *app) dubProbing() bool { return a.dub != nil && a.dubProbes[a.dub.ID] }

// dubAfterSaves runs fn(id) on the main thread once the saves queued so
// far are done.
func (a *app) dubAfterSaves(id string, fn func(string)) {
	if a.dubSaveQ == nil {
		fn(id)
		return
	}
	a.dubSaveQ.push(func() { a.post(func() { fn(id) }) })
}

// dubChooseCookies picks a cookies.txt for the next URL import.
func (a *app) dubChooseCookies() {
	go func() {
		paths, err := dialogOpen(a, i18n.T("Choose a cookies.txt export"), "txt")
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.dubCookies = paths[0] })
	}()
}

// dubRemoveSource clears the open project's source and everything made
// from it (it asks first when there is work to lose).
func (a *app) dubRemoveSource() {
	if a.dub == nil {
		return
	}
	a.dubStopPlayer()
	a.dubSet(func(p *dub.Project) {
		p.Source, p.Segments, p.Tracks, p.Stages, p.References, p.RefTried, p.Media, p.Run, p.Detected = dub.Source{}, nil, nil, nil, nil, nil, dub.Media{}, nil, ""
	})
	a.dubHist.Reset()
	a.dubStep = dubStepSource
	a.dubPeaks, a.dubPeaksFor = nil, ""
}

// dubLoadPeaks reads the open project's waveform in the background.
func (a *app) dubLoadPeaks() {
	p := a.dub
	if p == nil || p.Media.Peaks == "" {
		return
	}
	path := a.dubSvc.Path(p.ID, p.Media.Peaks)
	id, name := p.ID, p.Media.Peaks
	a.dubPeaksFor = name
	a.background(func() {
		pk, err := dub.ReadPeaks(path)
		a.post(func() {
			if err == nil && a.dub != nil && a.dub.ID == id {
				a.dubPeaks = pk
			}
		})
	})
}

// ytdlpProxy is the --proxy yt-dlp gets for a proxy mode of Settings →
// Models: the proxy named, or with "" (automatic) the one the environment
// or the macOS system proxy gives for the web; "" for none. yt-dlp itself
// only reads the environment, which apps started from the Finder lack.
func ytdlpProxy(mode string) string {
	if mode == hub.ProxyDirect {
		return ""
	}
	req, _ := http.NewRequest(http.MethodGet, "https://www.youtube.com/", nil)
	if u, err := hub.ProxyFunc(mode)(req); err == nil && u != nil {
		return u.String()
	}
	return ""
}
