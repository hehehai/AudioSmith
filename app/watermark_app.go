package main

// The invisible watermark (AudioSeal; engine watermark.embed / detect on
// the media worker, docs/protocol-v2.md §5.5):
//
//   - every finished render is marked in place (services.Config.Watermark:
//     takes, and so dub lines, workflow Speak steps and Local API speech,
//     which render through the take pipeline; longform chapters) unless
//     Settings → Privacy turns it off (settings.Privacy.NoWatermark). Without
//     the model downloaded the renders stay unmarked and the app says so
//     once per run;
//   - the message is watermark.Message(install id): AudioSmith's marker byte
//     and a random install id (settings.Privacy.WatermarkID, chosen once);
//   - Tools → Watermark and the Local API (POST /api/watermark/detect)
//     check a file for it.
//
// The engine loads safetensors the app converts once from the .pth
// checkpoints of facebook/audioseal (internal/watermark.Prepare), next to
// the model cache.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/watermark"
)

// watermarkState is the library's side: the engine's executors, and the
// one-time notice.
type watermarkState struct {
	mu      sync.Mutex
	exec    func(method string) jobs.Executor // nil until the engine connects
	missing func()                            // called once per run when the model is not downloaded
	warned  bool
	prepMu  sync.Mutex // one conversion at a time
}

// useWatermark connects the watermark to the engine (exec returns the
// executor of a watermark method, or nil while there is none) and to the
// app's notice.
func (l *library) useWatermark(exec func(method string) jobs.Executor, missing func()) {
	l.wm.mu.Lock()
	l.wm.exec, l.wm.missing = exec, missing
	l.wm.mu.Unlock()
}

func (l *library) watermarkExec(method string) jobs.Executor {
	l.wm.mu.Lock()
	fn := l.wm.exec
	l.wm.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(method)
}

// watermarkModel returns the catalogue entry and the converted model
// folder; core.ErrNotDownloaded when the model is not downloaded.
func (l *library) watermarkModel(ctx context.Context) (catalog.Entry, string, error) {
	e, ok := catalog.Builtin().ByID(catalog.DefaultWatermark)
	if !ok {
		return e, "", fmt.Errorf("no watermark model: %w", core.ErrUnsupported)
	}
	snap, err := l.modelSnapshot(ctx, e.Repo)
	if err != nil {
		return e, "", err
	}
	l.wm.prepMu.Lock()
	defer l.wm.prepMu.Unlock()
	dir, err := watermark.Prepare(snap, watermark.ConvertedDir(modelCache(l.settings.Get()), e.Repo, e.Revision))
	return e, dir, err
}

// watermarkInstall is this install's id (1–255), chosen at random the
// first time and kept in the settings.
func (l *library) watermarkInstall(ctx context.Context) uint8 {
	if id := l.settings.Get().Privacy.WatermarkID; id > 0 && id < 256 {
		return uint8(id)
	}
	id := 1 + rand.IntN(255)
	l.settings.Update(ctx, func(s *settings.Settings) {
		if s.Privacy.WatermarkID <= 0 || s.Privacy.WatermarkID > 255 {
			s.Privacy.WatermarkID = id
		}
	})
	return uint8(l.settings.Get().Privacy.WatermarkID)
}

