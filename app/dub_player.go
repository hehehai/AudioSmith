package main

// The Dub page's player: A/B between the original audio and each dubbed
// language, keeping the playhead. A dubbed track plays as a live mix of
// the lines as they are rendered now (services.Dubs.TrackMixer, a
// mygo/audio Decoder over dsp/mix), so a regenerated line is heard
// before the track is mixed again. Its clock is the playhead of the
// timeline and of the video preview: a web view with a muted <video>
// served by the "asmedia" scheme (range requests), seeked to the audio
// clock when it drifts. Line auditions render one line through the take
// pipeline's preview.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/egoist/mygo"
	myaudio "github.com/egoist/mygo/audio"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubOrig is the A/B player's original track.
const dubOrig = ""

// dubPlayerState is the A/B player.
type dubPlayerState struct {
	track   string // dubOrig or a language
	p       *myaudio.Player
	closer  func() error
	project string
	pos     float64 // seconds, while nothing is loaded
	length  float64
	loading bool
}

// dubWAVDecoder plays a WAV file from any frame (the original's audio).
type dubWAVDecoder struct{ *dub.WAVSource }

func (d dubWAVDecoder) SampleRate() int               { r, _ := d.Format(); return r }
func (d dubWAVDecoder) Channels() int                 { _, c := d.Format(); return c }
func (d dubWAVDecoder) Len() int64                    { return d.Frames() }
func (d dubWAVDecoder) SetPosition(frame int64) error { return d.SeekFrame(frame) }

// dubPosition is the playhead, seconds.
func (a *app) dubPosition() float64 {
	pl := &a.dubPlayer
	if pl.p != nil {
		return pl.p.Position().Seconds()
	}
	return pl.pos
}

// dubPlaying reports whether the A/B player plays.
func (a *app) dubPlaying() bool { return a.dubPlayer.p != nil && a.dubPlayer.p.Playing() }

// dubStopPlayer stops and forgets the player (the position stays).
func (a *app) dubStopPlayer() {
	pl := &a.dubPlayer
	if pl.p != nil {
		pl.pos = pl.p.Position().Seconds()
		pl.p.Close()
	}
	if pl.closer != nil {
		pl.closer()
	}
	pl.p, pl.closer = nil, nil
	a.dubVideoSync(true)
}

// dubOpenTrack loads a track at the playhead, playing if play.
func (a *app) dubOpenTrack(track string, play bool) {
	p := a.dub
	if p == nil {
		return
	}
	pos := a.dubPosition()
	a.dubStopPlayer()
	pl := &a.dubPlayer
	pl.track, pl.project, pl.pos, pl.loading = track, p.ID, pos, true
	svc := a.dubSvc
	q := p.Clone()
	a.background(func() {
		var dec myaudio.Decoder
		var closer func() error
		var err error
		mixed := ""
		if track != dubOrig && svc.WorkGone(q) {
			mixed = svc.MixedTrack(q, track) // freed: the mix as made (dub_freed.go)
		}
		if track == dubOrig || mixed != "" {
			path := svc.Path(q.ID, q.Media.Audio)
			if mixed != "" {
				path = mixed
			} else if svc.WorkGone(q) && !fileExists(path) {
				a.post(func() { pl.loading = false; a.dubRebuild() })
				return
			}
			var w *dub.WAVSource
			if w, err = dub.OpenWAV(path, 0, 0); err == nil {
				dec, closer = dubWAVDecoder{w}, w.Close
			}
		} else {
			var m *dub.TrackMixer
			if m, err = svc.TrackMixer(q, track); err == nil {
				dec, closer = m, m.Close
			}
		}
		a.post(func() {
			pl.loading = false
			if err != nil {
				a.toast(i18n.T("Cannot play: %v", err))
				return
			}
			if a.dub == nil || a.dub.ID != q.ID || pl.track != track {
				closer()
				return
			}
			pl.p, pl.closer = myaudio.NewPlayer(dec), closer
			pl.length = float64(dec.Len()) / float64(max(dec.SampleRate(), 1))
			if pos > 0 {
				pl.p.Seek(time.Duration(pos * float64(time.Second)))
			}
			if play {
				a.pl.Stop() // one player at a time
				pl.p.Play()
			}
			a.dubVideoSync(true)
		})
	})
}

