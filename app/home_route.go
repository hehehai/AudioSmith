package main

// Where the Home page's composer sends what it is given (0.5 F1, plan
// §1.1): plain text to Text to speech, in its script; a video file or a
// link to one to Dub, as the source of a new dub; an audio file to Speech
// to text, transcribed at once; a text file or an ebook to Audiobook, as
// its manuscript. With 「智能分派」 on (only while the writing help's model
// on this Mac is loaded: no 2.5 GB load for a guess), the model picks
// among Text to speech, Stories and Audiobook for plain text.

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/oplog"
)

// homeDest is a tool the composer sends to.
type homeDest string

const (
	homeStudio     homeDest = "studio"
	homeStories    homeDest = "stories"
	homeAudiobook  homeDest = "audiobook"
	homeDub        homeDest = "dub"
	homeTranscribe homeDest = "transcribe"
	homeNone       homeDest = ""
)

// homeSkill is the writing help's skill that picks a tool.
const homeSkill llm.Skill = "home_route"

var (
	homeVideoExts = []string{"mp4", "mov", "m4v", "mkv", "webm", "avi"}
	homeAudioExts = []string{"wav", "mp3", "m4a", "aac", "flac", "ogg", "opus", "aiff", "aif", "caf"}
	homeBookExts  = []string{"txt", "md", "markdown", "epub", "docx"}
)

// homeFileDest is where a file goes, by its kind; homeNone for one the
// tools do not take.
func homeFileDest(path string) homeDest {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch {
	case slices.Contains(homeVideoExts, ext):
		return homeDub
	case slices.Contains(homeAudioExts, ext):
		return homeTranscribe
	case slices.Contains(homeBookExts, ext):
		return homeAudiobook
	}
	return homeNone
}

// homeLink is text that is only a web link (a video to dub), "" else.
func homeLink(text string) string {
	s := strings.TrimSpace(text)
	if strings.ContainsAny(s, " \t\n") {
		return ""
	}
	if l := strings.ToLower(s); strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") {
		return s
	}
	return ""
}

// homeSubmit sends the composer's file, else its text, to its tool.
func (a *app) homeSubmit() {
	h := &a.homeUI
	if h.routing {
		return
	}
	if h.file != "" {
		path := h.file
		if !a.homeOpenFile(path) {
			a.toast(i18n.T("AudioSmith cannot open %s from Home: use a video, audio, text or ebook file.", filepath.Base(path)))
			return
		}
		h.file, h.text = "", ""
		return
	}
	text := strings.TrimSpace(h.text)
	if text == "" {
		return
	}
	if u := homeLink(text); u != "" {
		oplog.Info("home.route", "to", "dub", "source", "link")
		h.text = ""
		a.dubURL = u
		a.navigate("/dub")
		a.dubSubmit() // dub_list.go: a new dub of the link, started; or why not
		return
	}
	if h.smart && a.homeSmartReady() {
		a.homeSmartRoute(text)
		return
	}
	h.text = ""
	a.homeToStudio(text)
}

// homeOpenFile sends a file to its tool; false when no tool takes it.
func (a *app) homeOpenFile(path string) bool {
	dest := homeFileDest(path)
	oplog.Info("home.route", "to", string(dest), "source", "file")
	switch dest {
	case homeDub:
		a.navigate("/dub")
		a.dubStartFile(path) // a new dub of it, started (dub_list.go)
	case homeTranscribe:
		a.navigate("/transcripts")
		a.transcribeFile(path)
	case homeAudiobook:
		a.navigate("/audiobook")
		a.importBookFrom(a.lfBook, path) // as Import…, with Undo (audiobook.go)
	default:
		return false
	}
	return true
}

// homeToStudio opens Text to speech with text as its script.
func (a *app) homeToStudio(text string) {
	oplog.Info("home.route", "to", "studio", "source", "text")
	a.text = text // the editor takes it (syncScript)
	a.ttsUI.use = ""
	a.navigate("/studio")
}

// homeToTool sends plain text to dest (the smart routing's choice).
func (a *app) homeToTool(dest homeDest, text string) {
	switch dest {
	case homeStories:
		oplog.Info("home.route", "to", "stories", "source", "text")
		p := a.lfStories
		if strings.TrimSpace(p.doc.ImportText) != "" {
			text = p.doc.ImportText + "\n\n" + text
		}
		p.doc.ImportText = text // the review buffer: Auto-cast and Split from there
		a.navigate("/stories")
	case homeAudiobook:
		oplog.Info("home.route", "to", "audiobook", "source", "text")
		p := a.lfBook
		a.navigate("/audiobook")
		if strings.TrimSpace(p.doc.Script) == "" {
			a.setManuscript(p, text)
			return
		}
		old := p.doc.Script
		a.setManuscript(p, old+"\n\n"+text)
		a.toastUndo(i18n.T("Added to the manuscript"), func() { a.setManuscript(p, old) })
	default:
		a.homeToStudio(text)
	}
}

// homeSmartReady reports whether 「智能分派」 can run: the writing help's
// model on this Mac is loaded, so asking it costs no load.
func (a *app) homeSmartReady() bool {
	if a.llmRouter == nil || !a.localLLMReady() {
		return false
	}
	return slices.Contains(a.engineSummary().Loaded, a.localLLMEntry().Repo)
}

// homePrompt asks the model to pick a tool for a text.
const homePrompt = `You route a request in a voice studio to one tool. Answer with one word only:
studio — read the text aloud as it is (narration, an ad, a short script);
stories — a story or a dialogue with several characters to cast and perform;
audiobook — a long text in chapters to narrate as a book.`

// homeSmartRoute asks the writing help where text goes, then sends it
// there; Text to speech when it cannot tell.
func (a *app) homeSmartRoute(text string) {
	h := &a.homeUI
	h.routing = true
	router := a.llmRouter
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := router.Chat(ctx, homeSkill, llm.Request{
			Messages:    []llm.Message{{Role: llm.System, Content: homePrompt}, {Role: llm.User, Content: text}},
			Temperature: llm.F64(0), MaxTokens: 8,
		})
		dest := homeStudio
		if err == nil {
			dest = parseHomeDest(resp.Text)
		}
		oplog.Err("home.smart", err)
		a.post(func() {
			h.routing = false
			h.text = ""
			a.homeToTool(dest, text)
		})
	})
}

// parseHomeDest reads the model's answer: the first tool it names.
func parseHomeDest(s string) homeDest {
	s = strings.ToLower(s)
	best, at := homeStudio, len(s)+1
	for word, d := range map[string]homeDest{"studio": homeStudio, "stor": homeStories, "book": homeAudiobook} {
		if i := strings.Index(s, word); i >= 0 && i < at {
			best, at = d, i
		}
	}
	return best
}