// watermarkRender is services.Config.Watermark: it marks the WAV at path in
// place. Off, no model or no engine: false without error.
func (l *library) watermarkRender(ctx context.Context, path string, bits int) (bool, error) {
	if l.settings.Get().Privacy.NoWatermark {
		return false, nil
	}
	e, dir, err := l.watermarkModel(ctx)
	if errors.Is(err, core.ErrNotDownloaded) {
		l.wm.mu.Lock()
		missing, warned := l.wm.missing, l.wm.warned
		l.wm.warned = true
		l.wm.mu.Unlock()
		if !warned && missing != nil {
			missing()
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ex := l.watermarkExec(proto.MethodWatermarkEmbed)
	if ex == nil {
		return false, nil
	}
	p := proto.WatermarkEmbedParams{Model: e.Repo, ModelPath: dir, Input: path, Output: proto.Output{Path: path, Bits: bits},
		Message: watermark.Message(l.watermarkInstall(ctx))}
	raw, _ := json.Marshal(p)
	_, err = ex.Run(ctx, jobs.Job{ID: core.NewID(time.Now()), Spec: jobs.Spec{Kind: proto.MethodWatermarkEmbed, Params: raw}}, func(jobs.Progress) {}, nil)
	return err == nil, err
}

// detectWatermark checks a file for the watermark (Tools, the Local API).
func (l *library) detectWatermark(ctx context.Context, path string) (proto.WatermarkDetectResult, error) {
	e, dir, err := l.watermarkModel(ctx)
	if err != nil {
		return proto.WatermarkDetectResult{}, err
	}
	ex := l.watermarkExec(proto.MethodWatermarkDetect)
	if ex == nil {
		return proto.WatermarkDetectResult{}, fmt.Errorf("the engine is not running: %w", core.ErrEngine)
	}
	raw, _ := json.Marshal(proto.WatermarkDetectParams{Model: e.Repo, ModelPath: dir, Input: path})
	res, err := ex.Run(ctx, jobs.Job{ID: core.NewID(time.Now()), Spec: jobs.Spec{Kind: proto.MethodWatermarkDetect, Params: raw}}, func(jobs.Progress) {}, nil)
	if err != nil {
		return proto.WatermarkDetectResult{}, err
	}
	var r proto.WatermarkDetectResult
	err = json.Unmarshal(res, &r)
	return r, err
}

// openWatermark connects the library's watermark to the engine and the
// notices (once, from newApp).
func (a *app) openWatermark(lib *library) {
	host := a.eng
	lib.useWatermark(func(method string) jobs.Executor {
		if a.wmEngine != nil {
			return a.wmEngine(method)
		}
		if host == nil || host.sup == nil {
			return nil
		}
		return engine.Executor(host.sup, proto.RoleOf(method), method)
	}, func() {
		a.post(func() {
			a.notify(core.SeverityInfo, i18n.T("Generated audio is not watermarked"),
				i18n.T("Download the watermark model in Settings → Models & network to add the invisible watermark, or turn it off in Settings → Privacy."), "/models")
		})
	})
}

// watermarkUI is the state of Settings → Privacy and Tools → Watermark.
type watermarkUI struct {
	// wmEngine replaces the engine's watermark executors in tests.
	wmEngine func(method string) jobs.Executor
	wmOn     bool // the Privacy switch
	wmCheck  struct {
		file string
		busy bool
		res  *proto.WatermarkDetectResult
		err  string
	}
}

// watermarkEntry is the watermark model's entry and what is on disk.
func (a *app) watermarkEntry() (catalog.Entry, *modelState) {
	e, _ := catalog.Builtin().ByID(catalog.DefaultWatermark)
	return e, a.model(e)
}

// watermarkSettings is the watermark section of Settings → Privacy.
func (a *app) watermarkSettings(c *ui.Context) { // M7a: a section of Settings → Privacy (settings_privacy.go)
	t := c.Theme()
	a.wmOn = !a.prefs.Privacy.NoWatermark
	e, m := a.watermarkEntry()
	if !m.known && !m.downloading {
		m.known = true // read once
		a.refreshModel(e)
	}
	desc := i18n.T("Every take, chapter, dub and API output gets a mark you cannot hear, so it can be recognised as AudioSmith's. It survives compression and volume changes.")
	settingRow(c, "privacy-watermark", i18n.T("Add an invisible watermark to generated audio"), desc, func() {
		if inkSwitch(c.Key("privacy-watermark-switch"), &a.wmOn).Label(i18n.T("Add an invisible watermark to generated audio")).Changed() {
			on := a.wmOn
			a.updateSettings(func(s *settings.Settings) { s.Privacy.NoWatermark = !on })
		}
	})
	status := ""
	switch {
	case m.downloading:
		status = i18n.T("Downloading the watermark model…")
	case m.local.State == hub.Downloaded:
		status = i18n.T("The watermark model is downloaded.")
		if id := a.prefs.Privacy.WatermarkID; id > 0 {
			status = i18n.T("The watermark model is downloaded. This Mac's mark: AudioSmith, install %d.", id)
		}
	default:
		status = i18n.T("The watermark model (%s) is not downloaded: generated audio is not watermarked.", mb(e.Size))
	}
	settingFoot(c, "privacy-watermark-foot", func() {
		ui.Row(c.Key("privacy-watermark-model")).Gap(px(t, groupCtrlGap)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, status).FontSize(px(t, 12)).TextColor(t.TextMuted).Grow(1).Wrap()
			if m.local.State != hub.Downloaded && !m.downloading {
				if secondaryButton(c.Key("privacy-watermark-download"), i18n.T("Download")).Clicked() {
					a.download(e)
				}
			}
		})
	})
}

