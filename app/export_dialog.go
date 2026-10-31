package main

// The Export audio dialog (thread #31): a take exported with settings.
//
//   - Format: WAV always; M4A (AAC) through the engine's media.encode; MP3,
//     FLAC and Opus through ffmpeg, greyed out with a tip pointing at
//     Settings → Audio tools while it is not set up.
//   - By format: WAV's bit depth (16, 24, 32 float), FLAC's (16, 24), the
//     lossy formats' bitrate; the sample rate (not Opus: always 48 kHz) and
//     the channels.
//   - Processing (services.ExportAudio, internal/dsp): a loudness target
//     with a true-peak ceiling, and trimmed leading and trailing silence.
//   - The file's name and, for formats with tags, its title and artist;
//     files saved beside it (the script, subtitles from the chunks).
//   - The size and length it will have, then Export…: the save panel, the
//     export in the background with toasts, the record in Projects →
//     Exports.
//
// What it offers and how it opens depend on the kind of take (exportKinds:
// a Studio take, a voice's preview, a converted recording, a take of the
// Local API); the last settings are remembered per kind (settings.Export).
// History, the Studio's latest take, the History's details and Projects
// open it (a.openExport).
//
// Files: export_dialog.go (this: state, opening, the view), export_form.go
// (kinds, formats, the form's values), export_run.go (exporting).

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/ffmpeg"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// ---- State ----

// exportState is the app's part: the dialog, while open.
type exportState struct {
	exp *exportForm
}

// exportForm is the dialog's form. The controls bind to its fields
// directly (indices into their options), so a choice survives the builds
// that follow it.
type exportForm struct {
	open bool
	kind exportKind
	take core.Take
	src  string // the take's audio

	format   int                   // into exportFormats
	bits     [5]int                // per format: into its bits
	bitrate  [5]int                // per format: into its bitrates
	rate     [5]int                // per format: into its rateOptions
	channels int                   // 0 the source's, 1 the other
	loud     string                // the loudness choice's label (ui.Select)
	trim     bool                  // leading and trailing silence
	name     string                // without the extension
	title    string                // tag
	artist   string                // tag
	extras   map[string]*bool      // files beside the audio, by extra
	info     *services.AudioInfo   // the source, once read
	availOK  map[string]bool       // formats that can be written now
	availWhy map[string]string     // why the others cannot
	encode   services.AudioEncoder // tests' encoder; nil: a.exportEncoder()
}

// openExport opens the dialog for a take.
func (a *app) openExport(t *core.Take) {
	if t == nil {
		return
	}
	k := exportKindOf(t)
	prefs, ok := a.prefs.Export.Audio[k.id]
	if !ok {
		prefs = k.defaults
	}
	f := &exportForm{open: true, kind: k, take: *t, src: a.takeStore.AudioPath(*t),
		availOK: map[string]bool{}, availWhy: map[string]string{}, extras: map[string]*bool{}}
	a.exportAvailability(f)
	f.apply(prefs, k.defaults)
	f.name = strings.TrimSuffix(exportName(t), ".wav")
	f.title, f.artist = exportTitle(t.Text), "AudioSmith"
	for _, x := range k.extras {
		on := slices.Contains(prefs.Extras, x)
		f.extras[x] = &on
	}
	a.exp = f
	src := f.src
	a.background(func() {
		info, err := services.AnalyzeAudio(src)
		a.post(func() {
			if a.exp != f {
				return
			}
			if err != nil {
				a.toastWarn(i18n.T("Could not read the audio: %v", err))
				return
			}
			f.info = &info
		})
	})
}

// exportAvailability says which formats can be written now, and why not.
func (a *app) exportAvailability(f *exportForm) {
	exportAvailable(f, a.lib.encodeExecutor() != nil && a.engineEncodes(), a.mediaTool())
}

// exportAvailable fills a form's formats that can be written: M4A when
// m4a (the engine encodes), the others through ffmpeg (tool, nil = none).
func exportAvailable(f *exportForm, m4a bool, tool *ffmpeg.Tool) {
	for _, ef := range exportFormats {
		switch ef.id {
		case services.FormatWAV:
			f.availOK[ef.id] = true
		case services.FormatM4A:
			f.availOK[ef.id] = m4a
			f.availWhy[ef.id] = i18n.T("M4A needs the speech engine; it is not running.")
		default:
			f.availOK[ef.id] = tool.CanEncode(ef.id)
			f.availWhy[ef.id] = i18n.T("%s needs ffmpeg: set it up in Settings → Audio tools.", ef.label)
		}
	}
}

