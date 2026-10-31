package main

// The Audiobook page's logic (/audiobook): the manuscript editor (the
// script markup with "# " chapters and [voice:NAME] highlighted), the
// markup toolbar, importing a book, the sample, the cast found in the
// manuscript, and the book's pronunciations.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui/editor"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/text/markup"
)

// maxBookBytes caps a manuscript import.
const maxBookBytes = 64 << 20

// newManuscriptEditor is the Audiobook's editor.
func newManuscriptEditor() *editor.State {
	buf := editor.NewBuffer("")
	buf.SetLanguage(manuscriptLanguage(&speakerSlots{}))
	return &editor.State{
		Buffer:          buf,
		SoftWrap:        true,
		LineNumbers:     editor.LineNumbersOff,
		NoFolding:       true,
		NoIndentGuides:  true,
		NoAutoClose:     true,
		NoActiveLine:    true,
		NoScrollPastEnd: true,
		LineHeight:      1.6,
		Label:           i18n.T("Manuscript"),
	}
}

// manuscriptLanguage highlights the markup of a manuscript: Studio's
// tags (tagKind), "# " chapter headings, [voice:] as valid, and dialogue
// passages, each speaker's name in the colour of its slot
// (audiobook_dialogue_editor.go).
func manuscriptLanguage(slots *speakerSlots) *editor.Language {
	return &editor.Language{
		Name: "AudioSmith manuscript",
		Tokenizer: editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
			return tokenizeManuscriptWith(slots, line, state, tokens)
		}),
	}
}

// syncManuscript reconciles the document's script and the editor.
func (a *app) syncManuscript(p *lfPage) {
	if p.ed == nil {
		return
	}
	if p.doc.Script != p.edSynced {
		if p.ed.Buffer.String() != p.doc.Script {
			p.ed.Buffer.Set(p.doc.Script)
		}
		p.edSynced, p.edVersion = p.doc.Script, p.ed.Buffer.Version()
		p.scriptRev++
		return
	}
	if v := p.ed.Buffer.Version(); v != p.edVersion {
		p.edVersion = v
		p.doc.Script = p.ed.Buffer.String()
		p.edSynced = p.doc.Script
		p.scriptRev++
	}
}

// setManuscript replaces the manuscript as one undoable edit.
func (a *app) setManuscript(p *lfPage, s string) {
	p.doc.Script = s
	a.syncManuscript(p)
}

// insertMarkup inserts markup at the caret (replacing the selection).
func (a *app) insertMarkup(p *lfPage, s string) {
	a.syncManuscript(p)
	p.ed.Insert(s)
	a.syncManuscript(p)
}

// wrapMarkup wraps the selection in open … close.
func (a *app) wrapMarkup(p *lfPage, open, close string) {
	sel := p.ed.SelectedText()
	a.insertMarkup(p, open+sel+close)
	if sel == "" {
		// The caret between the tags, ready to type.
		at := p.ed.Cursor() - len(close)
		p.ed.Select(at, at)
	}
}

// insertVoice inserts [voice:NAME] with NAME selected, to type over.
func (a *app) insertVoice(p *lfPage) {
	name := i18n.T("NAME")
	a.insertMarkup(p, "[voice:"+name+"]")
	end := p.ed.Cursor() - 1
	p.ed.Select(end-len(name), end)
}

// importBook asks for a book and replaces the manuscript with it.
func (a *app) importBook(p *lfPage) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Import a book"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Books"), Extensions: []string{"txt", "md", "markdown", "epub", "docx"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.importBookFrom(p, paths[0]) })
	}()
}

// importBookFrom reads a book in the background (chapterised) and makes
// it the manuscript, offering Undo.
func (a *app) importBookFrom(p *lfPage, path string) {
	if p.importing {
		return
	}
	p.importing, p.opened, p.listing = true, true, false
	a.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		d, err := doc.Open(ctx, path, doc.Limits{MaxFileBytes: maxBookBytes})
		a.post(func() {
			p.importing = false
			if err != nil {
				a.toastWarn(bookImportError(path, err))
				return
			}
			s := applyBook(&p.doc, d)
			a.setManuscript(p, s)
			n := len(markup.Parse(s, nil).Chapters())
			a.composer.undoToast = "" // not the Studio's
			a.toastUndo(i18n.N(n, "Imported %[2]s · %[1]d chapter", "Imported %[2]s · %[1]d chapters", n, filepath.Base(path)), func() {
				p.ed.Buffer.Undo()
				a.syncManuscript(p)
			})
		})
	})
}

