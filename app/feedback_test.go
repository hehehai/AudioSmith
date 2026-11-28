package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/actnowkit/feedback"
	"github.com/hehehai/actnowkit/feedback/feedbacktest"
	fbui "github.com/hehehai/actnowkit/feedbackui"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// The words of the user the diagnostics must never carry: the text to
// speak, voices', projects' and files' names, the phone agent's words,
// dictation's vocabulary, a provider's name, a proxy's password.
var feedbackPrivateWords = []string{
	"Lighthouse", "Grandma", "Anna's AirPods", "Interview", "proxysecret", "Kubernetes", "River Phone", "Ahoy caller", "+15551234567", "My Ollama", "tok-secret",
}

// frames runs the tester a few frames, so state set in one shows.
func frames(tt *ui.Tester) { settle(tt) }

// has reports whether the view shows s.
func has(tt *ui.Tester, s string) bool {
	frames(tt)
	return tt.HasText(s)
}

// hasPrefix reports whether a text on screen starts with s.
func hasPrefix(tt *ui.Tester, s string) bool {
	for _, x := range tt.Texts() {
		if strings.HasPrefix(x, s) {
			return true
		}
	}
	return false
}

// fbClick clicks a label and lets the frames after it run.
func fbClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	settle(tt)
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v in %q", label, err, tt.Texts())
	}
	settle(tt)
}

// fbTest is the app with its feedback over a fake actnow.dev: the app's
// window in tt, the feedback window (headless) in wt.
type fbTest struct {
	a    *app
	eng  *fakeEngine
	site *feedbacktest.Site
	tt   *ui.Tester
	wt   *ui.Tester
}

func (f *fbTest) frames() { settle(f.tt); settle(f.wt) }

// connectTestFeedback connects a's feedback to site, the window headless.
func connectTestFeedback(t *testing.T, a *app, site *feedbacktest.Site, logDir string) {
	t.Helper()
	a.fbOptions = fbui.Options{Headless: true}
	if _, err := a.newFeedback(feedbackOptions{Base: site.URL, Dir: filepath.Join(t.TempDir(), "feedback"), LogDir: logDir,
		Version: "0.5.0-beta.4", Client: site.Client()}); err != nil {
		t.Fatal(err)
	}
}

// newFeedbackTest is the app with the feedback over a fake site, logs
// and settings with the user's words in them, on a tall window.
func newFeedbackTest(t *testing.T) *fbTest {
	t.Helper()
	site := feedbacktest.New()
	t.Cleanup(site.Close)
	a, eng, _ := newTestApp(t)
	logDir := t.TempDir()
	os.WriteFile(filepath.Join(logDir, "audiosmith.log"), []byte(strings.Join([]string{
		`2026-11-16 10:00:00.000 INFO app: AudioSmith 0.5.0-beta.3 started`,
		`2026-11-16 10:00:01.000 INFO app: toast text="Saved “The Lighthouse, chapter 3”"`,
		`2026-11-16 10:00:02.000 INFO app: studio: render name=Grandma file=Interview.wav ms=1200`,
		`2026-11-16 10:00:03.000 INFO app: longform: "The Lighthouse" is done: removed 2 earlier unfinished renders`,
		`2026-11-16 10:00:06.000 ERROR app: export: could not write ~/Documents/Interview with Anna.m4a: disk full`,
		`2026-11-16 10:00:07.000 ERROR app: dub: Get "https://www.youtube.com/watch?v=Lighthouse": EOF`,
	}, "\n")+"\n"), 0o600)
	os.WriteFile(filepath.Join(logDir, "engine-listen.log"), []byte("2026-11-16 10:00:00.000 capture 7: Anna's AirPods Pro did not start with voice processing (err); recording without it\n"), 0o600)
	a.updateSettings(func(s *settings.Settings) {
		s.Calls.UserName, s.Calls.Greeting, s.Calls.Number = "River Phone", "Ahoy caller, this is AudioSmith", "+15551234567"
		s.Dictation.Vocabulary = "Kubernetes, Lighthouse"
		s.LLM.Providers = []settings.LLMProvider{{ID: "ollama", Name: "My Ollama", BaseURL: "http://127.0.0.1:11434/v1", Headers: map[string]string{"X-Token": "tok-secret"}}}
		s.Models.Proxy = "http://river:proxysecret@127.0.0.1:7897"
	})
	connectTestFeedback(t, a, site, logDir)
	return &fbTest{a: a, eng: eng, site: site, tt: ui.NewTester(a.view, 1100, 1400), wt: ui.NewTester(a.fbWin.View, fbui.WindowWidth, fbui.WindowHeight)}
}

