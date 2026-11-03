package main

// The read-along player of a finished audiobook: a chapter's text with
// the word being read highlighted, words already read dimmed; a click on
// a word (or a paragraph) seeks there. Word times are estimated from where
// each line and span landed in the chapter (the chapter's sidecar), spread
// over the span by length; "Align words" asks the engine's forced aligner
// (stt.align, Qwen3-ForcedAligner) for real ones, kept next to the chapter
// as <chapter>.words.json.

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// readAlongOwner prefixes the owners of alignment jobs.
const readAlongOwner = "readalong:"

// readAlong is a page's read-along state.
type readAlong struct {
	render   string // the render the chapters are of
	loading  bool
	chapters []raChapter
	chapter  int            // the chapter shown
	aligning map[int]string // chapter → its stt.align job's owner
}

// raChapter is one chapter's text and timings.
type raChapter struct {
	Title   string
	File    string
	Seconds float64
	Aligned bool
	Paras   []raPara
}

type raPara struct {
	Start, End float64
	Words      []raWord
}

type raWord struct {
	Text       string
	Start, End float64
}

// wordsOf splits plain text into display words: spaced scripts by
// spaces, unspaced ones (Han, kana…) a character each, punctuation kept
// with the word before it.
func wordsOf(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		var cur strings.Builder
		for _, r := range f {
			if text.IsUnspaced(r) && unicode.IsLetter(r) {
				if cur.Len() > 0 {
					out = append(out, cur.String())
					cur.Reset()
				}
				cur.WriteRune(r)
				continue
			}
			if cur.Len() > 0 && unicode.IsLetter(r) && text.IsUnspaced(lastRune(cur.String())) {
				out = append(out, cur.String())
				cur.Reset()
			}
			cur.WriteRune(r)
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
	}
	return out
}

