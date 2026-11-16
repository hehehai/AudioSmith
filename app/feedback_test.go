package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/family"
	"github.com/hehehai/actnowkit/feedback"
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

// newFeedbackTest is the app with Settings → Feedback over a fake site,
// logs with the user's words in them, on a tall window.
func newFeedbackTest(t *testing.T) (*app, *fakeFeedbackSite, *ui.Tester) {
	t.Helper()
	site := newFakeFeedbackSite(t)
	a, _, _ := newTestApp(t)
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
	if _, err := a.newFeedback(feedbackOptions{Base: site.srv.URL, Dir: filepath.Join(t.TempDir(), "feedback"), LogDir: logDir,
		Version: "0.5.0-beta.4", Client: site.srv.Client()}); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1100, 2400)
	a.navigate("/settings/feedback")
	frames(tt)
	return a, site, tt
}

func TestFeedbackSendAsksForTheEmail(t *testing.T) {
	a, site, tt := newFeedbackTest(t)
	for _, s := range []string{"New feedback", "Your feedback", "Send feedback on the web", "Or open an issue on GitHub"} {
		if !has(tt, s) {
			t.Fatalf("no %q: %q", s, tt.Texts())
		}
	}
	a.fbForm.body = "The overlay stays after a recording."
	a.fbSend()
	frames(tt)
	if !has(tt, "Enter an email so we can reply.") || len(site.created()) != 0 {
		t.Fatalf("sent without an email, or no problem shown: %q", tt.Texts())
	}
	a.fbForm.email = "not an email"
	a.fbSend()
	if !has(tt, "This email doesn't look right.") {
		t.Fatalf("no problem shown for a bad email: %q", tt.Texts())
	}
	a.fbForm.email = "ann@example.com"
	a.fbSend()
	frames(tt)
	if a.fbSt.Phase != feedback.Sent {
		t.Fatalf("phase %s err %v", a.fbSt.Phase, a.fbSt.Err)
	}
	if !has(tt, "Thanks! Your feedback FB-1001 was sent. We'll reply by email and here.") {
		t.Fatalf("no number shown: %q", tt.Texts())
	}
	reqs := site.created()
	if len(reqs) != 1 || reqs[0].Email != "ann@example.com" || reqs[0].App != "audiosmith" || reqs[0].Channel != "beta" ||
		reqs[0].LicenceToken != "" || reqs[0].DeviceID != "" {
		t.Fatalf("create %+v", reqs)
	}
	if len(reqs[0].Attachments) != 1 || reqs[0].Attachments[0].Role != feedback.RoleDiagnostics || len(site.puts) != 1 {
		t.Fatalf("the diagnostics not attached: %+v puts %v", reqs[0].Attachments, site.puts)
	}
	if a.fbForm.body != "" || a.fbForm.email != "" || !a.fbForm.diagnostics {
		t.Errorf("the form not cleared after sending: %+v", a.fbForm)
	}
	if len(a.fbHistory) != 1 || a.fbHistory[0].Number != "FB-1001" || !has(tt, "The overlay stays after a recording.") {
		t.Errorf("history %+v", a.fbHistory)
	}
}