func TestFeedbackSettingsPanelOpensWindow(t *testing.T) {
	f := newFeedbackTest(t)
	f.a.navigate("/settings/feedback")
	f.frames()
	for _, s := range []string{"Your feedback", "Feedback you send from this Mac appears here.", "Open the feedback window",
		"Send feedback on the web", "Or open an issue on GitHub"} {
		if !has(f.tt, s) {
			t.Fatalf("no %q in %q", s, f.tt.Texts())
		}
	}
	if f.a.fbWin.IsOpen() {
		t.Fatal("open before asked")
	}
	fbClick(t, f.tt, "Open the feedback window")
	f.frames()
	if !f.a.fbWin.IsOpen() || !has(f.wt, "Send Feedback") || !has(f.wt, "to the AudioSmith team · actnow.dev") {
		t.Fatalf("window: open %v, %q", f.a.fbWin.IsOpen(), f.wt.Texts())
	}
	fbClick(t, f.wt, "Cancel")
	if f.a.fbWin.IsOpen() {
		t.Fatal("still open")
	}
}

func TestFeedbackWindowInChinese(t *testing.T) {
	f := newFeedbackTest(t)
	i18n.SetLocale("zh-Hans")
	f.a.sendFeedback()
	f.frames()
	for _, s := range []string{"发送反馈", "发给 AudioSmith 团队 · actnow.dev", "提问", "提交"} {
		if !has(f.wt, s) {
			t.Fatalf("no %q in %q", s, f.wt.Texts())
		}
	}
}

// AudioSmith has no licence: the email is always asked for, and the
// diagnostics go with the feedback; sent, Settings → Feedback lists it
// and a reply shows a dot.
func TestFeedbackSendAsksForTheEmail(t *testing.T) {
	f := newFeedbackTest(t)
	w := f.a.fbWin
	f.a.sendFeedback()
	w.SetForm(feedback.KindBug, "The overlay stays after a recording.", "", true, false)
	w.Send()
	f.frames()
	if !has(f.wt, "Enter an email so we can reply.") || f.site.Count("POST /api/feedback/v1/create") != 0 {
		t.Fatalf("sent without an email, or no problem shown: %q", f.wt.Texts())
	}
	w.SetForm("", "", "ann@example.com", true, false)
	w.Send()
	f.frames()
	if s := w.Snapshot(); s.Sent != "FB-1042" && s.Phase != feedback.Sent {
		t.Fatalf("not sent: %+v", s)
	}
	it := f.site.Items()["fb_1"]
	if it == nil || !it.Completed || it.Request.Email != "ann@example.com" || it.Request.App != "audiosmith" || it.Request.Channel != "beta" ||
		it.Request.LicenceToken != "" || it.Request.DeviceID != "" || it.Request.Context != nil {
		t.Fatalf("site %+v", it)
	}
	if len(it.Files) != 1 {
		t.Fatalf("the diagnostics not attached: %+v", it.Files)
	}
	f.a.navigate("/settings/feedback")
	if !has(f.tt, "The overlay stays after a recording.") || !has(f.tt, "Your feedback (1)") {
		t.Fatalf("panel %q", f.tt.Texts())
	}
	if f.a.fbUnread() != 0 || f.a.settingsDot("feedback") {
		t.Fatal("unread before a reply")
	}
	f.site.SupportReplies("fb_1", "Thanks, which macOS version is this?")
	w.Refresh()
	f.frames()
	if f.a.fbUnread() != 1 || !f.a.settingsDot("feedback") {
		t.Fatalf("unread %d", f.a.fbUnread())
	}
}

// TestFeedbackDiagnosticsHaveNoUserText: what the diagnostics send holds
// the logs, the settings and the helper's lines, and nothing the user
// said, wrote or named.
func TestFeedbackDiagnosticsHaveNoUserText(t *testing.T) {
	f := newFeedbackTest(t)
	f.a.sendFeedback()
	f.a.fbWin.SetForm("", "", "ann@example.com", true, false)
	f.a.fbWin.ShowWhatIsSent(false)
	p := f.a.fbWin.Snapshot().Sheet
	if !strings.Contains(p, `"id": "audiosmith"`) || !strings.Contains(p, "audiosmith.log") {
		t.Fatalf("preview %s", p)
	}
	for _, w := range []string{`"Engine"`, `"settings"`, `"calls"`, "127.0.0.1:7897", "disk full", "render", "engine-listen.log", "did not start with voice processing"} {
		if !strings.Contains(p, w) {
			t.Errorf("no %s in the diagnostics: %s", w, p)
		}
	}
	for _, w := range feedbackPrivateWords {
		if strings.Contains(p, w) {
			t.Errorf("the diagnostics hold %q: %s", w, p)
		}
	}
}

