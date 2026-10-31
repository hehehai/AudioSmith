package main

// The Studio's script editor: a ui/editor View of the script with the
// markup highlighted (pauses, non-verbal tags, styles, overrides, unknown
// tags), its findings underlined, and the text kept in a.text (which the
// rest of the app reads and may set). Import script reads a document
// (internal/doc) in the background and inserts it at the caret; Undo
// takes it back.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/text"
	"github.com/hehehai/AudioSmith/internal/text/markup"
	"github.com/hehehai/AudioSmith/internal/text/plan"
)

// scriptEditor is the editor's state and what is derived from its text.
type scriptEditor struct {
	st *editor.State
	// synced is the text the buffer and a.text last agreed on.
	synced string
	// version is the buffer version synced was read at.
	version uint64

	// stats of the text, recomputed when it changes.
	statsFor string
	statsKey string
	stats    scriptStats
}

// scriptStats is the stats line and the readiness of the script.
type scriptStats struct {
	chars   int     // runes, markup included
	words   int     // spaced-script words
	unspace int     // unspaced-script characters
	seconds float64 // estimated audio, pauses included, at speed 1
	chunks  int     // engine calls
	diags   []text.Diagnostic
}

func newScriptEditor() scriptEditor {
	buf := editor.NewBuffer("")
	buf.SetLanguage(scriptLanguage)
	return scriptEditor{st: &editor.State{
		Buffer:          buf,
		SoftWrap:        true,
		LineNumbers:     editor.LineNumbersOff,
		NoFolding:       true,
		NoIndentGuides:  true,
		NoAutoClose:     true,
		NoActiveLine:    true,
		NoScrollPastEnd: true,
		LineHeight:      1.6,
		Label:           i18n.T("Script"),
	}}
}

// reset replaces the text and forgets the undo history (a restored draft).
func (s *scriptEditor) reset(t string) {
	s.st.Buffer.Reset(t)
	s.synced, s.version = t, s.st.Buffer.Version()
}

// set replaces the text as one undoable edit (Reuse).
func (s *scriptEditor) set(t string) {
	if s.st.Buffer.String() == t {
		return
	}
	s.st.Buffer.Set(t)
	s.synced, s.version = t, s.st.Buffer.Version()
}

// scriptFocused reports whether the script editor has the keyboard focus
// (window shortcuts that are typed characters, as "?", leave it the key).
func (a *app) scriptFocused() bool {
	return a.script.st != nil && a.script.st.Focused() && a.currentPage() == "studio"
}

// syncScript reconciles a.text and the buffer: text set elsewhere (tests,
// Reuse, the palette) goes into the buffer; edits in the editor come out.
func (a *app) syncScript() {
	s := &a.script
	if a.text != s.synced {
		s.set(a.text)
		s.synced = a.text
		return
	}
	if v := s.st.Buffer.Version(); v != s.version {
		s.version = v
		a.text = s.st.Buffer.String()
		s.synced = a.text
	}
}

// scriptLanguage highlights the markup.
var scriptLanguage = &editor.Language{
	Name:      "AudioSmith script",
	Tokenizer: editor.TokenizerFunc(tokenizeScript),
}

// tokenizeScript colours one line's markup. Tags never span lines.
func tokenizeScript(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	pos := 0
	for _, sp := range text.Tags(line) {
		if sp.Start > pos {
			tokens = append(tokens, editor.Token{End: sp.Start, Kind: editor.TokenText})
		}
		tokens = append(tokens, editor.Token{End: sp.End, Kind: tagKind(sp.Of(line))})
		pos = sp.End
	}
	if pos < len(line) {
		tokens = append(tokens, editor.Token{End: len(line), Kind: editor.TokenText})
	}
	return tokens, state
}

// scriptTheme colours the script's markup with the design's few colours:
// the accent for timing and delivery, greys for expressions and what the
// model ignores, the warning colour for what belongs elsewhere.
func scriptTheme(t *ui.Theme) *editor.Theme {
	k := tok(t)
	th := editor.DefaultTheme(t)
	th.Cursor = k.Text
	for kind, col := range map[editor.TokenKind]ui.Color{
		editor.TokenKeyword:  k.AccentInk,
		editor.TokenType:     k.AccentInk,
		editor.TokenFunction: k.AccentInk,
		editor.TokenString:   k.Text2,
		editor.TokenInvalid:  t.Warning,
		editor.TokenComment:  k.Text3,
	} {
		th.Tokens[kind].Color = col
	}
	return th
}

// tagKind is how a tag is coloured.
func tagKind(tag string) editor.TokenKind {
	if strings.HasPrefix(tag, "[[") {
		return editor.TokenFunction // [[word|say]]
	}
	inner := strings.ToLower(strings.TrimSpace(tag[1 : len(tag)-1]))
	name := strings.TrimPrefix(inner, "/")
	if i := strings.IndexAny(name, " :="); i >= 0 {
		name = name[:i]
	}
	switch {
	case name == "pause":
		return editor.TokenKeyword
	case name == markup.Slow || name == markup.Fast || name == markup.Emphasis || name == markup.Spell:
		return editor.TokenType
	case name == "voice":
		return editor.TokenInvalid // Stories only
	case slices.Contains(markup.DefaultNonVerbal, inner):
		return editor.TokenString
	}
	return editor.TokenComment
}