// watermarkTool is Tools → Watermark.
func (a *app) watermarkTool(c *ui.Context) {
	t := c.Theme()
	w := &a.wmCheck
	toolIntro(c, i18n.T("Is a file watermarked? Drop it here or choose it: the watermark model finds its mark, and whether AudioSmith made it."))
	zone := ui.Column(c.Key("wm-drop")).Padding(px(t, 12)).Gap(px(t, 6)).Radius(8).Border(1, t.Border)
	if files := zone.DroppedFiles(); len(files) > 0 && !w.busy {
		a.checkWatermark(files[0])
	}
	if zone.FileDragOver() {
		zone.Border(2, t.Accent)
	}
	zone.Children(func() {
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
			name := w.file
			if name == "" {
				name = i18n.T("No file chosen")
			}
			ui.Text(c, name).Grow(1).SingleLine()
			if secondaryButton(c.Key("wm-choose"), i18n.T("Choose…")).Disabled(w.busy).Clicked() {
				go func() {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("An audio or video file")})
					if err != nil || len(paths) == 0 {
						return
					}
					a.post(func() { a.checkWatermark(paths[0]) })
				}()
			}
		})
	})
	if w.busy {
		ui.Spinner(c)
	}
	if w.err != "" {
		ui.Text(c.Key("wm-err"), w.err).TextColor(t.Danger).Wrap()
	}
	if r := w.res; r != nil {
		for i, line := range watermarkVerdict(*r) {
			txt := ui.Text(c.Key(fmt.Sprintf("wm-line-%d", i)), line).Wrap()
			if i == 0 {
				txt.Bold()
			}
		}
	}
}

// watermarkVerdict explains a detection: the verdict first.
func watermarkVerdict(r proto.WatermarkDetectResult) []string {
	pct := int(r.Probability*100 + 0.5)
	if !r.Detected {
		return []string{i18n.T("No watermark found (%d%% of the audio looks marked).", pct)}
	}
	out := []string{i18n.T("Watermarked: %d%% of the audio carries the mark.", pct)}
	if ours, id := watermark.Decode(r.Message); ours && r.Confidence >= 0.2 {
		out = append(out, i18n.T("Made with AudioSmith (install %d).", id))
	} else {
		out = append(out, i18n.T("The message (%04X) is not AudioSmith's: another app's watermark, or a damaged mark.", r.Message))
	}
	return out
}

// checkWatermark runs the detector on path.
func (a *app) checkWatermark(path string) {
	w := &a.wmCheck
	w.file, w.busy, w.res, w.err = path, true, nil, ""
	lib := a.lib
	a.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		r, err := lib.detectWatermark(ctx, path)
		a.post(func() {
			if a.wmCheck.file != path {
				return
			}
			a.wmCheck.busy = false
			switch {
			case errors.Is(err, core.ErrNotDownloaded):
				a.wmCheck.err = i18n.T("Download the watermark model in Models first.")
			case err != nil:
				a.wmCheck.err = engineErrorText(err)
			default:
				a.wmCheck.res = &r
			}
		})
	})
}