// ---- The view ----

// exportDialog is the dialog, while open (shell.go draws it over the page).
func (a *app) exportDialog(c *ui.Context) {
	f := a.exp
	if f == nil {
		return
	}
	if !f.open {
		a.exp = nil
		return
	}
	t := c.Theme()
	k := tok(t)
	ef := f.fmt_()
	sub := kindLabel(&f.take) + " · " + fileDetails(&f.take)
	dialogFrame(c, "export-dialog", &f.open, i18n.T("Export audio"), sub, px(t, 560), func() {
		sectionTitle(c, i18n.T("Audio"))
		formRow(c, "ex-format", i18n.T("Format"), "", func() {
			labels := make([]string, len(exportFormats))
			off := make([]bool, len(exportFormats))
			tips := make([]string, len(exportFormats))
			for i, x := range exportFormats {
				labels[i], off[i] = x.label, !f.availOK[x.id]
				if off[i] {
					tips[i] = f.availWhy[x.id]
				}
			}
			segmentedSome(c.Key("ex-format-seg"), &f.format, labels, off, tips).Label(i18n.T("Format"))
			if slices.Contains(off, true) {
				infoTip(c.Key("ex-format-why"), exportMissing(f))
			}
		})
		formRow(c, "ex-loud", i18n.T("Loudness"), i18n.T("Brings the whole file to a loudness target, with peaks held under a ceiling (−1.5 dBTP for podcasts, −1 dBTP otherwise). −16 LUFS suits podcasts and streaming, −23 LUFS broadcast (EBU R128)."), func() {
			opts := make([]string, len(exportLoudness))
			for i, key := range exportLoudness {
				opts[i] = loudnessLabel(key)
			}
			if f.info != nil && f.info.Integrated > -70 {
				caption(c, i18n.T("Now %s LUFS", signedDB(f.info.Integrated))).SingleLine()
			}
			ui.Select(c.Key("ex-loud-select"), &f.loud, opts).Label(i18n.T("Loudness")).Width(px(t, 200)).Shrink(1).MinWidth(px(t, 120))
		})

		sectionTitle(c, i18n.T("File"))
		formRow(c, "ex-name", i18n.T("File name"), "", func() {
			ui.TextInput(c.Key("ex-name-input"), &f.name).Label(i18n.T("File name")).Width(px(t, 220)).Shrink(1).MinWidth(px(t, 120))
			ui.Text(c, "."+ef.ext).TextColor(k.Text2).SingleLine()
		})
		if ef.tags && f.kind.tags {
			formRow(c, "ex-title", i18n.T("Title"), i18n.T("Written into the file, for players and libraries."), func() {
				ui.TextInput(c.Key("ex-title-input"), &f.title).Label(i18n.T("Title")).Width(px(t, 240)).Shrink(1).MinWidth(px(t, 120))
			})
			formRow(c, "ex-artist", i18n.T("Artist"), "", func() {
				ui.TextInput(c.Key("ex-artist-input"), &f.artist).Label(i18n.T("Artist")).Width(px(t, 240)).Shrink(1).MinWidth(px(t, 120))
			})
		}
		if extras := f.extrasShown(); len(extras) > 0 {
			sectionTitle(c, i18n.T("Also save"))
			ui.Column(c.Key("ex-extras")).Gap(px(t, sp2)).Padding(px(t, 2), 0).Children(func() {
				for _, x := range extras {
					ui.Checkbox(c.Key("ex-extra-"+x), f.extras[x], extraLabel(x, ef.ext))
				}
			})
		}
		ui.Box(c).Height(px(t, sp1))
		a.advancedSection(c, "export", f.changes(), false, f.restore, func() {
			if len(ef.bits) > 0 {
				formRow(c, "ex-bits", i18n.T("Bit depth"), i18n.T("16-bit plays everywhere; 24-bit keeps more headroom for editing; 32-bit float keeps the model's own samples."), func() {
					labels := make([]string, len(ef.bits))
					for i, b := range ef.bits {
						labels[i] = bitsLabel(b)
					}
					segmentedFit(c.Key("ex-bits-"+ef.id), &f.bits[f.format], labels...).Label(i18n.T("Bit depth"))
				})
			}
			if len(ef.bitrates) > 0 {
				formRow(c, "ex-bitrate", i18n.T("Bitrate"), i18n.T("Higher keeps more detail and makes a larger file. Speech sounds clean from the lowest."), func() {
					labels := make([]string, len(ef.bitrates))
					for i, b := range ef.bitrates {
						labels[i] = i18n.T("%d kb/s", b)
					}
					segmentedFit(c.Key("ex-bitrate-"+ef.id), &f.bitrate[f.format], labels...).Label(i18n.T("Bitrate"))
				})
			}
			if ef.rates {
				tip := i18n.T("Raising it does not add detail; choose 44.1 or 48 kHz when an app or a platform asks for it.")
				if f.srcRate() < ef.minRate {
					tip = i18n.T("%[1]s at %[2]s cannot hold these bitrates, so it is written at 44.1 or 48 kHz.", ef.label, khz(f.srcRate()))
				}
				formRow(c, "ex-rate", i18n.T("Sample rate"), tip, func() {
					opts := f.rateOptions(ef)
					labels := make([]string, len(opts))
					for i, r := range opts {
						if r == 0 {
							labels[i] = i18n.X("audio", "Original (%s)", khz(f.srcRate()))
						} else {
							labels[i] = khz(r)
						}
					}
					f.rate[f.format] = min(max(f.rate[f.format], 0), len(opts)-1)
					segmentedFit(c.Key("ex-rate-"+ef.id), &f.rate[f.format], labels...).Label(i18n.T("Sample rate"))
				})
			}
			formRow(c, "ex-channels", i18n.T("Channels"), i18n.T("Stereo puts the same voice in both channels, for players and editors that expect two."), func() {
				orig, other := i18n.T("Original (mono)"), i18n.T("Stereo")
				if f.srcChannels() > 1 {
					orig, other = i18n.T("Original (stereo)"), i18n.T("Mono")
				}
				segmentedFit(c.Key("ex-ch-seg"), &f.channels, orig, other).Label(i18n.T("Channels"))
			})
			formRow(c, "ex-trim", i18n.T("Trim silence"), i18n.T("Cuts the silence before the first sound and after the last, keeping a short breath at each end."), func() {
				if f.trim && f.info != nil && f.info.Seconds-f.info.Trimmed >= 0.05 {
					caption(c, i18n.T("−%.1f s", f.info.Seconds-f.info.Trimmed)).SingleLine()
				}
				inkSwitch(c.Key("ex-trim-switch"), &f.trim).Label(i18n.T("Trim silence"))
			})
		})
	}, func() {
		size := humanBytes(f.estimate())
		ui.Text(c, i18n.T("About %s · %s", size, i18n.T("%.1f s", f.seconds()))).FontSize(px(t, fsCaption)).TextColor(k.Text2).
			SingleLine().Ellipsis("…").MinWidth(0).Grow(1)
		if secondaryButton(c.Key("ex-cancel"), i18n.T("Cancel")).Clicked() {
			f.open = false
		}
		if primaryButton(c.Key("ex-export"), i18n.T("Export…")).Disabled(!f.availOK[ef.id]).Clicked() {
			a.exportChoose(f)
		}
	})
}

// extrasShown are the kind's extras that apply to this take.
func (f *exportForm) extrasShown() []string {
	var out []string
	for _, x := range f.kind.extras {
		if x == "subtitles" && (f.take.Render == nil || len(f.take.Render.Chunks) == 0) {
			continue
		}
		out = append(out, x)
	}
	return out
}

// extraLabel names an extra.
func extraLabel(x, ext string) string {
	switch x {
	case "script":
		return i18n.T("The script, as a text file (.txt)")
	case "subtitles":
		return i18n.T("Subtitles timed to the audio (.srt)")
	}
	return x
}

// exportMissing says what greys formats out.
func exportMissing(f *exportForm) string {
	var lines []string
	if !f.availOK[services.FormatM4A] {
		lines = append(lines, f.availWhy[services.FormatM4A])
	}
	if !f.availOK[services.FormatMP3] {
		lines = append(lines, i18n.T("MP3, FLAC and Opus need ffmpeg: set it up in Settings → Audio tools."))
	}
	return strings.Join(lines, "\n")
}

// khz is "24 kHz", "44.1 kHz".
func khz(rate int) string {
	return i18n.T("%s kHz", strings.TrimSuffix(fmt.Sprintf("%.1f", float64(rate)/1000), ".0"))
}
