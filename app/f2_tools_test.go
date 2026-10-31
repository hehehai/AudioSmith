package main

// Behaviour tests of 0.5 F2's tool pages: Dub, Speech to text and Voice
// isolator as a composer over a list; a row opens the item; the
// hand-offs land in Dub and Text to speech with the media or the text.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// The composer makes a dub and starts it at once: the row is on top of
// the list, a click opens the editor, ← Dub goes back.
func TestDubComposerFlow(t *testing.T) {
	d := newDubTest(t, 3)
	a, tt := d.a, d.tt
	tt.Frame()
	for _, s := range []string{"Drop a video or audio file here", "No dubs yet", "Into: Spanish"} {
		if !tt.HasText(s) {
			t.Fatalf("empty page lacks %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("Dub") && strings.Count(strings.Join(tt.Texts(), "\n"), "\nDub\n") > 3 {
		t.Log(tt.Texts())
	}
	a.dubPend = d.src
	tt.Frame()
	if !tt.HasText("Interview.wav") {
		t.Fatalf("file chip: %q", tt.Texts())
	}
	d.click(t, "Dub (↩)")
	if p := a.router.Path(); p != "/dub" {
		t.Fatalf("left the list: %s", p)
	}
	if len(a.dubList) != 1 || a.dubList[0].Source != d.src || a.dubPend != "" {
		t.Fatalf("list %+v", a.dubList)
	}
	// It started: the fake pipeline ran to the transcript (review on).
	if a.dub == nil || len(a.dub.Segments) != 3 || !slices3(a.dub.Targets, "es") {
		t.Fatalf("not started: %+v", a.dub)
	}
	tt.Frame()
	if !tt.HasText("Interview") || !tt.HasText("Transcribed") {
		t.Errorf("row: %q", tt.Texts())
	}
	id := a.dubList[0].ID
	d.click(t, "Interview")
	if p := a.router.Path(); p != "/dub/"+id {
		t.Fatalf("row click: %s", p)
	}
	if !tt.HasText("Hello there.") || !tt.HasText("Dub /") || !tt.HasText("Interview") {
		t.Errorf("editor: %q", tt.Texts())
	}
	d.click(t, "Back to Dub") // the list's name before the title
	if p := a.router.Path(); p != "/dub" {
		t.Fatalf("back: %s", p)
	}
	d.click(t, "Interview")
	d.click(t, "Back (⌘[)") // the header's ‹
	if p := a.router.Path(); p != "/dub" {
		t.Fatalf("‹: %s", p)
	}
	// Filters: a failed filter hides it; the search finds it.
	a.dubFilter = 3
	tt.Frame()
	if !tt.HasText("No dubs match.") {
		t.Errorf("failed filter: %q", tt.Texts())
	}
	a.dubFilter, a.dubQuery = 0, "inter"
	tt.Frame()
	if !tt.HasText("Interview") {
		t.Errorf("search: %q", tt.Texts())
	}
	// Export… opens the editor at Export; Delete removes it.
	a.dubWant = dubStepExport
	a.openDub(id)
	tt.Frame()
	if a.dubStep != dubStepExport {
		t.Errorf("export step %d", a.dubStep)
	}
	a.navigate("/dub")
	a.deleteDub(id)
	tt.Frame()
	if len(a.dubList) != 0 || !tt.HasText("No dubs yet") {
		t.Errorf("after delete: %+v", a.dubList)
	}
}

func slices3(l []string, s string) bool { return len(l) == 1 && l[0] == s }

// A link through the composer makes a dub of it, or says why not.
func TestDubComposerLink(t *testing.T) {
	d := newDubTest(t, 2)
	a, tt := d.a, d.tt
	a.dubURL = "not a link"
	tt.Frame()
	d.click(t, "Dub (↩)")
	if a.dubErr == "" || len(a.dubList) != 0 {
		t.Fatalf("bad link: %q %d", a.dubErr, len(a.dubList))
	}
	if !tt.HasText(a.dubErr) {
		t.Errorf("error not shown: %q", tt.Texts())
	}
}

// Speech to text: the composer's file → the running row → the list's
// row → the transcript; its hand-offs reach Dub and Text to speech.
func TestTranscribeComposerFlow(t *testing.T) {
	m := newM2Test(t)
	a, eng, tt := m.a, m.eng, m.tt
	a.navigate("/transcripts")
	tt.Frame()
	if !tt.HasText("No transcripts yet") || !tt.HasText("Drop audio or video here") {
		t.Fatalf("empty: %q", tt.Texts())
	}
	src := filepath.Join(t.TempDir(), "meeting.m4a")
	os.WriteFile(src, []byte("m4a"), 0o644)
	a.trPend = src
	tt.Frame()
	if err := tt.Click("Transcribe (↩)"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	r, req := transcribeReq(t, eng)
	if req.Input != src {
		t.Fatalf("request %+v", req)
	}
	eng.progress(r.ID, "transcribing", 0.4, 0)
	tt.Frame()
	if !tt.HasText("meeting.m4a") || !tt.HasText("Transcribing") {
		t.Fatalf("running row: %q", tt.Texts())
	}
	tr := sampleTranscript(req.ID, time.Now())
	tr.Title, tr.SourcePath = "meeting.m4a", src
	putTranscript(t, a, tr)
	eng.succeed(r.ID, services.TranscribeResult{Transcript: tr})
	tt.Frame()
	// The list was shown: the transcript opens.
	if p := a.router.Path(); p != "/transcripts/"+tr.ID {
		t.Fatalf("after: %s", p)
	}
	if !tt.HasText("Hello there.") {
		t.Errorf("detail: %q", tt.Texts())
	}
	if err := tt.Click("Back to Speech to text"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.router.Path(); p != "/transcripts" || !tt.HasText("meeting.m4a") {
		t.Fatalf("list: %s %q", p, tt.Texts())
	}
	if err := tt.Click("meeting.m4a"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.router.Path(); p != "/transcripts/"+tr.ID || a.trDetail == nil {
		t.Fatalf("row: %s", p)
	}
	// Read it in Text to speech: the text is the script.
	if err := tt.Click("Read it in Text to speech"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if p := a.router.Path(); p != "/studio" || !strings.Contains(a.text, "Hello there.") {
		t.Fatalf("studio: %s %q", p, a.text)
	}
	// Dub: a new dub of the same file, started, in the Dub list.
	a.selectTranscript(tr.ID)
	tt.Frame()
	if err := tt.Click("Dub the same media into another language"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if p := a.router.Path(); p != "/dub" || len(a.dubList) != 1 || a.dubList[0].Source != src {
		t.Fatalf("dub: %s %+v", p, a.dubList)
	}
	if eng.lastOf(t, services.KindDubIngest).ID == "" {
		t.Error("the dub did not start")
	}
}

// isoTest is an app whose isolations run at once on a fake engine that
// writes the stems.
func isoTest(t *testing.T) (*app, *fakeEngine, *ui.Tester, *[]string) {
	t.Helper()
	a, eng, tt := newTestApp(t)
	fakeOnDisk(t, a, isoSeparator())
	fakeOnDisk(t, a, isoDenoiser())
	var calls []string
	a.isoEngine = isoFakeEngine(&calls)
	eng.executors[services.KindIsolate] = a.isoSvc.Executor()
	return a, eng, tt, &calls
}

// Voice isolator: submit → a job → the stems listed; play, save, the
// hand-offs; delete.
func TestIsolateFlow(t *testing.T) {
	a, eng, tt, calls := isoTest(t)
	a.navigate("/isolate")
	tt.Frame()
	if !tt.HasText("No isolations yet") || !tt.HasText("Drop audio or video here") || tt.HasText("Coming in this version") {
		t.Fatalf("empty: %q", tt.Texts())
	}
	src := filepath.Join(t.TempDir(), "Song.mp4")
	os.WriteFile(src, []byte("video"), 0o644)
	a.isoPend = src
	tt.Frame()
	if err := tt.Click("Isolate voices (↩)"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if a.isoErr != "" {
		t.Fatalf("refused: %s", a.isoErr)
	}
	r := eng.lastOf(t, services.KindIsolate)
	if !strings.HasPrefix(r.Spec.Owner, services.IsolateOwner) || r.Spec.Lane != jobs.LaneGPU {
		t.Fatalf("job %+v", r.Spec)
	}
	if strings.Join(*calls, ",") != "media.decode,separate.run,enhance.run" {
		t.Errorf("calls %v", *calls)
	}
	if len(a.isoList) != 1 || a.isoList[0].State != services.IsolateDone || a.isoList[0].Title != "Song" {
		t.Fatalf("list %+v", a.isoList)
	}
	x := a.isoList[0]
	if !tt.HasText("Song") || !tt.HasText("Done") {
		t.Errorf("row: %q", tt.Texts())
	}
	if err := tt.Click("Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if p := a.router.Path(); p != "/isolate/"+x.ID {
		t.Fatalf("row: %s", p)
	}
	for _, s := range []string{"Voices", "Music and effects", "Voices, denoised", "Send to Speech to text", "Send to Dub"} {
		if !tt.HasText(s) {
			t.Errorf("result lacks %q: %q", s, tt.Texts())
		}
	}
	// Save a stem.
	dir := t.TempDir()
	isoSaveDir = dir
	t.Cleanup(func() { isoSaveDir = "" })
	if err := tt.Click("Download Music and effects"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Song - Music and effects.wav")); err != nil {
		t.Errorf("saved: %v", err)
	}
	// Send to Speech to text: the denoised voices are transcribed.
	if err := tt.Click("Send to Speech to text"); err != nil {
		t.Fatal(err)
	}
	_, req := transcribeReq(t, eng)
	if p := a.router.Path(); p != "/transcripts" || req.Input != a.isoSvc.Path(x.ID, services.IsolateClean) {
		t.Errorf("to transcribe: %s %+v", p, req)
	}
	// Send to Dub: a dub of the file.
	a.navigate("/isolate/" + x.ID)
	tt.Frame()
	if err := tt.Click("Send to Dub"); err != nil {
		t.Fatal(err)
	}
	if p := a.router.Path(); p != "/dub" || len(a.dubList) != 1 || a.dubList[0].Source != src {
		t.Errorf("to dub: %s %+v", p, a.dubList)
	}
	// Home's Recent lists it.
	if items := a.homeItems(10); !containsKey(items, "iso:"+x.ID) {
		t.Errorf("recent %+v", items)
	}
	// The bell's card goes to the page.
	if a.activityKind(jobs.Job{Spec: jobs.Spec{Owner: services.IsolateOwner + x.ID}}) != "isolate" {
		t.Error("activity kind")
	}
	// Delete.
	a.isoDelete(x.ID, true)
	tt.Frame()
	if len(a.isoList) != 0 {
		t.Fatalf("after delete %+v", a.isoList)
	}
	if _, err := os.Stat(a.isoSvc.Dir(x.ID)); !os.IsNotExist(err) {
		t.Error("folder kept")
	}
	// Read again at start: nothing.
	if l, _ := a.isoSvc.List(context.Background()); len(l) != 0 {
		t.Errorf("listed %+v", l)
	}
}

func containsKey(items []homeItem, key string) bool {
	for _, it := range items {
		if it.key == key {
			return true
		}
	}
	return false
}

// Without the separation model the composer says what to download, and
// submit is off; the download starts from there.
func TestIsolateMissingModel(t *testing.T) {
	a, eng, tt, _ := isoTest(t)
	sep := removeModel(t, a, isoSeparator().ID)
	a.navigate("/isolate")
	tt.Frame()
	want := "Needs " + modelRef(sep) + " (" + humanBytes(sep.Size) + "): download it to isolate voices."
	if !tt.HasText(want) {
		t.Fatalf("no banner %q: %q", want, tt.Texts())
	}
	src := filepath.Join(t.TempDir(), "a.wav")
	writeTestWAV(t, src, 16000, 0.5)
	a.isoPend = src
	tt.Frame()
	a.isoSubmit()
	if a.isoErr == "" || len(a.isoList) != 0 {
		t.Fatalf("started without the model: %+v", a.isoList)
	}
	if err := tt.Click("Download (" + humanBytes(sep.Size) + ")"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if r := eng.lastOf(t, "download"); r.Model != sep.Repo {
		t.Errorf("downloads %s", r.Model)
	}
}

// isoFakeEngine plays the engine of an isolation: it writes the stems
// (calls, when given, records the methods).
func isoFakeEngine(calls *[]string) func(string) jobs.Executor {
	return func(method string) jobs.Executor {
		return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
			if calls != nil {
				*calls = append(*calls, method)
			}
			out := func(path string) {
				wav.WriteFile(path, dsp.Buffer{Rate: 44100, Channels: 2, Samples: make([]float32, 8820)}, wav.PCM16, wav.Options{})
			}
			switch method {
			case proto.MethodMediaDecode:
				var p proto.DecodeParams
				json.Unmarshal(job.Params, &p)
				out(p.Output.Path)
				return json.Marshal(proto.DecodeResult{AudioOut: proto.AudioOut{AudioSeconds: 2.5}})
			case proto.MethodSeparateRun:
				var p proto.SeparateParams
				json.Unmarshal(job.Params, &p)
				out(p.Vocals.Path)
				out(p.Background.Path)
				return json.Marshal(proto.SeparateResult{AudioSeconds: 2.5})
			case proto.MethodEnhanceRun:
				var p proto.EnhanceParams
				json.Unmarshal(job.Params, &p)
				out(p.Output.Path)
				return json.Marshal(proto.EnhanceResult{})
			}
			return nil, &jobs.Error{Code: proto.CodeMethodNotFound, Message: method}
		})
	}
}

// With dictation off, Speech to text does not offer its shortcut (it does
// nothing then) but says where to turn it on (the Mac check, #121).
func TestTranscribeDictationHint(t *testing.T) {
	m := newM2Test(t)
	a, tt := m.a, m.tt
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Enabled = true })
	a.navigate("/transcripts")
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Dictate into any app with ") }) {
		t.Fatalf("on: %q", tt.Texts())
	}
	a.updateSettings(func(s *settings.Settings) { s.Dictation.Enabled = false })
	tt.Frame()
	if !tt.HasText("Turn on dictation in Settings → Dictation to dictate into any app.") || !tt.HasText("Transcribe a file or a recording.") {
		t.Fatalf("off: %q", tt.Texts())
	}
	if slices.ContainsFunc(tt.Texts(), func(s string) bool {
		return strings.Contains(s, "dictate anywhere with") || strings.HasPrefix(s, "Dictate into any app with ")
	}) {
		t.Errorf("off still offers the shortcut: %q", tt.Texts())
	}
}
