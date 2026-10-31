package main

// The Dub page's centre before the transcript (thread #106): what the
// source is. For a link, what its site said before the download
// (Dubs.Probe: the cover, the site, title, channel, length, size and
// description), with the download's progress under the cover; for a
// file, its poster, name, length and picture. Once the video is on disk
// the centre shows the video itself. The later steps' progress is in the
// footer; the centre keeps the video and its details until the transcript
// comes.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
)

// dubSourceCentre is the workspace before the transcript; compact, it is
// the head of the narrow page, above the step's panel.
func (a *app) dubSourceCentre(c *ui.Context, compact bool) {
	t := c.Theme()
	p := a.dub
	if p.Source.Path == "" && p.Source.URL == "" {
		ui.Icon(c, icons.Subtitles.Linear).Size(px(t, 36), px(t, 36)).TextColor(tok(t).Text3)
		caption(c, i18n.T("Choose the video or audio to dub in the Source step.")).TextAlign(ui.Center)
		return
	}
	_, winH := c.Size()
	if compact {
		// Narrow: the picture beside its details, as a list's row.
		w := min(px(t, 200), max(a.dubWorkW-2*px(t, sp6), px(t, 240))*0.4)
		ui.Row(c.Key("dub-src-centre")).Gap(px(t, sp3)).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Width(w).Gap(px(t, sp1)).Shrink(0).Children(func() { a.dubSourceMedia(c, w, w*9/16) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp1)).Children(func() { a.dubSourceDetails(c) })
		})
		return
	}
	// 16:9 as wide as the workspace (at most 640), and short enough that
	// its details show under it.
	w := max(min(a.dubWorkW-2*px(t, sp6), px(t, 640), max(winH*0.42, px(t, 150))*16/9), px(t, 200))
	ui.Column(c.Key("dub-src-centre")).Width(w).Gap(px(t, sp2)).Children(func() {
		a.dubSourceMedia(c, w, w*9/16)
		a.dubSourceDetails(c)
	})
}

// dubSourceMedia is the video once it is on disk, else the cover (the
// site's, or the file's poster) with the download's progress under it.
func (a *app) dubSourceMedia(c *ui.Context, w, h float32) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	video := dubVideoReady(p)
	if video {
		a.dubEnsureVideo()
	}
	if v := &a.dubVideo; video && v.wv != nil && v.project == p.ID {
		ui.WebView(c, v.wv).Width(w).Height(h).Radius(px(t, radiusCard))
	} else {
		bmp := a.coverBitmap(a.dubCoverPath(p))
		if bmp == nil && dubAudioOnly(p) {
			h = min(h, px(t, 96)) // sound alone: a band, not a screen
		}
		box := ui.Box(c.Key("dub-cover")).Width(w).Height(h).Radius(px(t, radiusCard)).Background(k.Subtle).Center().Clip()
		box.Children(func() {
			switch {
			case bmp != nil:
				ui.Image(c, bmp).Width(w).Height(h).Fit(ui.Contain).Label(i18n.T("Cover"))
			case a.dubProbing():
				ui.Column(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					caption(c, i18n.T("Reading the video's details…"))
				})
			default:
				ic := dubIcon
				if dubAudioOnly(p) {
					ic = icons.Soundwave
				}
				ui.Icon(c, ic.Linear).Size(px(t, 40), px(t, 40)).TextColor(k.Text3)
			}
		})
	}
	// The download: a thin bar under the cover, with the size and time left.
	if j := a.dubRunning(); j.Kind == services.KindDubFetch && j.State == jobs.StateRunning {
		if f := determinate(j); f > 0 {
			ui.Progress(c.Key("dub-src-bar"), f).Width(w).Height(px(t, 4)).Label(dubFetchStatus(j.Progress))
		}
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			if determinate(j) <= 0 {
				ui.Spinner(c).Size(px(t, 12), px(t, 12))
			}
			caption(c, dubFetchStatus(j.Progress)).SingleLine().Ellipsis("…").MinWidth(0)
		})
	}
	if video && p.Media.Audio != "" {
		a.dubPlayControls(c, false)
	}
}

