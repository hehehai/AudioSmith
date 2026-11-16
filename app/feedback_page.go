package main

// Settings → Feedback's state: a new feedback (its type, what happened,
// an email to answer, screenshots and a screen recording, the
// diagnostics with what they hold shown before sending), the submissions
// waiting to be sent, and this Mac's feedback with the replies (read and
// answered here, or on actnow.dev). The work is actnowkit's
// feedback.Manager (the protocol, the outbox that survives quitting, the
// history); feedback.go makes it, this file keeps its state on the main
// thread, feedback_view.go draws it. After Dicta's feedbackui.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/actnowkit/feedback"
)

// feedbackPrivacyURL is actnow.dev's privacy policy (the form's last line).
const feedbackPrivacyURL = "https://actnow.dev/legal/privacy"

// openExternal opens a link in the browser; tests replace it.
var openExternal = func(u string) { go mygo.Shell.OpenExternal(u) }

// pickFeedbackFiles asks for files (blocks: called off the main thread);
// tests replace it.
var pickFeedbackFiles = func(parent *mygo.Window, title string, multiple bool, exts ...string) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: title, Multiple: multiple,
		Filters: []mygo.FileFilter{{Name: title, Extensions: exts}}})
}

// feedbackForm is the new feedback being written.
type feedbackForm struct {
	kind        int // index in feedback.Kinds
	body, email string
	images      []string
	video       string
	diagnostics bool
}

func newFeedbackForm() feedbackForm { return feedbackForm{diagnostics: true} }

// draft is the form as actnowkit takes it.
func (f feedbackForm) draft() feedback.Draft {
	k := feedback.KindBug
	if f.kind >= 0 && f.kind < len(feedback.Kinds) {
		k = feedback.Kinds[f.kind]
	}
	return feedback.Draft{Kind: k, Body: f.body, Email: strings.TrimSpace(f.email), Images: f.images, Video: f.video, Diagnostics: f.diagnostics}
}

// feedbackState is Settings → Feedback's state (main thread, but for
// the snapshot, which the Manager reads off it).
type feedbackState struct {
	fbMgr   *feedback.Manager
	fbUnsub func()
	fbStop  context.CancelFunc

	fbForm feedbackForm
	// fbTried: Send was pressed once, so the problems show.
	fbTried    bool
	fbProblems []feedback.Problem
	// fbSt is the Manager's state, as last posted.
	fbSt feedback.State
	// fbHistory is this Mac's feedback (newest first); fbRefreshing
	// while the site is asked, fbHistErr its last failure.
	fbHistory    []feedback.HistoryEntry
	fbRefreshing bool
	fbHistErr    string
	fbRefreshed  bool
	// The thread open in its dialog.
	fbThreadOpen bool
	fbThreadID   string
	fbThread     *feedback.Thread
	fbThreadErr  string
	fbLoading    bool
	fbReplyText  string
	fbReplying   bool
	// The preview of the diagnostics.
	fbPreviewOpen bool
	fbPreview     string

	fbSnapMu sync.Mutex
	fbSnap   feedbackSnapshot
}

// feedbackSnapshot is AudioSmith's part of the diagnostics, read on the
// main thread for the Manager's Collect, which runs off it.
type feedbackSnapshot struct {
	settings json.RawMessage
	extra    map[string]string
}

// fbUse connects the Manager.
func (a *app) fbUse(m *feedback.Manager) {
	if a.fbUnsub != nil {
		a.fbUnsub()
		a.fbUnsub = nil
	}
	a.fbMgr = m
	a.fbForm = newFeedbackForm()
	if m == nil {
		return
	}
	a.fbSt = m.State()
	a.fbSt.Pending = m.Pending() // before Resume runs
	a.fbHistory = m.History()
	a.fbUnsub = m.Subscribe(func(st feedback.State) { a.post(func() { a.fbStateChanged(st) }) })
}

func (a *app) fbStateChanged(st feedback.State) {
	a.fbSt = st
	if st.Phase == feedback.Sent {
		a.fbForm, a.fbTried, a.fbProblems = newFeedbackForm(), false, nil
		a.fbHistory = a.fbMgr.History()
	}
}

// fbUnread counts the feedback with replies not read (the dot on
// Settings → Feedback).
func (a *app) fbUnread() int {
	n := 0
	for _, e := range a.fbHistory {
		if e.Unread() {
			n++
		}
	}
	return n
}

// fbTakeSnapshot reads the settings and the diagnostics' lines.
func (a *app) fbTakeSnapshot() {
	snap := feedbackSnapshot{settings: feedbackSettings(a.prefs), extra: feedbackExtra(a.diagnosticsText())}
	a.fbSnapMu.Lock()
	a.fbSnap = snap
	a.fbSnapMu.Unlock()
}

// fbSnapSettings and fbSnapExtra are the last snapshot's (Collect's
// Settings and Extra; any goroutine).
func (a *app) fbSnapSettings() json.RawMessage {
	a.fbSnapMu.Lock()
	defer a.fbSnapMu.Unlock()
	return a.fbSnap.settings
}

func (a *app) fbSnapExtra() map[string]string {
	a.fbSnapMu.Lock()
	defer a.fbSnapMu.Unlock()
	return a.fbSnap.extra
}