func TestFeedbackExtra(t *testing.T) {
	got := feedbackExtra("AudioSmith: 1.0\nEngine: 0.5.0 (ready)\nData folder: /Users/ann/x (free 3 GB)\nMyGo: v0.5.5\nCheck data: fail Cannot write in ~/Documents/Acme notes: denied\nLanguage: en\n")
	if len(got) != 3 || got["Engine"] != "0.5.0 (ready)" || got["MyGo"] != "v0.5.5" || strings.Contains(got["Check data"], "Acme") {
		t.Errorf("%v", got)
	}
}

// The palette's Send Feedback opens the window (Help → Send Feedback and
// the menu bar's item call the same sendFeedback); without the feedback
// connected it goes to Settings → Feedback.
func TestSendFeedbackCommand(t *testing.T) {
	f := newFeedbackTest(t)
	for _, c := range f.a.commands() {
		if c.id == "send-feedback" {
			if !c.enabled {
				t.Fatal("disabled")
			}
			c.run()
		}
	}
	if !f.a.fbWin.IsOpen() {
		t.Fatal("the palette's command did not open the window")
	}

	a, _, tt := newTestApp(t) // no Manager
	var opened []string
	old := openExternal
	openExternal = func(u string) { opened = append(opened, u) }
	t.Cleanup(func() { openExternal = old })
	a.sendFeedback()
	if !a.settingsOpen || !has(tt, "Send feedback on the web") || !has(tt, "Open GitHub") {
		t.Fatalf("texts %q", tt.Texts())
	}
	openExternal(issuesURL) // the button may be below the dialog's fold
	if len(opened) != 1 || opened[0] != issuesURL {
		t.Errorf("opened %v", opened)
	}
}

// fbFailure is a failure point of AudioSmith: how to show it, and its
// report's code, kind and place.
type fbFailure struct {
	name, code, kind, where string
	show                    func(t *testing.T, f *fbTest)
}

var fbFailures = []fbFailure{
	{"speech", codeSpeech, kindSpeech, "Text to speech › Generate", func(t *testing.T, f *fbTest) {
		f.a.navigate("/studio")
		f.a.jobError = "the engine exited: signal: abort trap"
	}},
	{"engine", codeEngine, "engine_crashed", "Engine › gpu", func(t *testing.T, f *fbTest) {
		f.eng.status(engine.WorkerStatus{Role: proto.RoleGPU, State: engine.WorkerFailed, Reason: engine.FailCrashed, Err: "the engine exited: signal: abort trap"})
		f.a.showPage("engine")
	}},
	{"model", codeModel, kindModelDownload, "Models › Model card", func(t *testing.T, f *fbTest) {
		useFakeHub(t, f.a)
		f.a.updateSettings(func(s *settings.Settings) { s.Models.Endpoints = []string{"http://127.0.0.1:1"} })
		f.a.resetHub()
		openModels(f.a)
		f.frames()
		f.a.download(entry(t, "chatterbox-turbo-8bit"))
		f.tt.SetSize(1100, 6000)
	}},
	{"transcription", codeTranscribe, kindTranscribe, "Speech to text", func(t *testing.T, f *fbTest) {
		f.a.navigate("/transcripts")
		f.a.trErr = "The engine stopped while it listened."
	}},
	{"audiobook", codeAudiobook, kindRender, "Audiobook › Render", func(t *testing.T, f *fbTest) {
		f.a.navigate("/audiobook")
		fbClick(t, f.tt, "Load sample")
		p := f.a.lfBook
		p.render = "r1"
		f.a.lfRenders["r1"] = &lfRender{rec: core.Render{ID: "r1", Kind: core.ProjectAudiobook, State: core.RenderFailed}}
	}},
	{"dub", codeDub, kindDub, "Dub", func(t *testing.T, f *fbTest) {
		f.a.navigate("/dub")
		f.a.dubErr = "The mix could not be written: disk full"
	}},
}

// Every failure point shows "Report this problem"; a click opens the
// window filled with its report, without the user's words.
func TestFailuresShowReportLink(t *testing.T) {
	simulateDarwin(t)
	for _, fl := range fbFailures {
		t.Run(fl.name, func(t *testing.T) {
			f := newFeedbackTest(t)
			f.a.step("generate")
			fl.show(t, f)
			f.frames()
			fbClick(t, f.tt, "Report this problem")
			if !f.a.fbWin.IsOpen() {
				t.Fatal("the window did not open")
			}
			f.frames()
			all := strings.Join(f.wt.Texts(), "\n")
			want := []string{"Report this problem", fl.code, "log lines)", "Include this error report", fl.kind, fl.where, "Generate speech"}
			if fl.name == "model" {
				want = append(want, "Download a model")
			}
			for _, s := range want {
				if !strings.Contains(all, s) {
					t.Errorf("no %q in the window: %q", s, f.wt.Texts())
				}
			}
			for _, w := range feedbackPrivateWords {
				if strings.Contains(all, w) {
					t.Errorf("the user's words in the report: %q", w)
				}
			}
			if s := f.a.fbWin.Snapshot(); !strings.HasPrefix(s.Report, "ER-") || !s.Include || s.Kind != feedback.KindBug {
				t.Errorf("prefill %+v", s)
			}
		})
	}
}