// dubVideoReady reports whether the source's video is on disk in a form
// the preview plays (MP4/MOV in H.264 or HEVC, or its preview copy).
func dubVideoReady(p *dub.Project) bool {
	if p.Source.Path == "" {
		return false
	}
	m := p.Source.Meta
	if !p.Source.HasVideo && (p.Media.Audio != "" || m == nil || !m.HasVideo() || m.VCodec == "") {
		return false
	}
	if p.Media.Proxy != "" {
		return true
	}
	codec := ""
	if v := p.Source.Video; v != nil {
		codec = v.Codec
	} else if m != nil {
		codec = m.VCodec
	}
	return dubPlayable(p.Source.Path, codec)
}

// dubPlayable: the web view plays the file as it is.
func dubPlayable(path, codec string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "mp4", "mov", "m4v":
	default:
		return false
	}
	codec = strings.ToLower(codec)
	for _, c := range []string{"avc", "h264", "hev", "hvc", "h265", "hevc", "mp4v", "mpeg4"} {
		if strings.HasPrefix(codec, c) {
			return true
		}
	}
	return codec == ""
}

// dubAudioOnly reports a source known to have no picture: by the site's
// format, by what reading the file found, else by its extension.
func dubAudioOnly(p *dub.Project) bool {
	switch {
	case p.Source.Meta != nil && p.Source.Meta.Error == "":
		return !p.Source.Meta.HasVideo()
	case p.Source.HasVideo || p.Source.Video != nil:
		return false
	case p.Media.Audio != "" || p.Source.Duration > 0:
		return true // read: no picture
	case p.Source.Path != "":
		return !slices.Contains(dub.VideoExts, strings.ToLower(strings.TrimPrefix(filepath.Ext(p.Source.Path), ".")))
	}
	return false
}

// dubCoverPath is the picture the centre shows before the video: the
// file's poster, else the site's cover ("" = none).
func (a *app) dubCoverPath(p *dub.Project) string {
	if p.Media.Poster != "" {
		return a.dubSvc.Path(p.ID, p.Media.Poster)
	}
	if m := p.Source.Meta; m != nil && m.Thumbnail != "" {
		return a.dubSvc.Path(p.ID, m.Thumbnail)
	}
	return ""
}