func TestFeedbackHistoryUnreadAndReply(t *testing.T) {
	a, site, tt := newFeedbackTest(t)
	a.fbForm.body = "AudioSmith stops after an hour of rendering."
	a.fbForm.email = "ann@example.com"
	a.fbForm.diagnostics = false
	a.fbSend()
	frames(tt)
	if len(a.fbHistory) != 1 || len(site.created()[0].Attachments) != 0 {
		t.Fatalf("history %+v (phase %s err %v)", a.fbHistory, a.fbSt.Phase, a.fbSt.Err)
	}
	if a.fbUnread() != 0 || a.settingsDot("feedback") {
		t.Fatal("unread before any reply")
	}
	site.supportReplies("fb_1", "Thanks, which macOS version is this?")
	a.fbRefresh() // a click: the history is below the dialog's fold
	frames(tt)
	if a.fbUnread() != 1 || !a.settingsDot("feedback") || !hasPrefix(tt, "New reply · FB-1001 · Waiting for you · Problem") {
		t.Fatalf("unread %d texts %q", a.fbUnread(), tt.Texts())
	}
	a.fbOpenThread("fb_1")
	if a.fbThread == nil || !has(tt, "Thanks, which macOS version is this?") {
		t.Fatalf("thread %+v err %q texts %q", a.fbThread, a.fbThreadErr, tt.Texts())
	}
	if a.fbUnread() != 0 || a.settingsDot("feedback") {
		t.Fatal("still unread once read")
	}
	a.fbReplyText = "macOS 15.4 on an M3."
	frames(tt)
	if err := tt.Click("Reply"); err != nil {
		t.Fatal(err)
	}
	frames(tt)
	if a.fbThread == nil || len(a.fbThread.Messages) != 2 || a.fbThread.Messages[1].Body != "macOS 15.4 on an M3." {
		t.Fatalf("thread after the reply %+v", a.fbThread)
	}
	a.fbThreadOpen = false
	a.fbRefresh()
	if a.fbUnread() != 0 {
		t.Errorf("the user's reply counts as unread")
	}
}

