package main

// Behaviour tests of 0.5 F5: Projects (samples that make and open a
// project, the type filter, list and cards, rename, duplicate, delete),
// Assets (the type filter, ▶, send to a tool), ⌘K (quick actions, recent,
// groups, a voice into Text to speech, the read-aloud row) and the
// Stories and Audiobook pages' composer over their list.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/services"
)

// A sample card makes the right kind of project and opens it (the
// built-in fallbacks: no sample bundled; TestBundledSamplesOpen opens the
// bundled ones).
func TestProjectsTemplates(t *testing.T) {
	useSamples(t, fstest.MapFS{})
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	a.navigate("/projects")
	tt.Frame()
	for _, s := range []string{"Samples", "A story for two voices", "An audiobook chapter", "A product narration", "Coming soon", "No projects yet"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	if err := tt.Click("A story for two voices"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	p := a.lfStories
	if p.project == "" || a.router.Path() != "/stories" || len(p.doc.Lines) < 5 || len(p.doc.Cast) != 2 || !a.lfEditing(p) {
		t.Fatalf("story: project %q path %s lines %d", p.project, a.router.Path(), len(p.doc.Lines))
	}
	if !tt.HasText("Stories /") || !tt.HasText("The Night Ferry") {
		t.Errorf("editor header: %q", tt.Texts())
	}
	// The list's name goes back to the list, which lists it.
	if err := tt.Click("Back to Stories"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.lfEditing(p) || !tt.HasText("The Night Ferry") || !tt.HasText("New story") {
		t.Fatalf("list: editing %v %q", a.lfEditing(p), tt.Texts())
	}
	a.navigate("/projects")
	tt.Frame()
	if err := tt.Click("An audiobook chapter"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b := a.lfBook; b.project == "" || a.router.Path() != "/audiobook" || !strings.Contains(b.doc.Script, "Ferry") {
		t.Fatalf("book: %q on %s", b.project, a.router.Path())
	}
	a.navigate("/projects")
	tt.Frame()
	if err := tt.Click("A product narration"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/studio" || !strings.Contains(a.text, "headphones") {
		t.Fatalf("narration: %s %q", a.router.Path(), a.text)
	}
	a.navigate("/projects")
	tt.Frame()
	a.toasts = nil
	if err := tt.Click("A dubbed clip"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("The sample projects come with the next update.") {
		t.Errorf("dub sample: %q", tt.Texts())
	}
}

// The list: every kind, the type filter, cards, rename, duplicate, delete.
func TestProjectsListFlow(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	ctx := context.Background()
	for _, k := range []core.ProjectKind{core.ProjectStory, core.ProjectAudiobook} {
		d := services.NewLongformDoc(k, kokoro)
		d.Title = "Saved " + string(k)
		if _, err := a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: k, Title: d.Title}, d); err != nil {
			t.Fatal(err)
		}
	}
	tr := sampleTranscript(core.NewID(time.Now()), time.Now())
	tr.Title = "Team meeting.m4a"
	putTranscript(t, a, tr)
	a.navigate("/projects")
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"3 items", "Saved story", "Saved audiobook", "Team meeting.m4a", "Transcript", "Draft"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	// The type filter, from the header.
	if err := tt.Click("Type"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Audiobook"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	if !tt.HasText("Saved audiobook") || tt.HasText("Saved story") || tt.HasText("Team meeting.m4a") || !tt.HasText("1 found") {
		t.Fatalf("filter: %q", tt.Texts())
	}
	a.projects.filter = 0
	// Cards.
	if err := tt.Click("Cards (⌥2)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.projects.cards != 1 || !tt.HasText("Saved story") {
		t.Fatalf("cards: %q", tt.Texts())
	}
	if err := tt.Click("Saved story"); err != nil { // a card opens it
		t.Fatal(err)
	}
	tt.Frame()
	if a.router.Path() != "/stories" || a.lfStories.doc.Title != "Saved story" {
		t.Fatalf("card: %s %q", a.router.Path(), a.lfStories.doc.Title)
	}
	a.navigate("/projects")
	tt.Frame()
	if err := tt.Click("List (⌥1)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Rename and duplicate the book.
	book := a.projItems(projBook, "")[0]
	a.projects.renaming, a.projects.renameText = book.key(), "Renamed book"
	tt.Frame()
	if err := tt.Click("Save name"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if got, _ := a.lib.svc.Projects.Get(ctx, book.id); got.Title != "Renamed book" {
		t.Errorf("rename: %q", got.Title)
	}
	a.duplicateProj(book)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Renamed book (copy)") {
		t.Errorf("duplicate: %q", tt.Texts())
	}
	// Delete the transcript, asked first.
	a.projects.filter = slices.Index(projFilters, projTranscript)
	tt.Frame()
	chooseAway(t, tt, "Project actions", "Delete…")
	if !a.projects.confirmOpen || !tt.HasText("Delete “Team meeting.m4a”?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.trList) != 0 {
		t.Errorf("transcript left: %d", len(a.trList))
	}
}

// Assets: takes, stems, the recording transcribed; the type filter, ▶,
// send to a tool's composer, the read-aloud of a take's script.
func TestAssetsFlow(t *testing.T) {
	a, _, tt, _ := isoTest(t)
	tt.SetSize(1200, 900)
	addTake(t, a, core.Take{ID: "T1", CreatedAt: time.Now().Add(-time.Hour), Text: "The quick brown fox.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	src := filepath.Join(t.TempDir(), "Song.mp4")
	os.WriteFile(src, []byte("video"), 0o644)
	a.isoStart(src)
	tr := sampleTranscript(core.NewID(time.Now()), time.Now())
	putTranscript(t, a, tr)
	a.navigate("/assets")
	tt.Frame()
	tt.Frame()
	for _, s := range []string{"The quick brown fox.", "Song · Voices", "Song · Music and effects", "interview.m4a", "Song.mp4", "Voice isolator", "Text to speech"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	// The type filter: the stems only.
	if err := tt.Click("Type"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Stems"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	if tt.HasText("The quick brown fox.") || tt.HasText("interview.m4a") || !tt.HasText("Song · Voices") {
		t.Fatalf("stems: %q", tt.Texts())
	}
	// ▶ plays the first stem.
	if err := tt.Click("Play"); err != nil {
		t.Fatal(err)
	}
	if cur := a.pl.Current(); !strings.HasPrefix(cur, "stem:") {
		t.Fatalf("playing %q (%q)", cur, a.toasts)
	}
	a.pl.Stop()
	// Send to Dub: the file waits in Dub's composer.
	if err := tt.Click("Asset actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Send to", "Dub"); err != nil {
		t.Fatalf("%v: %q", err, tt.Menu())
	}
	tt.Frame()
	if a.router.Path() != "/dub" || filepath.Dir(a.dubPend) != filepath.Dir(a.isoSvc.Path(a.isoList[0].ID, a.isoList[0].Vocals)) {
		t.Fatalf("send to dub: %s %q", a.router.Path(), a.dubPend)
	}
	a.dubPend = ""
	// The recording transcribed: Send to Voice isolator.
	a.navigate("/assets")
	a.history.kind = slices.Index(assetKinds, assetImport)
	a.history.source = slices.Index(assetSources, "/transcripts")
	tt.Frame()
	if tt.HasText("Song.mp4") || !tt.HasText("interview.m4a") {
		t.Fatalf("imported from Speech to text: %q", tt.Texts())
	}
	tt.Click("Asset actions")
	if err := tt.ChooseMenuItem("Send to", "Voice isolator"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/isolate" || a.isoPend != a.trSvc.AudioPath(tr) {
		t.Fatalf("send to isolator: %s %q", a.router.Path(), a.isoPend)
	}
	a.isoPend = ""
	// A take: Send to Text to speech (its script), the source opens it.
	a.navigate("/assets")
	a.history.kind, a.history.source = slices.Index(assetKinds, assetSpeech), 0
	tt.Frame()
	tt.Click("Asset actions")
	if err := tt.ChooseMenuItem("Send to", "Text to speech (the text)"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/studio" || a.text != "The quick brown fox." {
		t.Fatalf("send to studio: %s %q", a.router.Path(), a.text)
	}
}

// ⌘K: quick actions and recent when empty; grouped results; a voice
// goes to Text to speech; the last row reads the query aloud.
func TestPaletteSearchEverything(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	ctx := context.Background()
	addTake(t, a, core.Take{ID: "T1", CreatedAt: time.Now(), Text: "The last ferry left at nine.", Model: kokoro, Voice: "af_heart", AudioSeconds: 1}, 0.3)
	d := services.NewLongformDoc(core.ProjectStory, kokoro)
	d.Title = "Ferry tales"
	a.lib.svc.Library.SaveDoc(ctx, core.Project{Kind: core.ProjectStory, Title: d.Title}, d)
	v, err := a.lib.svc.Voices.Save(ctx, core.Voice{Name: "Mara", Kind: core.VoiceCloned, RefText: "Hello."})
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.openPalette()
	tt.Frame()
	for _, s := range []string{"Quick actions", "Generate speech", "Dub a video", "Transcribe a recording", "New voice", "New story", "New audiobook", "Recent", "The last ferry left at nine."} {
		if !tt.HasText(s) {
			t.Fatalf("empty palette lacks %q: %q", s, tt.Texts())
		}
	}
	// New story opens Stories' composer.
	rows := a.paletteRows()
	i := slices.IndexFunc(rows, func(r paletteRow) bool { return r.key == "q-story" })
	a.runPaletteRow(rows[i])
	tt.Frame()
	if a.router.Path() != "/stories" || a.lfEditing(a.lfStories) || !tt.HasText("Describe a story in a sentence, or paste a script…") {
		t.Fatalf("new story: %s %q", a.router.Path(), tt.Texts())
	}
	// Typing: grouped results.
	a.openPalette()
	a.paletteQuery = "ferry"
	tt.Frame()
	groups := map[string]bool{}
	for _, r := range a.paletteRows() {
		groups[r.group] = true
	}
	for _, g := range []string{"Projects", "Assets", "Use the text"} {
		if !groups[g] {
			t.Errorf("no group %q: %v", g, groups)
		}
	}
	if !tt.HasText("Ferry tales") || !tt.HasText("Read “ferry” in Text to speech") || !tt.HasText("Open ↵") {
		t.Errorf("typed: %q", tt.Texts())
	}
	a.paletteQuery = "stories"
	if rows := a.paletteRows(); len(rows) == 0 || rows[0].group != "Pages" || rows[0].title != "Stories" {
		t.Errorf("pages first: %+v", rows)
	}
	// A voice: Enter uses it in Text to speech.
	a.paletteQuery, a.paletteSel = "mara", 0
	tt.Frame()
	rows = a.paletteRows()
	if len(rows) == 0 || rows[0].group != "Voices" || rows[0].title != "Mara" {
		t.Fatalf("voices: %+v", rows)
	}
	tt.SetFocused(true)
	tt.Click("Search commands")
	tt.Key(0, ui.KeyEnter)
	if a.paletteOpen || a.router.Path() != "/studio" || a.studioVoice != v.ID {
		t.Fatalf("voice: open %v path %s voice %q", a.paletteOpen, a.router.Path(), a.studioVoice)
	}
	// The read-aloud row fills the script.
	a.openPalette()
	a.paletteQuery = "Hello from the palette"
	rows = a.paletteRows()
	last := rows[len(rows)-1]
	if last.key != "x-read" {
		t.Fatalf("last row %+v", last)
	}
	a.runPaletteRow(last)
	if a.router.Path() != "/studio" || a.text != "Hello from the palette" {
		t.Errorf("read: %s %q", a.router.Path(), a.text)
	}
	// No writing help loaded: no row for it.
	if slices.ContainsFunc(rows, func(r paletteRow) bool { return r.key == "x-smart" }) {
		t.Error("a writing-help row without its model")
	}
}

// Stories and Audiobook: the composer makes a project and opens it; the
// list shows it; going back saves what changed.
func TestLongformComposer(t *testing.T) {
	a, _, tt := newTestApp(t)
	tt.SetSize(1200, 900)
	a.navigate("/stories")
	tt.Frame()
	p := a.lfStories
	if !tt.HasText("No stories yet") || !tt.HasText("Describe a story in a sentence, or paste a script…") {
		t.Fatalf("entry: %q", tt.Texts())
	}
	p.entryText = "Two friends meet on a night train.\nThey talk until dawn."
	tt.Frame()
	if err := tt.Click("New story"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.project == "" || !a.lfEditing(p) || p.doc.Title != "Two friends meet on a night train" || !strings.Contains(p.doc.ImportText, "until dawn") || p.entryText != "" {
		t.Fatalf("made: %q editing %v title %q", p.project, a.lfEditing(p), p.doc.Title)
	}
	id := p.project
	// An edit, then ‹: saved, closed, listed.
	p.doc.ImportText += "\nThe end."
	tt.Frame()
	if err := tt.Click("Back (⌘[)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.lfEditing(p) || !tt.HasText("Two friends meet on a night train") {
		t.Fatalf("list: %q", tt.Texts())
	}
	_, d, err := a.lib.svc.Library.LoadDoc(context.Background(), id)
	if err != nil || !strings.Contains(d.ImportText, "The end.") {
		t.Fatalf("not saved: %v %q", err, d.ImportText)
	}
	if err := tt.Click("Two friends meet on a night train"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p.project != id || !a.lfEditing(p) {
		t.Fatalf("reopened %q", p.project)
	}

	// Audiobook: a manuscript and its narrator.
	a.navigate("/audiobook")
	tt.Frame()
	b := a.lfBook
	if !tt.HasText("No audiobooks yet") || !tt.HasText("Narrator voice") {
		t.Fatalf("book entry: %q", tt.Texts())
	}
	b.entryText = "Chapter One\n\nThe fog rolled in over the river."
	b.entryVoice = "speaker:Vivian"
	tt.Frame()
	if err := tt.Click("New audiobook"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b.project == "" || !strings.Contains(b.doc.Script, "fog rolled in") || b.doc.Voice != "speaker:Vivian" || b.doc.Title != "Chapter One" {
		t.Fatalf("book: %q %q %q", b.project, b.doc.Voice, b.doc.Title)
	}
	if !tt.HasText("Audiobook /") || !tt.HasText("Chapter One") {
		t.Errorf("book header: %q", tt.Texts())
	}
	// Both are on Projects.
	a.navigate("/projects")
	tt.Frame()
	if !tt.HasText("Chapter One") || !tt.HasText("Two friends meet on a night train") {
		t.Errorf("projects: %q", tt.Texts())
	}
}