// dubSourceDetails: the site and channel, the title, length, picture,
// size, date and views, the description and the captions; or why the
// link could not be read.
func (a *app) dubSourceDetails(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	p := a.dub
	m := p.Source.Meta
	if m != nil && m.Error != "" && p.Source.Path == "" {
		head, why := dubProbeErrorText(m.Error, m.Detail)
		notice(c, "dub-probe-err", noticeError, head, "", func() {
			if ghostButton(c.Key("dub-probe-again"), i18n.T("Try again")).Disabled(a.dubBusy() || a.dubProbing()).Clicked() {
				a.dubProbe(p.ID)
			}
		}, nil)
		if why != "" {
			caption(c.Key("dub-probe-why"), why).MaxLines(3).Ellipsis("…").Selectable()
		}
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			caption(c, p.Source.URL).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(p.Source.URL)
			if a.dubCardAbove { // the panel's card, with its 🗑, is left out
				ui.Spacer(c)
				a.dubRemoveSourceButton(c, a.dubBusy())
			}
		})
		return
	}
	if m != nil && m.NeedsFFmpeg && p.Source.Path == "" && a.mediaTool() == nil {
		notice(c, "dub-src-ffmpeg", noticeWarn, dubURLErrorText(&dub.URLError{Code: dub.CodeYtDlp, Detail: "Requested format is not available"}), "", func() {
			if secondaryButton(c.Key("dub-src-ffmpeg-go"), i18n.T("Open Settings → Audio tools")).Clicked() {
				a.navigate("/settings/audio-tools")
			}
		}, nil)
	}
	if m != nil && (m.SiteName != "" || m.Channel != "") {
		ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
			if m.SiteName != "" {
				tagChip(c, m.SiteName)
			}
			if m.Channel != "" {
				ui.Text(c, m.Channel).FontSize(px(t, fsCaption)).TextColor(k.Text2).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(m.Channel)
			}
		})
	}
	title := firstOf(p.Source.Name, p.Title)
	if m != nil && m.Title != "" {
		title = m.Title
	}
	if title == p.Source.URL && !a.dubCardAbove {
		title = "" // the link shows under
	}
	if title != "" || a.dubCardAbove {
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Start).Children(func() {
			tx := ui.Text(c, title).FontSize(px(t, fsSection)).FontWeight(600).MaxLines(2).Ellipsis("…").Tooltip(title).Grow(1).MinWidth(0)
			if a.dubCardAbove {
				tx.FontSize(px(t, fsBody))
			}
			if a.dubCardAbove { // the panel's card, with its 🗑, is left out
				a.dubRemoveSourceButton(c, a.dubBusy())
			}
		})
	}
	if line := dubSourceFacts(p); line != "" {
		caption(c.Key("dub-src-facts"), line).MaxLines(2).Ellipsis("…")
	}
	if p.Source.Kind == dub.SourceURL && title != p.Source.URL {
		caption(c, p.Source.URL).TextColor(k.Text3).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(p.Source.URL)
	}
	if m == nil {
		if p.Source.Kind == dub.SourceURL && p.Source.Path == "" && !a.dubProbing() && !a.dubBusy() {
			if ghostButton(c.Key("dub-probe"), i18n.T("Read the video's details")).Clicked() {
				a.dubProbe(p.ID)
			}
		}
		return
	}
	if d := m.Description; d != "" {
		long := utf8.RuneCountInString(d) > 180 || strings.Count(d, "\n") > 2
		txt := ui.Text(c.Key("dub-src-desc"), d).FontSize(px(t, fsCaption)).TextColor(k.Text2).Selectable()
		if long && !a.dubDescOpen {
			txt.MaxLines(3).Ellipsis("…")
		}
		if long {
			label := i18n.T("Expand")
			if a.dubDescOpen {
				label = i18n.T("Collapse")
			}
			if linkButton(c.Key("dub-src-desc-more"), label).AlignSelf(ui.Start).Clicked() {
				a.dubDescOpen = !a.dubDescOpen
			}
		}
	}
	if len(m.Captions) > 0 {
		var names []string
		for _, l := range m.Captions[:min(len(m.Captions), 6)] {
			names = append(names, dubLangName(l))
		}
		s := strings.Join(names, ", ")
		if n := len(m.Captions) - 6; n > 0 {
			s += " " + i18n.N(n, "+%d more", "+%d more", n)
		}
		caption(c.Key("dub-src-caps"), i18n.T("Captions: %s", s)).MaxLines(2).Ellipsis("…")
	}
}