// dubTogglePlay plays or pauses the track chosen.
func (a *app) dubTogglePlay() {
	pl := &a.dubPlayer
	if pl.p == nil || pl.project != a.dub.ID {
		a.dubOpenTrack(pl.track, true)
		return
	}
	if pl.p.Playing() {
		pl.p.Pause()
	} else {
		a.pl.Stop()
		if pl.length > 0 && pl.p.Position().Seconds() >= pl.length-0.05 {
			pl.p.Seek(0)
		}
		pl.p.Play()
	}
	a.dubVideoSync(true)
}

// dubSwitchTrack switches A/B, keeping the playhead and the play state.
func (a *app) dubSwitchTrack(track string) {
	if a.dubPlayer.track == track && a.dubPlayer.p != nil {
		return
	}
	playing := a.dubPlaying()
	if a.dubPlayer.p == nil && !playing {
		a.dubPlayer.track = track
		return
	}
	a.dubOpenTrack(track, playing)
}

// dubSeek moves the playhead.
func (a *app) dubSeek(sec float64) {
	pl := &a.dubPlayer
	sec = max(sec, 0)
	if pl.p != nil {
		pl.p.Seek(time.Duration(sec * float64(time.Second)))
	} else {
		pl.pos = sec
	}
	a.dubVideoSync(true)
}

// dubPlayRange plays the original from a line's start (double-click on
// the timeline).
func (a *app) dubPlayRange(id string) {
	s, ok := a.dub.Segment(id)
	if !ok {
		return
	}
	a.dubSeek(s.Start)
	if !a.dubPlaying() {
		a.dubTogglePlay()
	}
}

// --- auditions ----------------------------------------------------------------------

// dubAuditionState is a line being rendered to be heard.
type dubAuditionState struct {
	seg    string
	job    string
	output string
	busy   bool
}

// dubAuditionLine renders one line as the dub would and plays it.
func (a *app) dubAuditionLine(id string) {
	p := a.dub
	if p == nil {
		return
	}
	if a.dubAudition.busy {
		a.toast(i18n.T("A line is being rendered: wait for it."))
		return
	}
	q, l := p.Clone(), a.dubLang
	// A conversation line already recorded plays as it is; one not yet is
	// heard on the dub's model, which is not what generating records.
	heard, inDialogue := a.dubDialogueHeard(id, l)
	if heard != "" {
		a.dubStopPlayer()
		if err := a.pl.Toggle("dubline:"+id, heard); err != nil {
			a.toast(i18n.T("Cannot play: %v", err))
		}
		return
	}
	if inDialogue {
		a.toast(i18n.T("This line is in a conversation: generating records it with the lines around it. Until then you hear it alone."))
	}
	svc, render := a.dubSvc, a.lib.svc.Render
	a.dubAudition = dubAuditionState{seg: id, busy: true}
	a.async(func() {
		take, warn, err := svc.LineRecipe(context.Background(), q, id, l)
		var job, out string
		if err == nil {
			job, out, err = render.Preview(context.Background(), take)
		}
		a.post(func() {
			if a.dubAudition.seg != id {
				return
			}
			if err != nil {
				a.dubAudition.busy = false
				var lic *core.LicenceRequiredError
				switch {
				case errors.As(err, &lic):
					a.askLicence(entryFor(lic.Model), func() { a.dubAuditionLine(id) })
				case errors.Is(err, core.ErrNotDownloaded):
					e := entryFor(take.Model)
					a.dubErr, a.dubNeed = i18n.T("%s is not downloaded yet.", modelRef(e)), &e
				default:
					a.toast(engineErrorText(err))
				}
				return
			}
			if warn != "" {
				a.toast(dubWarningText(warn))
			}
			a.dubAudition.job, a.dubAudition.output = job, out
		})
	})
}

// dubAuditionUpdated follows the audition's preview job.
func (a *app) dubAuditionUpdated(j jobs.Job) {
	au := &a.dubAudition
	if au.job == "" || j.ID != au.job {
		return
	}
	switch {
	case j.State == jobs.StateSucceeded:
		au.busy = false
		a.dubStopPlayer()
		if err := a.pl.Toggle("dubline:"+au.seg, au.output); err != nil {
			a.toast(i18n.T("Cannot play: %v", err))
		}
	case j.State.Terminal() || j.State == jobs.StateInterrupted:
		au.busy = false
		if j.State == jobs.StateFailed {
			a.toast(jobErrorText(j.Err))
		}
	}
}

// --- the video preview ------------------------------------------------------------------