// fbBusy: a submission is being prepared or sent.
func (a *app) fbBusy() bool {
	return a.fbSt.Phase == feedback.Preparing || a.fbSt.Phase == feedback.Sending
}

// fbRefresh asks the site for the statuses and replies of the history.
func (a *app) fbRefresh() {
	if a.fbMgr == nil || a.fbRefreshing {
		return
	}
	a.fbRefreshing, a.fbRefreshed = true, true
	m := a.fbMgr
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		items, err := m.Refresh(ctx)
		a.post(func() {
			a.fbRefreshing = false
			if items != nil || err == nil {
				a.fbHistory = m.History()
			}
			a.fbHistErr = ""
			if err != nil {
				a.fbHistErr = err.Error()
			}
		})
	})
}

// fbSend checks the form and, when it is complete, submits it off the
// main thread; the Manager's state shows how it goes.
func (a *app) fbSend() {
	if a.fbMgr == nil || a.fbBusy() {
		return
	}
	a.fbTried = true
	d := a.fbForm.draft()
	a.fbProblems = feedback.Validate(d, feedback.Identity{}) // no licence: the email is asked for
	if len(a.fbProblems) > 0 {
		return
	}
	a.fbTakeSnapshot()
	m := a.fbMgr
	a.async(func() {
		_, _ = m.Submit(context.Background(), d) // the state says how it went
	})
}

// fbRetry sends a waiting submission again.
func (a *app) fbRetry(localID string) {
	if a.fbMgr == nil || a.fbBusy() {
		return
	}
	m := a.fbMgr
	a.async(func() { _ = m.Retry(context.Background(), localID) })
}

// fbDiscard drops a waiting submission.
func (a *app) fbDiscard(localID string) {
	if a.fbMgr == nil {
		return
	}
	if err := a.fbMgr.Discard(localID); err != nil {
		a.toast(i18n.T("Could not discard the feedback: %v", err))
	}
}

// fbOpenThread opens a feedback of the history with its messages (and
// marks them read).
func (a *app) fbOpenThread(id string) {
	if a.fbMgr == nil {
		return
	}
	a.fbThreadOpen, a.fbThreadID, a.fbThread, a.fbThreadErr, a.fbLoading, a.fbReplyText = true, id, nil, "", true, ""
	m := a.fbMgr
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t, err := m.Thread(ctx, id)
		a.post(func() {
			if a.fbThreadID != id {
				return
			}
			a.fbLoading = false
			a.fbHistory = m.History()
			if err != nil {
				a.fbThreadErr = err.Error()
				return
			}
			a.fbThread = &t
		})
	})
}

// fbSendReply adds the user's reply to the open thread.
func (a *app) fbSendReply() {
	body := strings.TrimSpace(a.fbReplyText)
	if a.fbMgr == nil || a.fbThread == nil || body == "" || a.fbReplying {
		return
	}
	a.fbReplying = true
	id, m := a.fbThreadID, a.fbMgr
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := m.Reply(ctx, id, body)
		a.post(func() {
			a.fbHistory = m.History() // the reply is marked seen (actnowkit)
			a.fbReplying = false
			if err != nil {
				a.toast(i18n.T("Could not send the reply: %v", err))
				return
			}
			a.fbReplyText = ""
			if a.fbThread == nil || a.fbThreadID != id {
				return
			}
			a.fbThread.Messages = append(a.fbThread.Messages, msg)
		})
	})
}

// fbOpenPreview shows what the diagnostics hold now.
func (a *app) fbOpenPreview() {
	if a.fbMgr == nil {
		return
	}
	a.fbTakeSnapshot()
	a.fbPreview = a.fbMgr.Diagnostics(a.fbForm.email).Preview()
	a.fbPreviewOpen = true
}

// fbAddImages asks for screenshots (up to feedback.MaxImages in all).
func (a *app) fbAddImages() {
	win := a.win
	go func() {
		paths, err := pickFeedbackFiles(win, i18n.T("Add screenshots"), true, "png", "jpg", "jpeg", "gif", "heic", "webp", "tiff", "bmp")
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.fbAddImagePaths(paths) })
	}()
}

func (a *app) fbAddImagePaths(paths []string) {
	for _, path := range paths {
		if len(a.fbForm.images) >= feedback.MaxImages {
			a.toast(i18n.T("At most %d images.", feedback.MaxImages))
			return
		}
		if pr := feedback.CheckImage(path); pr != "" {
			a.toast(feedbackProblemText(pr) + " " + filepath.Base(path))
			continue
		}
		a.fbForm.images = append(a.fbForm.images, path)
	}
}

// fbAddVideo asks for a screen recording.
func (a *app) fbAddVideo() {
	win := a.win
	go func() {
		paths, err := pickFeedbackFiles(win, i18n.T("Add a screen recording"), false, "mov", "mp4", "m4v")
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			if pr := feedback.CheckVideo(paths[0]); pr != "" {
				a.toast(feedbackProblemText(pr))
				return
			}
			a.fbForm.video = paths[0]
		})
	}()
}

// fbWebURL is the site's feedback form (the app, its version and language).
func (a *app) fbWebURL() string {
	if a.fbMgr == nil {
		return feedbackSite + "/feedback?app=" + updateSlug
	}
	return a.fbMgr.WebURL()
}