// TestFeedbackDiagnosticsHaveNoUserText: what the diagnostics send holds
// the logs, the settings and the helper's lines, and nothing the user
// said, wrote or named.
func TestFeedbackDiagnosticsHaveNoUserText(t *testing.T) {
	a, _, tt := newFeedbackTest(t)
	if !has(tt, "See what is sent") {
		t.Fatalf("no preview button: %q", tt.Texts())
	}
	a.fbOpenPreview()
	p := a.fbPreview
	if !a.fbPreviewOpen || !strings.Contains(p, `"id": "audiosmith"`) || !strings.Contains(p, "audiosmith.log") {
		t.Fatalf("preview %v %s", a.fbPreviewOpen, p)
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

func TestSendFeedbackCommand(t *testing.T) {
	a, _, tt := newTestApp(t)
	var opened []string
	old := openExternal
	openExternal = func(u string) { opened = append(opened, u) }
	t.Cleanup(func() { openExternal = old })
	var cmd *command
	for _, c := range a.commands() {
		if c.id == "send-feedback" {
			cmd = &c
		}
	}
	if cmd == nil || !cmd.enabled {
		t.Fatal("no Send Feedback in the palette")
	}
	cmd.run()
	if !a.settingsOpen || a.shownPath() != "/settings/feedback" || !has(tt, "Send feedback on the web") {
		t.Fatalf("path %s texts %q", a.shownPath(), tt.Texts())
	}
	if !has(tt, "Open GitHub") {
		t.Fatal("no GitHub issue")
	}
	openExternal(issuesURL) // the button is below the dialog's fold
	if len(opened) != 1 || opened[0] != issuesURL {
		t.Errorf("opened %v", opened)
	}
}

// kitMessage is a reference text of actnowkit as AudioSmith's catalogue
// keys it: {app} is AudioSmith, {site} actnow.dev, {number} {email}
// {size} %s, {max} %d.
func kitMessage(s string) string {
	return strings.NewReplacer("{app}", "AudioSmith", "{site}", "actnow.dev", "{number}", "%s", "{email}", "%s", "{size}", "%s", "{max}", "%d").Replace(s)
}

// TestFeedbackTextsMatchKit: the section says, in English and in
// Chinese, what actnowkit's reference texts say (the family's apps word
// them alike), the labels without their "…".
func TestFeedbackTextsMatchKit(t *testing.T) {
	defer i18n.SetLocale("en")
	texts := []family.Text{
		feedback.TextNew, feedback.TextHistory, feedback.TextOnTheWeb,
		feedback.TextViewOnWeb, feedback.TextKind, feedback.TextKindBug, feedback.TextKindIdea, feedback.TextBodyPlaceholder,
		feedback.TextEmail, feedback.TextEmailPlaceholder, feedback.TextAttachments, feedback.TextAddImages,
		feedback.TextAddVideo, feedback.TextAttachHint, feedback.TextDiagnostics, feedback.TextPreview,
		feedback.TextPrivacyLink, feedback.TextPreparing, feedback.TextSending, feedback.TextSent,
		feedback.TextFailed, feedback.TextRetry, feedback.TextNoHistory, feedback.TextReply, feedback.TextYou, feedback.TextNewReply,
		feedback.TextStatusTriaged, feedback.TextStatusWaitingUser, feedback.TextProblemBodyShort, feedback.TextProblemBodyLong,
		feedback.TextProblemEmailRequired, feedback.TextProblemEmailInvalid, feedback.TextProblemTooManyImages,
		feedback.TextProblemImage, feedback.TextProblemVideo, feedback.TextProblemVideoTooLarge, feedback.TextRateLimited,
	}
	// AudioSmith's labels have no trailing "…" (AGENTS.md), actnowkit's
	// buttons do: compared without it.
	labels := map[string]bool{feedback.TextAddImages.EN: true, feedback.TextAddVideo.EN: true, feedback.TextPreview.EN: true}
	for _, tx := range texts {
		if labels[tx.EN] {
			tx.EN, tx.ZH = strings.TrimSuffix(tx.EN, "…"), strings.TrimSuffix(tx.ZH, "…")
		}
		en := kitMessage(tx.EN)
		i18n.SetLocale("zh-Hans")
		zh := i18n.T(en)
		i18n.SetLocale("en")
		if zh == en {
			t.Errorf("%q is not in the catalogue", en)
			continue
		}
		if want := kitMessage(tx.ZH); zh != want {
			t.Errorf("%q: AudioSmith says %q, actnowkit %q", en, zh, want)
		}
	}
	i18n.SetLocale("zh-Hans")
	for _, c := range []struct {
		got  string
		want family.Text
	}{
		{feedbackKindText(feedback.KindQuestion), feedback.TextKindQuestion}, {feedbackKindText(feedback.KindOther), feedback.TextKindOther},
		{feedbackStatusText(feedback.StatusNew), feedback.TextStatusNew}, {feedbackStatusText(feedback.StatusInProgress), feedback.TextStatusInProgress},
		{feedbackStatusText(feedback.StatusResolved), feedback.TextStatusResolved}, {feedbackStatusText(feedback.StatusClosed), feedback.TextStatusClosed},
		{i18n.X("feedback", "Description"), feedback.TextBody}, {i18n.X("feedback", "Send"), feedback.TextSend},
		{i18n.X("feedback", "Discard"), feedback.TextDiscard}, {i18n.X("feedback author", "Support"), feedback.TextSupport},
	} {
		if c.got != c.want.ZH {
			t.Errorf("%q: AudioSmith says %q, actnowkit %q", c.want.EN, c.got, c.want.ZH)
		}
	}
}

// connectFeedbackShots gives the snapshots' Settings → Feedback a fake
// site with one feedback answered, and a form being written.
func connectFeedbackShots(t *testing.T, a *app) {
	t.Helper()
	site := newFakeFeedbackSite(t)
	if _, err := a.newFeedback(feedbackOptions{Base: site.srv.URL, Dir: filepath.Join(t.TempDir(), "feedback"), LogDir: t.TempDir(),
		Version: "0.5.0", Client: site.srv.Client()}); err != nil {
		t.Fatal(err)
	}
	a.fbForm = feedbackForm{body: "Rewrite answers in English when I speak Chinese.", email: "ann@example.com"}
	a.fbSend()
	site.supportReplies("fb_1", "Thanks! Which language model is set for Rewrite?")
	a.fbRefresh()
	a.fbMgr.Reset()
	a.fbForm = feedbackForm{kind: 0, body: "The overlay stays on screen after a recording ends.", diagnostics: true}
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