// spaceBetween says whether a space separates two display words: not
// when either side is unspaced (Han, kana, CJK punctuation…).
func spaceBetween(prev, next string) bool {
	return !text.IsUnspaced(lastRune(prev)) && !text.IsUnspaced(firstRune(next))
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

// estimateChapter turns a chapter's sidecar into paragraphs of timed words.
func estimateChapter(title string, r services.ChapterResult) raChapter {
	ch := raChapter{Title: title, File: r.Output, Seconds: r.AudioSeconds}
	for _, l := range r.Lines {
		p := raPara{Start: l.Start, End: l.End}
		for _, sp := range l.Spans {
			plain := markup.Parse(sp.Text, &markup.Options{NoChapters: true}).Plain()
			ws := wordsOf(plain)
			total := 0
			for _, w := range ws {
				total += utf8.RuneCountInString(w)
			}
			at, span := sp.Start, sp.End-sp.Start
			for _, w := range ws {
				d := span * float64(utf8.RuneCountInString(w)) / float64(max(total, 1))
				p.Words = append(p.Words, raWord{Text: w, Start: at, End: at + d})
				at += d
			}
		}
		if len(p.Words) > 0 {
			ch.Paras = append(ch.Paras, p)
		}
	}
	return ch
}

// applyAlignment replaces estimated times with aligned ones when the
// aligner returned the same number of words; otherwise it maps them in
// order as far as they go.
func applyAlignment(ch *raChapter, words []proto.Word) bool {
	if len(words) == 0 {
		return false
	}
	i := 0
	for pi := range ch.Paras {
		p := &ch.Paras[pi]
		for wi := range p.Words {
			if i >= len(words) {
				return true
			}
			p.Words[wi].Start, p.Words[wi].End = words[i].Start, words[i].End
			i++
		}
		if len(p.Words) > 0 {
			p.Start, p.End = p.Words[0].Start, p.Words[len(p.Words)-1].End
		}
	}
	ch.Aligned = true
	return true
}

// alignedPath is where a chapter's aligned words are kept.
func alignedPath(chapterFile string) string { return chapterFile + ".words.json" }

// loadReadAlong reads the chapters of a finished render, in the background.
func (a *app) loadReadAlong(p *lfPage, r *lfRender) {
	if p.ra.render == r.rec.ID || r.body.Output == nil {
		return
	}
	p.ra = readAlong{render: r.rec.ID, loading: true, aligning: map[int]string{}}
	id := r.rec.ID
	chs := r.body.Output.Chapters
	a.background(func() {
		var out []raChapter
		for i, c := range chs {
			res, err := services.ReadChapter(c.File)
			if err != nil {
				continue
			}
			title := c.Title
			if title == "" {
				title = i18n.T("Chapter %d", i+1)
			}
			ch := estimateChapter(title, res)
			if data, err := os.ReadFile(alignedPath(c.File)); err == nil {
				var ar proto.AlignResult
				if json.Unmarshal(data, &ar) == nil {
					applyAlignment(&ch, ar.Words)
				}
			}
			out = append(out, ch)
		}
		a.post(func() {
			if p.ra.render == id {
				p.ra.chapters, p.ra.loading = out, false
			}
		})
	})
}

// raPlayerID is the player id of a chapter of the read-along.
func raPlayerID(render string, ch int) string { return "ra:" + render + ":" + strconv.Itoa(ch) }

// readAlongView is the player: chapter, play, the text with the current word.
func (a *app) readAlongView(c *ui.Context, p *lfPage, r *lfRender) {
	t := c.Theme()
	a.loadReadAlong(p, r)
	ra := &p.ra
	if ra.loading || len(ra.chapters) == 0 {
		return
	}
	ra.chapter = min(max(ra.chapter, 0), len(ra.chapters)-1)
	ch := &ra.chapters[ra.chapter]
	id := raPlayerID(r.rec.ID, ra.chapter)
	pos := -1.0
	if a.pl.Current() == id {
		pos = float64(a.pl.Progress(id)) * ch.Seconds
		if a.pl.Playing(id) {
			c.After(200 * time.Millisecond) // the word playing
		}
	}
	k := tok(t)
	ui.Box(c).Height(1).Background(k.Line)
	ui.Row(c.Key("ra-head")).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Follow along")).FontWeight(600).SingleLine().Shrink(0)
		ui.Spacer(c)
		if iconButton(c.Key("ra-prev"), icons.AltArrowLeft, i18n.T("Previous chapter")).Disabled(ra.chapter == 0).Clicked() {
			ra.chapter--
		}
		truncText(c, ch.Title).TextAlign(ui.Center).Shrink(1).MaxWidth(px(t, 260))
		if iconButton(c.Key("ra-next"), icons.AltArrowRight, i18n.T("Next chapter")).Disabled(ra.chapter >= len(ra.chapters)-1).Clicked() {
			ra.chapter++
		}
		if playToggle(c.Key("ra-play"), a.pl.Playing(id), i18n.T("Play chapter"), i18n.T("Pause")).Clicked() {
			if err := a.pl.Toggle(id, ch.File); err != nil {
				a.toastWarn(i18n.T("Can't play: %v", err))
			}
		}
	})
	ui.Row(c.Key("ra-timing")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
		note := i18n.T("Word timing is estimated from where each line landed.")
		if ch.Aligned {
			note = i18n.T("Word timing comes from the forced aligner.")
		}
		caption(c, note).TextColor(k.Text3).Grow(1).MinWidth(0)
		if _, busy := ra.aligning[ra.chapter]; busy {
			ui.Spinner(c).Label(i18n.T("Aligning words…"))
		} else if !ch.Aligned {
			if ghostButton(c.Key("ra-align"), i18n.T("Align words")).Tooltip(i18n.T("Find each word's time with the forced aligner")).Clicked() {
				a.alignChapter(p, r, ra.chapter)
			}
		}
	})
	seek := func(sec float64) {
		if a.pl.Current() != id {
			if err := a.pl.Toggle(id, ch.File); err != nil {
				a.toastWarn(i18n.T("Can't play: %v", err))
				return
			}
		}
		if ch.Seconds > 0 {
			a.pl.Seek(id, float32(min(max(sec/ch.Seconds, 0), 1)))
		}
	}
	// The paragraph being read, one before and two after it.
	cur := 0
	if pos >= 0 {
		cur = sort.Search(len(ch.Paras), func(i int) bool { return ch.Paras[i].Start > pos }) - 1
		cur = max(cur, 0)
	}
	from, to := max(cur-1, 0), min(cur+3, len(ch.Paras))
	ui.Column(c.Key("ra-text")).Gap(px(t, 8)).Children(func() {
		for pi := from; pi < to; pi++ {
			para := ch.Paras[pi]
			if pi != cur {
				var b strings.Builder
				for i, w := range para.Words {
					if i > 0 && spaceBetween(para.Words[i-1].Text, w.Text) {
						b.WriteByte(' ')
					}
					b.WriteString(w.Text)
				}
				start := para.Start
				if ui.ButtonBase(c.Key("ra-p-" + strconv.Itoa(pi))).Label(i18n.T("Play from here")).Children(func() {
					ui.Text(c, b.String()).TextColor(k.Text2).FontSize(px(t, 14)).MaxLines(3).Ellipsis("…").Grow(1).MinWidth(0)
				}).Clicked() {
					seek(start)
				}
				continue
			}
			// Spaced words keep a space's gap after them; unspaced ones
			// (Han, kana…) sit side by side, as the text has them.
			ui.Row(c.Key("ra-cur-" + strconv.Itoa(pi))).Wrap().GapX(0).GapY(px(t, 4)).Children(func() {
				for wi, w := range para.Words {
					gap := float32(0)
					if wi+1 < len(para.Words) && spaceBetween(w.Text, para.Words[wi+1].Text) {
						gap = px(t, 4)
					}
					pad := px(t, 1)
					if text.IsUnspaced(firstRune(w.Text)) {
						pad = 0
					}
					color := t.Text
					bg := ui.Transparent
					switch {
					case pos >= w.Start && pos < w.End:
						bg, color = t.Selection, k.Text
					case pos >= w.End:
						color = k.Text2
					}
					start := w.Start
					if ui.ButtonBase(c.Key("w"+strconv.Itoa(wi))).Label(w.Text).Radius(3).Padding(0, pad).Margin(0, gap, 0, 0).Background(bg).Children(func() {
						ui.Text(c, w.Text).TextColor(color).FontSize(px(t, 15))
					}).Clicked() {
						seek(start)
					}
				}
			})
		}
	})
}