// scriptStats returns the stats of the script for the form's model,
// recomputed when the text or the model changed. Planning is pure and
// fast (no normalisation: it only counts chunks and finds markup that
// will not work with the model).
func (a *app) scriptStats() scriptStats {
	s := &a.script
	tr := a.traits()
	key := a.preset().Repo
	if s.statsFor == a.text && s.statsKey == key {
		return s.stats
	}
	s.statsFor, s.statsKey = a.text, key
	doc := markup.Parse(a.text, &markup.Options{NoChapters: true})
	st := doc.Stats()
	pl := plan.Build(a.text, plan.Options{NonVerbal: tr.NonVerbal, Speed: tr.Speed, MaxChars: tr.MaxChars,
		MaxCharsCJK: tr.MaxCharsCJK, NoNormalize: true})
	s.stats = scriptStats{
		chars: utf8.RuneCountInString(strings.TrimSpace(a.text)), words: st.Words, unspace: st.Chars,
		seconds: pl.Seconds(1), chunks: len(pl.Speech()),
	}
	for _, d := range pl.Diags {
		if d.Code != plan.CodeNothingToSay {
			s.stats.diags = append(s.stats.diags, d)
		}
	}
	// Underline them in the editor.
	ds := make([]editor.Diagnostic, 0, len(s.stats.diags))
	for _, d := range s.stats.diags {
		if d.Span.End <= d.Span.Start {
			continue
		}
		sev := editor.SeverityWarning
		if d.Severity == text.Info {
			sev = editor.SeverityInfo
		}
		ds = append(ds, editor.Diagnostic{Start: d.Span.Start, End: d.Span.End, Severity: sev, Message: diagText(d), Source: "AudioSmith"})
	}
	if s.st.Buffer.String() == a.text {
		s.st.SetDiagnostics(s.st.Buffer.Version(), ds)
	}
	return s.stats
}

// scriptView builds the editor.
func (a *app) scriptView(c *ui.Context) {
	t := c.Theme()
	a.syncScript()
	st := a.script.st
	st.Font = ui.Font{Family: "system-ui", Size: px(t, 15)}
	st.Theme = scriptTheme(t)
	st.Placeholder = i18n.T("Type or paste the text to speak…")
	st.Label = i18n.T("Script")
	// No box (0.5 F3): the script is the page, as ElevenLabs' is.
	box := ui.Column(c.Key("script-box")).Grow(1).MinHeight(px(t, 120)).Background(editor.DefaultTheme(t).Background).
		Padding(px(t, sp2), 0)
	box.Children(func() {
		editor.View(c.Key("script"), st).Grow(1).MinHeight(0)
	})
	a.syncScript()
}

// insertScript inserts text at the caret (replacing the selection), as
// one undoable edit, and focuses the editor.
func (a *app) insertScript(s string) {
	a.syncScript()
	a.script.st.Insert(s)
	a.syncScript()
}

// wrapSelection wraps the selection in open … close, or inserts both
// around nothing at the caret.
func (a *app) wrapSelection(open, close string) {
	sel := a.script.st.SelectedText()
	a.insertScript(open + sel + close)
}

// importScript asks for a document and inserts its text at the caret.
func (a *app) importScript() {
	go func() {
		var exts []string
		for _, e := range doc.Extensions() {
			exts = append(exts, strings.TrimPrefix(e, "."))
		}
		slices.Sort(exts)
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.win,
			Title:   i18n.T("Import script"),
			Filters: []mygo.FileFilter{{Name: i18n.T("Documents and subtitles"), Extensions: exts}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.importScriptFrom(paths[0])
	}()
}

// importScriptFrom reads a document in the background and inserts its
// script at the caret, offering Undo.
func (a *app) importScriptFrom(path string) {
	a.background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		d, err := doc.Open(ctx, path, doc.Limits{})
		a.post(func() {
			if err != nil {
				a.toastWarn(importError(path, err))
				return
			}
			s := studioScriptOf(d)
			if s == "" {
				a.toast(i18n.T("%s has no text to import.", filepath.Base(path)))
				return
			}
			if !strings.HasSuffix(a.textBeforeCaret(), "\n") && a.text != "" {
				s = "\n\n" + s
			}
			a.insertScript(s)
			a.composer.undoToast = i18n.T("Imported %s · %d characters", filepath.Base(path), utf8.RuneCountInString(s))
		})
	})
}

// studioScriptOf is a document as a Studio script: chapter titles become
// paragraphs of their own (the Studio has no chapters), paragraphs stay
// apart.
func studioScriptOf(d *doc.Document) string {
	var parts []string
	for _, ch := range d.Chapters {
		if t := strings.TrimSpace(ch.Title); t != "" {
			parts = append(parts, t)
		}
		if t := strings.TrimSpace(ch.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}

// textBeforeCaret is the script up to the caret.
func (a *app) textBeforeCaret() string {
	c := min(a.script.st.Cursor(), len(a.text))
	return a.text[:max(c, 0)]
}

// undoScript takes back the last edit of the script (an import).
func (a *app) undoScript() {
	a.script.st.Buffer.Undo()
	a.syncScript()
}

// importError explains why a document could not be imported.
func importError(path string, err error) string {
	name := filepath.Base(path)
	switch {
	case errors.Is(err, doc.ErrUnsupported):
		return i18n.T("%s: this kind of file cannot be imported (PDF comes later).", name)
	case errors.Is(err, doc.ErrTooLarge):
		return i18n.T("%s is too large to import.", name)
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("%s was not found.", name)
	}
	return i18n.T("Could not import %s: %v", name, err)
}