// Sent from a report's link: the request has the context, the
// diagnostics the report, and neither holds the user's words.
func TestFeedbackWithReportUploaded(t *testing.T) {
	f := newFeedbackTest(t)
	f.a.step("generate")
	fbFailures[0].show(t, f)
	f.frames()
	fbClick(t, f.tt, "Report this problem")
	f.a.fbWin.SetForm("", "The take fails at once.", "ann@example.com", true, true)
	f.a.fbWin.Send()
	f.frames()
	if !has(f.wt, "FB-1042") && f.a.fbWin.Snapshot().Phase != feedback.Sent {
		t.Fatalf("not sent: %+v %q", f.a.fbWin.Snapshot(), f.wt.Texts())
	}
	it := f.site.Items()["fb_1"]
	if it == nil || !it.Completed {
		t.Fatalf("site %+v", it)
	}
	ctx := it.Request.Context
	if ctx == nil || ctx.Code != codeSpeech || ctx.Kind != kindSpeech || ctx.Where != "Text to speech › Generate" ||
		!strings.HasPrefix(ctx.ReportID, "ER-") || it.Request.Kind != feedback.KindBug || len(ctx.Steps) == 0 || ctx.Steps[0] != "generate" {
		t.Fatalf("context %+v kind %s", ctx, it.Request.Kind)
	}
	var diag struct {
		Report *feedback.Report `json:"report"`
	}
	for _, file := range it.Files {
		if file.Meta.Role == feedback.RoleDiagnostics {
			if err := json.Unmarshal(file.Data, &diag); err != nil {
				t.Fatal(err)
			}
		}
	}
	if diag.Report == nil || diag.Report.ID != ctx.ReportID || len(diag.Report.Logs) == 0 || !strings.Contains(diag.Report.Error, "abort trap") {
		t.Fatalf("diagnostics report %+v", diag.Report)
	}
	data, _ := json.Marshal(it.Request)
	rep, _ := json.Marshal(diag.Report)
	for _, w := range feedbackPrivateWords {
		if strings.Contains(string(data), w) || strings.Contains(string(rep), w) {
			t.Errorf("the user's words %q are sent", w)
		}
	}
}

// The window follows Settings → Appearance, not only the system's.
func TestFeedbackWindowFollowsAppearance(t *testing.T) {
	f := newFeedbackTest(t)
	f.a.sendFeedback()
	f.a.updateSettings(func(s *settings.Settings) { s.Appearance.Theme = "dark" })
	f.frames()
	if c := f.wt.Image().RGBAAt(5, 300); c.R > 40 {
		t.Fatalf("not dark: %v", c)
	}
	f.a.updateSettings(func(s *settings.Settings) { s.Appearance.Theme = "light" })
	f.wt.SetDark(true)
	f.frames()
	if c := f.wt.Image().RGBAAt(5, 300); c.R < 200 {
		t.Fatalf("not light: %v", c)
	}
}

// connectFeedbackShots gives the snapshots' Settings → Feedback a fake
// site with one feedback answered.
func connectFeedbackShots(t *testing.T, a *app) {
	t.Helper()
	site := feedbacktest.New()
	t.Cleanup(site.Close)
	connectTestFeedback(t, a, site, t.TempDir())
	w := a.fbWin
	w.Open(feedback.Prefill{})
	w.SetForm(feedback.KindBug, "Rewrite answers in English when I speak Chinese.", "ann@example.com", true, false)
	w.Send()
	site.SupportReplies("fb_1", "Thanks! Which language model is set for Rewrite?")
	w.Refresh()
	w.Close()
}

// TestFeedbackSiteOverride: AUDIOSMITH_FEEDBACK_SERVER points a development
// build at another site; a published build ignores it.
func TestFeedbackSiteOverride(t *testing.T) {
	a, _, _ := newTestApp(t)
	t.Setenv(feedbackSiteEnv, "http://127.0.0.1:3100")
	old := feedbackDevBuild
	t.Cleanup(func() { feedbackDevBuild = old })
	for _, c := range []struct {
		dev  bool
		want string
	}{{true, "http://127.0.0.1:3100/feedback"}, {false, feedbackSite + "/feedback"}} {
		feedbackDevBuild = func() bool { return c.dev }
		m, err := a.newFeedback(feedbackOptions{Dir: filepath.Join(t.TempDir(), "feedback"), Version: "0.1.0"})
		if err != nil {
			t.Fatal(err)
		}
		if u := m.WebURL(); !strings.HasPrefix(u, c.want) {
			t.Errorf("dev %v: %s", c.dev, u)
		}
	}
}