// dubVideoState is the video preview.
type dubVideoState struct {
	wv       *mygo.WebView
	project  string
	loaded   string // what the page shows: the project's video, proxy and poster
	lastSync time.Time
	show     bool
}

// dubMediaScheme serves a dub's media to its preview: asmedia://localhost/<id>/video|poster.
const dubMediaScheme = "asmedia"

// dubMediaHandler serves the preview's files, with range requests.
func dubMediaHandler(svc func() *services.Dubs) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(path.Clean(r.URL.Path), "/"), "/")
		s := svc()
		if len(parts) != 2 || s == nil {
			http.NotFound(w, r)
			return
		}
		p, err := s.Get(r.Context(), parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var file string
		switch parts[1] {
		case "video":
			file = p.Source.Path
			if p.Media.Proxy != "" {
				file = s.Path(p.ID, p.Media.Proxy)
			}
		case "poster": // the file's frame, else the site's cover (#106)
			file = s.Path(p.ID, p.Media.Poster)
			if m := p.Source.Meta; file == "" && m != nil {
				file = s.Path(p.ID, m.Thumbnail)
			}
		}
		if file == "" {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	})
}

// dubVideoHTML is the preview's page: a muted video the app seeks.
func dubVideoHTML(id string, poster bool) string {
	p := ""
	if poster {
		p = fmt.Sprintf(` poster="%s://localhost/%s/poster"`, dubMediaScheme, id)
	}
	return `<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0;height:100%;background:#000}video{width:100%;height:100%;object-fit:contain}</style></head><body>` +
		fmt.Sprintf(`<video id="v" muted playsinline preload="auto"%s src="%s://localhost/%s/video"></video>`, p, dubMediaScheme, id) +
		`<script>const v=document.getElementById('v');window.asSync=function(t,play){if(Math.abs(v.currentTime-t)>0.15){v.currentTime=t}if(play&&v.paused){v.play().catch(()=>{})}if(!play&&!v.paused){v.pause()}}</script></body></html>`
}

// dubEnsureVideo makes the preview's web view for the open project once
// its video is on disk and playable (in a window only; tests show the
// poster), and loads it again when the file or its poster changed.
func (a *app) dubEnsureVideo() {
	v := &a.dubVideo
	p := a.dub
	if a.win == nil || p == nil || !dubVideoReady(p) {
		return
	}
	if v.wv == nil {
		wv, err := a.win.NewWebView(mygo.WebViewOptions{BackgroundColor: "#000000"})
		if err != nil {
			return
		}
		v.wv = wv
	}
	// Again when the file changes (downloaded, then its preview copy) or
	// its poster comes.
	poster := p.Media.Poster != "" || p.Source.Meta != nil && p.Source.Meta.Thumbnail != ""
	key := p.ID + "\x00" + p.Source.Path + "\x00" + p.Media.Proxy + "\x00" + p.Media.Poster + fmt.Sprint(poster)
	if v.project != p.ID || v.loaded != key {
		v.project, v.loaded = p.ID, key
		v.wv.Page().LoadHTML(dubVideoHTML(p.ID, poster), dubMediaScheme+"://localhost/")
	}
}

// dubVideoSync seeks the video to the audio clock (at most every 250 ms
// unless forced), on a goroutine: Eval waits for the page.
func (a *app) dubVideoSync(force bool) {
	v := &a.dubVideo
	if v.wv == nil || a.dub == nil || v.project != a.dub.ID {
		return
	}
	if !force && time.Since(v.lastSync) < 250*time.Millisecond {
		return
	}
	v.lastSync = time.Now()
	t, playing := a.dubPosition(), a.dubPlaying()
	page := v.wv.Page()
	go page.Eval(fmt.Sprintf("window.asSync&&window.asSync(%.3f,%v)", t, playing))
}

// --- helpers ---------------------------------------------------------------------------

// readWAVMono reads a WAV file mixed down to mono.
func readWAVMono(path string) (dsp.Buffer, error) {
	b, _, err := wav.ReadFile(path)
	if err != nil {
		return b, err
	}
	return dsp.Remix(b, 1), nil
}

// bgCtx is the context of background work started by the UI.
func bgCtx() context.Context { return context.Background() }

// dubJSON decodes JSON, ignoring errors (job data).
func dubJSON(data []byte, v any) { json.Unmarshal(data, v) }

// dubRound rounds to hundredths (time fields).
func dubRound(x float64) float64 { return math.Round(x*100) / 100 }