// dubSourceFacts is "3:32 · 1080p · ≈ 75 MB · Dec 31, 2019 · 107.6M views",
// as much as is known: of the site's metadata until the source is read,
// then of the file.
func dubSourceFacts(p *dub.Project) string {
	m := p.Source.Meta
	var parts []string
	dur := p.Source.Duration
	if dur <= 0 && m != nil {
		dur = m.Duration
	}
	if dur > 0 {
		parts = append(parts, clock(dur))
	}
	switch v := p.Source.Video; {
	case v != nil && v.Width > 0:
		parts = append(parts, dubPicture(v.Width, v.Height)+" "+strings.ToUpper(v.Codec))
	case m != nil && m.Width > 0 && m.Height > 0:
		parts = append(parts, dubPicture(m.Width, m.Height))
	case dubAudioOnly(p):
		parts = append(parts, i18n.T("Audio only"))
	}
	switch {
	case p.Source.Size > 0:
		parts = append(parts, humanBytes(p.Source.Size))
	case m != nil && m.Size > 0 && m.Approx:
		parts = append(parts, "≈ "+humanBytes(m.Size))
	case m != nil && m.Size > 0:
		parts = append(parts, humanBytes(m.Size))
	}
	if m != nil && m.Uploaded != "" {
		if d, err := time.ParseInLocation(time.DateOnly, m.Uploaded, time.Local); err == nil {
			parts = append(parts, i18n.Date(d))
		}
	}
	if m != nil && m.Views >= 0 {
		parts = append(parts, i18n.N(int(min(m.Views, 1<<31-1)), "%s view", "%s views", compactCount(m.Views)))
	}
	return joinDot(parts)
}

// dubPicture is "1080p", or "576×1024" for an upright picture.
func dubPicture(w, h int) string {
	if h > w {
		return fmt.Sprintf("%d×%d", w, h)
	}
	return fmt.Sprintf("%dp", h)
}

// compactCount is "1,234", "12.3K", "4.5M" (in Chinese "1234", "12.3万",
// "4.5亿").
func compactCount(n int64) string {
	f := float64(n)
	trim := func(s string) string { return strings.TrimSuffix(s, ".0") }
	if i18n.Locale() == "zh-Hans" {
		switch {
		case n >= 1e8:
			return trim(fmt.Sprintf("%.1f", f/1e8)) + "亿"
		case n >= 1e4:
			return trim(fmt.Sprintf("%.1f", f/1e4)) + "万"
		}
		return fmt.Sprint(n)
	}
	switch {
	case n >= 1e9:
		return trim(fmt.Sprintf("%.1f", f/1e9)) + "B"
	case n >= 1e6:
		return trim(fmt.Sprintf("%.1f", f/1e6)) + "M"
	case n >= 1e4:
		return trim(fmt.Sprintf("%.1f", f/1e3)) + "K"
	case n >= 1e3:
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprint(n)
}

// dubProbeErrorText says why a link could not be read: a headline, and
// yt-dlp's reason, short ("" when the headline says it all).
func dubProbeErrorText(code, detail string) (string, string) {
	if code != dub.CodeYtDlp {
		return dubURLErrorText(&dub.URLError{Code: code, Detail: detail}), ""
	}
	why := detail
	if i := strings.Index(why, ". Use --cookies"); i > 0 {
		why = why[:i+1] // yt-dlp's advice on its own options
	}
	if i := strings.Index(why, " See  https://"); i > 0 {
		why = why[:i]
	}
	if r := []rune(why); len(r) > 200 {
		why = string(r[:200]) + "…"
	}
	l := strings.ToLower(detail)
	switch {
	case strings.Contains(l, "unsupported url"):
		return i18n.T("This site is not supported for importing."), ""
	case strings.Contains(l, "private video") || strings.Contains(l, "video is private"):
		return i18n.T("This video is private."), why
	case strings.Contains(l, "not available in your country") || strings.Contains(l, "geo") && strings.Contains(l, "restrict") ||
		strings.Contains(l, "not available from your location"):
		return i18n.T("This video is not available in your region."), why
	case strings.Contains(l, "sign in") || strings.Contains(l, "log in") || strings.Contains(l, "logged-in") || strings.Contains(l, "login") ||
		strings.Contains(l, "cookies"):
		return i18n.T("The site wants a sign-in (or its cookies) for this video."), why
	case strings.Contains(l, "unavailable") || strings.Contains(l, "no video could be found") || strings.Contains(l, "404") ||
		strings.Contains(l, "has been removed"):
		return i18n.T("This video is unavailable."), why
	}
	return i18n.T("The link could not be read."), why
}
