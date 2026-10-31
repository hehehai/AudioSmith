package main

// The Stories page's logic (/stories): the review buffer (pasted or
// imported text), Auto-cast and Split into lines, line editing, the cast,
// and the sample story.

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/autocast"
)

// Split modes of the review buffer.
var splitModes = []string{"sentences", "paragraphs", "chapters"}

// toneTags are the tone chips of a line: tag and label.
var toneTags = []string{"laughter", "sigh", "question-en", "surprise-wa", "confirmation-en", "dissatisfaction-hnn"}

// newLineID is a new line's id.
func newLineID() string { return core.NewID(time.Now()) }

// splitStory splits the review buffer into lines (headings become
// chapter lines) and appends them.
func (a *app) splitStory(p *lfPage) {
	src := strings.TrimSpace(p.doc.ImportText)
	if src == "" {
		a.toast(i18n.T("Nothing to split: paste or import a story first."))
		return
	}
	n := splitStoryText(&p.doc, src)
	p.doc.ImportText = ""
	p.open["review"] = false
	a.toast(i18n.N(n, "%d line", "%d lines", n))
}

// splitStoryText splits src by the document's SplitBy into lines
// (headings become chapter lines) and appends them; it returns how many
// spoken lines it added. The API's stories use it too.
func splitStoryText(d *services.LongformDoc, src string) int {
	opt := text.Options{Mode: text.Paragraphs, MaxChars: -1}
	switch d.SplitBy {
	case "sentences":
		opt.Mode, opt.MaxChars = text.Sentences, d.MaxChars
		if opt.MaxChars <= 0 {
			opt.MaxChars = 500
		}
	case "chapters":
		opt.Mode = text.Chapters
	}
	n := 0
	for _, ch := range text.Chunks(src, opt) {
		l := services.StoryLine{ID: newLineID(), Text: ch.Text}
		if ch.Heading {
			l.Chapter, l.Text = true, strings.TrimSpace(strings.TrimLeft(ch.Text, "#"))
		} else {
			n++
		}
		d.Lines = append(d.Lines, l)
	}
	return n
}

// autoCastStory finds who speaks in the review buffer, casts new speakers
// with voices round-robin, and appends the lines.
func (a *app) autoCastStory(p *lfPage) {
	src := strings.TrimSpace(p.doc.ImportText)
	lines, speakers := autoCastText(&p.doc, src, a.lfVoices(entryFor(p.doc.Model)))
	if lines == 0 {
		a.toast(i18n.T("Nothing to auto-cast: paste or import a story first."))
		return
	}
	p.doc.ImportText = ""
	p.open["review"] = false
	n := speakers + 1
	a.toast(i18n.N(lines, "Auto-cast %[1]d line across %[2]d voices", "Auto-cast %[1]d lines across %[2]d voices", lines, n))
	p.open["cast"] = true
}

// autoCastText finds who speaks in src ("Name: line"), casts new
// speakers with voices round-robin (voices nobody plays first), and
// appends the lines to d. It returns the lines found (0: nothing to
// cast, d unchanged) and the speakers. The API's stories use it too.
func autoCastText(d *services.LongformDoc, src string, voices []lfVoice) (lines, speakers int) {
	res := autocast.Detect(src, nil)
	if strings.TrimSpace(src) == "" || len(res.Lines) == 0 {
		return 0, 0
	}
	pick := castPicker(d, voices)
	ids := map[string]string{} // speaker (lower case) → cast id
	for _, m := range d.Cast {
		ids[strings.ToLower(m.Name)] = m.ID
	}
	spoke := map[string]bool{}
	for _, l := range res.Lines {
		line := services.StoryLine{ID: newLineID(), Text: l.Text, Direction: l.Direction}
		if text.IsHeading(l.Text) {
			line.Chapter, line.Text = true, strings.TrimSpace(strings.TrimLeft(l.Text, "#"))
			d.Lines = append(d.Lines, line)
			continue
		}
		if l.Speaker != "" {
			k := strings.ToLower(l.Speaker)
			id, ok := ids[k]
			if !ok {
				id = core.NewID(time.Now())
				ids[k] = id
				d.Cast = append(d.Cast, services.CastMember{ID: id, Name: l.Speaker, Voice: pick()})
			}
			line.Character = id
			spoke[k] = true
		}
		d.Lines = append(d.Lines, line)
	}
	return len(res.Lines), len(spoke)
}

// castPicker gives new characters voices: the narrator (d.Voice) is the
// first voice when it has none; then voices nobody plays yet, then
// round-robin.
func castPicker(d *services.LongformDoc, voices []lfVoice) func() string {
	if d.Voice == "" && len(voices) > 0 {
		d.Voice = voices[0].ref // the narrator
	}
	used := map[string]bool{d.Voice: true}
	for _, m := range d.Cast {
		used[m.Voice] = true
	}
	next := 0
	return func() string {
		if len(voices) == 0 {
			return ""
		}
		for range voices {
			v := voices[next%len(voices)]
			next++
			if !used[v.ref] {
				used[v.ref] = true
				return v.ref
			}
		}
		v := voices[next%len(voices)]
		next++
		return v.ref
	}
}

// importStory asks for a file and puts its text in the review buffer.
func (a *app) importStory(p *lfPage) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Import a story"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Stories"), Extensions: []string{"txt", "md", "srt", "vtt", "epub", "docx"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.importStoryFrom(p, paths[0]) })
	}()
}