// applyBook takes an imported book's title, author and language into an
// audiobook's document where it has none, and returns its manuscript
// (the API's books use it too).
func applyBook(d *services.LongformDoc, b *doc.Document) string {
	if d.Title == "" && b.Title != "" {
		d.Title = b.Title
	}
	if d.Book.Author == "" && b.Author != "" {
		d.Book.Author = b.Author
	}
	if b.Language != "" && d.Language == "" {
		d.Language = b.Language
	}
	return b.Script()
}

// bookImportError explains why a book could not be imported.
func bookImportError(path string, err error) string {
	name := filepath.Base(path)
	switch {
	case errors.Is(err, doc.ErrUnsupported):
		return i18n.T("%s: this kind of file cannot be imported (PDF comes later).", name)
	case errors.Is(err, doc.ErrTooLarge):
		return i18n.T("%s is too large to import (64 MB at most).", name)
	case errors.Is(err, doc.ErrNoText):
		return i18n.T("%s has no text to import.", name)
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("%s was not found.", name)
	}
	return i18n.T("Could not import %s: %v", name, err)
}

// toastUndo shows a toast with Undo on the next build.
func (a *app) toastUndo(msg string, undo func()) {
	a.lfUndo = append(a.lfUndo, lfUndoToast{msg, undo})
}

// lfUndoToast is a toast with an Undo action.
type lfUndoToast struct {
	msg  string
	undo func()
}

// sampleManuscript is the Audiobook's sample: chapters, two voices,
// pauses, styles and reactions.
func sampleManuscript() string {
	return i18n.T("# The Night Ferry") + "\n\n" +
		i18n.T("The last ferry left the harbour at eleven. [pause 500ms] Fog had settled on the water like wool.") + "\n\n" +
		"[voice:Ines]" + i18n.T("Is this the boat to Harrow Point?") + "\n\n" +
		"[voice:Tomas][slow]" + i18n.T("It is the only boat,") + "[/slow] " + i18n.T("the ferryman said. [laughter] Sit anywhere you like.") + "\n\n" +
		"[voice:default]" + i18n.T("She chose the bench by the lantern, and watched the lights of the town go out one by one.") + "\n\n" +
		i18n.T("# Harrow Point") + "\n\n" +
		i18n.T("By morning the fog had lifted. [pause 1s] The island was smaller than the map promised.") + "\n\n" +
		"[voice:Ines][fast]" + i18n.T("We're here already?") + "[/fast] [sigh] " + i18n.T("I slept the whole way.") + "\n\n" +
		"[voice:Tomas]" + i18n.T("Everyone does. The crossing is [emphasis]longer[/emphasis] than it looks. Mind the sign: it says [spell]NO[/spell] swimming.")
}

// loadSample fills an empty manuscript with the sample.
func (a *app) loadSample(p *lfPage) {
	a.setManuscript(p, sampleManuscript())
	if p.doc.Title == "" {
		p.doc.Title = i18n.T("The Night Ferry")
	}
	// Cast the two voices from the library when there are some.
	e := entryFor(p.doc.Model)
	vs := a.lfVoices(e)
	if p.doc.VoiceCast == nil {
		p.doc.VoiceCast = map[string]string{}
	}
	for i, name := range []string{"Ines", "Tomas"} {
		if p.doc.VoiceCast[name] == "" && len(vs) > 0 {
			p.doc.VoiceCast[name] = vs[(i+1)%len(vs)].ref
		}
	}
	if p.doc.Voice == "" && len(vs) > 0 {
		p.doc.Voice = vs[0].ref
	}
}

// bookCast are the names the manuscript casts with [voice:NAME] and in
// its dialogue passages (audiobook_dialogue.go).
func bookCast(p *lfPage) []string {
	if p.cast != nil && p.lfc.deferring {
		// A large manuscript's compile is pending: the cast it last found
		// (longform_compile.go finds it again with the compile).
		return p.cast
	}
	if p.cast == nil || p.castRev != p.scriptRev {
		p.cast, p.castRev = castNamesOf(p.doc.Script), p.scriptRev
	}
	return p.cast
}

func castNamesOf(script string) []string {
	out := []string{}
	for _, n := range markup.Parse(script, nil).VoiceNames() {
		if !strings.EqualFold(n, "default") && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	for _, n := range services.DialogueNames(script) {
		if !slices.ContainsFunc(out, func(o string) bool { return markup.NameKey(o) == markup.NameKey(n) }) {
			out = append(out, n)
		}
	}
	return out
}