// alignChapter submits stt.align for a chapter (when the aligner is
// downloaded; else it offers it).
func (a *app) alignChapter(p *lfPage, r *lfRender, i int) {
	e, ok := catalog.Builtin().ByID(catalog.DefaultAlign)
	if !ok {
		return
	}
	if m := a.model(e); m.known && m.local.State != hub.Downloaded {
		a.toast(i18n.T("Download %s first (Models).", modelRef(e)))
		a.download(e)
		return
	}
	ch := p.ra.chapters[i]
	var b strings.Builder
	for _, para := range ch.Paras {
		for _, w := range para.Words {
			b.WriteString(w.Text)
			b.WriteByte(' ')
		}
		b.WriteByte('\n')
	}
	params, _ := json.Marshal(proto.AlignParams{Model: e.Repo, Audio: ch.File, Text: strings.TrimSpace(b.String()), Language: r.body.Summary.Lang})
	owner := readAlongOwner + r.rec.ID + ":" + strconv.Itoa(i) + ":" + core.NewID(time.Now())
	p.ra.aligning[i] = owner
	sched := a.lib.sched
	spec := jobs.Spec{Kind: proto.MethodSTTAlign, Lane: jobs.Lane(proto.RoleOf(proto.MethodSTTAlign)), Priority: jobs.PriorityNormal,
		Params: params, Title: i18n.T("Align words · %s", ch.Title), Owner: owner}
	a.async(func() {
		if _, err := sched.Submit(context.Background(), spec); err != nil {
			a.post(func() {
				delete(p.ra.aligning, i)
				a.toastWarn(i18n.T("Could not align: %v", err))
			})
		}
	})
}

// alignDone applies a finished alignment job (jobs.Updated subscriber).
func (a *app) alignDone(j jobs.Job) {
	for _, p := range []*lfPage{a.lfStories, a.lfBook} {
		for i, owner := range p.ra.aligning {
			if owner != j.Owner {
				continue
			}
			delete(p.ra.aligning, i)
			if j.State != jobs.StateSucceeded {
				a.toastWarn(i18n.T("Could not align: %s", jobErrorText(j.Err)))
				return
			}
			var res proto.AlignResult
			if json.Unmarshal(j.Result, &res) != nil || i >= len(p.ra.chapters) {
				return
			}
			ch := &p.ra.chapters[i]
			if !applyAlignment(ch, res.Words) {
				a.toast(i18n.T("The aligner found no words."))
				return
			}
			path, data := alignedPath(ch.File), j.Result
			a.background(func() { oplog.Err("file.write", os.WriteFile(path, data, 0o644), "file", baseName(path)) })
			return
		}
	}
}