// importStoryFrom reads a file in the background into the review buffer.
func (a *app) importStoryFrom(p *lfPage, path string) {
	if p.importing {
		return
	}
	p.importing, p.opened, p.listing = true, true, false
	a.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d, err := doc.Open(ctx, path, doc.Limits{MaxFileBytes: maxBookBytes})
		a.post(func() {
			p.importing = false
			if err != nil {
				a.toastWarn(bookImportError(path, err))
				return
			}
			s := d.Script()
			if strings.TrimSpace(p.doc.ImportText) != "" {
				s = strings.TrimRight(p.doc.ImportText, "\n") + "\n\n" + s
			}
			p.doc.ImportText = s
			if p.doc.Title == "" && d.Title != "" {
				p.doc.Title = d.Title
			}
			p.open["review"] = true
			a.toast(i18n.T("Imported %s: review it, then Auto-cast or Split into lines.", filepath.Base(path)))
		})
	})
}

// --- lines ---------------------------------------------------------------

// addLine adds a line after the selected one (or at the end).
func (a *app) addLine(p *lfPage, chapter bool) {
	l := services.StoryLine{ID: newLineID()}
	if chapter {
		n := 1
		for _, x := range p.doc.Lines {
			if x.Chapter {
				n++
			}
		}
		l.Chapter, l.Text = true, i18n.T("Chapter %d", n)
	} else if p.sel >= 0 && p.sel < len(p.doc.Lines) {
		l.Character = p.doc.Lines[p.sel].Character
	}
	at := len(p.doc.Lines)
	if p.sel >= 0 && p.sel < len(p.doc.Lines) {
		at = p.sel + 1
	}
	p.doc.Lines = append(p.doc.Lines[:at:at], append([]services.StoryLine{l}, p.doc.Lines[at:]...)...)
	p.sel = at
}

// moveLine moves line i by d (−1 up, +1 down).
func (a *app) moveLine(p *lfPage, i, d int) {
	j := i + d
	if i < 0 || j < 0 || i >= len(p.doc.Lines) || j >= len(p.doc.Lines) {
		return
	}
	p.doc.Lines[i], p.doc.Lines[j] = p.doc.Lines[j], p.doc.Lines[i]
	if p.sel == i {
		p.sel = j
	}
}

// deleteLine removes line i.
func (a *app) deleteLine(p *lfPage, i int) {
	if i < 0 || i >= len(p.doc.Lines) {
		return
	}
	p.doc.Lines = append(p.doc.Lines[:i:i], p.doc.Lines[i+1:]...)
	switch {
	case p.sel == i:
		p.sel = -1
	case p.sel > i:
		p.sel--
	}
}

// appendTag adds markup at the end of line i's text.
func appendTag(l *services.StoryLine, tag string) {
	t := strings.TrimRight(l.Text, " ")
	if t != "" {
		t += " "
	}
	l.Text = t + tag
}

// removeCast removes a character: its lines read in the default voice.
func (a *app) removeCast(p *lfPage, id string) {
	for i, m := range p.doc.Cast {
		if m.ID == id {
			p.doc.Cast = append(p.doc.Cast[:i:i], p.doc.Cast[i+1:]...)
			break
		}
	}
	for i := range p.doc.Lines {
		if p.doc.Lines[i].Character == id {
			p.doc.Lines[i].Character = ""
		}
	}
}

// loadSampleStory replaces an empty story with the sample: three voices,
// two chapters, pauses and tones.
func (a *app) loadSampleStory(p *lfPage) {
	voices := a.lfVoices(entryFor(p.doc.Model))
	ref := func(i int) string {
		if len(voices) == 0 {
			return ""
		}
		return voices[i%len(voices)].ref
	}
	p.doc.Title = i18n.T("The Night Ferry")
	if p.doc.Voice == "" {
		p.doc.Voice = ref(0)
	}
	ines, tomas := core.NewID(time.Now()), core.NewID(time.Now())
	p.doc.Cast = []services.CastMember{{ID: ines, Name: "Ines", Voice: ref(1)}, {ID: tomas, Name: "Tomas", Voice: ref(2)}}
	line := func(who, text string) services.StoryLine {
		return services.StoryLine{ID: newLineID(), Character: who, Text: text}
	}
	p.doc.Lines = []services.StoryLine{
		{ID: newLineID(), Chapter: true, Text: i18n.T("The Night Ferry")},
		line("", i18n.T("The last ferry left the harbour at eleven. [pause 500ms] Fog had settled on the water like wool.")),
		line(ines, i18n.T("Is this the boat to Harrow Point?")),
		line(tomas, "[slow]"+i18n.T("It is the only boat,")+"[/slow] "+i18n.T("the ferryman said. [laughter] Sit anywhere you like.")),
		line("", i18n.T("She chose the bench by the lantern, and watched the lights of the town go out one by one.")),
		{ID: newLineID(), Chapter: true, Text: i18n.T("Harrow Point")},
		line("", i18n.T("By morning the fog had lifted. [pause 1s] The island was smaller than the map promised.")),
		line(ines, "[fast]"+i18n.T("We're here already?")+"[/fast] [sigh] "+i18n.T("I slept the whole way.")),
		line(tomas, i18n.T("Everyone does. The crossing is [emphasis]longer[/emphasis] than it looks. Mind the sign: it says [spell]NO[/spell] swimming.")),
	}
	p.doc.Lines[7].PauseAfter = 800
	p.project, p.savedJSON, p.sel = "", "", -1
	p.open["cast"], p.open["review"] = true, false
}
