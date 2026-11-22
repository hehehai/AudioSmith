package main

// In-app feedback (wiring): actnowkit's feedback.Manager over actnow.dev
// (AUDIOSMITH_FEEDBACK_SERVER for a development site, in development
// builds only: not made by `mygo build`), through Settings → Models &
// network's proxy, with diagnostics of AudioSmith's and the engine's
// logs, the settings without secrets or the user's words and the lines
// of Settings → Advanced's diagnostics. AudioSmith has no licence of its
// own: the site never knows who writes, so the email is always asked
// for, and no device ID is sent. The feedback window (actnowkit's
// feedbackui, feedback_window.go) draws it, Settings → Feedback
// (feedback_view.go) lists this Mac's feedback; the GitHub issue
// (Settings → Advanced → Report a bug) stays.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/logs"
	"github.com/hehehai/AudioSmith/internal/logscrub"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/feedback"
)

// feedbackSite is where feedback goes; feedbackSiteEnv overrides it (a
// development copy of actnow.dev) in development builds.
const (
	feedbackSite    = "https://actnow.dev"
	feedbackSiteEnv = "AUDIOSMITH_FEEDBACK_SERVER"
)

// feedbackDevBuild: a development build, which honours feedbackSiteEnv; a
// published one sends only to actnow.dev, whatever its environment says.
var feedbackDevBuild = mygo.IsDev

// feedbackOptions are what differs between the app and tests.
type feedbackOptions struct {
	Base    string // the site ("": feedbackSiteEnv in a development build, else feedbackSite)
	Dir     string // the outbox and the history ("": the data folder's feedback/)
	LogDir  string // the logs ("": a.logDir)
	Version string
	Client  *http.Client // nil: through the proxy setting
}

// newFeedback makes the Manager and the feedback window over it.
func (a *app) newFeedback(o feedbackOptions) (*feedback.Manager, error) {
	base := o.Base
	if base == "" && feedbackDevBuild() {
		base = os.Getenv(feedbackSiteEnv)
	}
	if base == "" {
		base = feedbackSite
	}
	dir := o.Dir
	if dir == "" {
		dir = filepath.Join(a.dataDir(), "feedback")
	}
	version := o.Version
	if version == "" {
		version = "0.0.0-dev"
	}
	channel := "release"
	if strings.Contains(version, "-") {
		channel = "beta"
	}
	client := o.Client
	if client == nil {
		set := a.lib.settings
		client = feedbackClient(func() string { return set.Get().Models.Proxy })
	}
	logDir := o.LogDir
	if logDir == "" {
		logDir = a.logDir
	}
	specs := []feedback.LogSpec{{Name: logs.FileName}}
	for _, r := range []proto.Role{proto.RoleGPU, proto.RoleListen, proto.RoleMedia} {
		specs = append(specs, feedback.LogSpec{Name: "engine-" + string(r) + ".log"})
	}
	cc := feedback.CollectConfig{
		LogDir:   logDir,
		Logs:     specs,
		Settings: a.fbSnapSettings,
		Extra:    a.fbSnapExtra,
		// The log files went through logs.Redact as they were written;
		// again here for lines a process wrote itself (the engines).
		Scrub: func(s string) string { return logscrub.Line(logs.Redact(s)) },
	}
	if logDir == "" {
		cc.Logs = nil
	}
	m, err := feedback.New(feedback.Config{
		App: updateSlug, Version: version, Build: buildRevision(), Channel: channel,
		BaseURL:    base,
		Dir:        dir,
		HTTPClient: client,
		Locale:     i18n.Locale,
		Collect:    cc,
		Log:        func(s string) { log.Print(s) },
	})
	if err != nil {
		return nil, err
	}
	a.fbUse(m)
	return m, nil
}

// connectFeedback makes the Manager of this run and sends what the last
// one left in the outbox (after the window opens: a.version is read).
func (a *app) connectFeedback() {
	m, err := a.newFeedback(feedbackOptions{Version: a.version})
	if err != nil {
		log.Printf("feedback: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.fbStop = cancel
	go m.Resume(ctx)
}

// closeFeedback stops sending and closes the window (the app quits; the
// outbox stays).
func (a *app) closeFeedback() {
	if a.fbStop != nil {
		a.fbStop()
	}
	if a.fbWin != nil {
		a.fbWin.Stop()
	}
}

// feedbackClient goes through the proxy setting as it is at each request
// (as the updates' client does); the uploads may take minutes.
func feedbackClient(mode func() string) *http.Client {
	c := hub.NewHTTPClient("", 60*time.Second)
	if t, ok := c.Transport.(*http.Transport); ok {
		t.Proxy = func(r *http.Request) (*url.URL, error) { return hub.ProxyFunc(mode())(r) }
	}
	c.Timeout = 15 * time.Minute
	return c
}

// buildRevision is the commit this build was made from ("" unknown).
func buildRevision() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 12 {
				return s.Value[:12]
			}
		}
	}
	return ""
}

// feedbackMasked are the settings' keys whose values are the user's own
// words or numbers (the phone agent's name, disclosure, greeting and
// brief, the Twilio number and account, dictation's vocabulary, the
// labels of API clients and the names of language-model providers).
var feedbackMasked = map[string]bool{
	"userName": true, "disclosure": true, "greeting": true, "inboundBrief": true, "number": true, "numberSid": true,
	"accountSid": true, "vocabulary": true, "label": true, "name": true,
}

// feedbackSettings are the settings as the feedback's diagnostics carry
// them: the diagnostic bundle's (redactedSettings: no headers, no
// passwords), the user's words of feedbackMasked out, and every other
// string as a log line is scrubbed (links with their site only, folder
// and file names masked).
func feedbackSettings(s settings.Settings) json.RawMessage {
	var doc any
	if json.Unmarshal(redactedSettings(s), &doc) != nil {
		return nil
	}
	data, err := json.Marshal(scrubSettingValue("", doc))
	if err != nil {
		return nil
	}
	return data
}

func scrubSettingValue(key string, v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = scrubSettingValue(k, x)
		}
		return v
	case []any:
		for i, x := range v {
			v[i] = scrubSettingValue(key, x)
		}
		return v
	case string:
		if v == "" {
			return v
		}
		if feedbackMasked[key] {
			return logscrub.Mask
		}
		return logscrub.Line(v)
	}
	return v
}

// feedbackExtraKeys are the lines of the diagnostics (diagnosticsText)
// the feedback adds; the machine, the folders and the language are the
// feedback's own.
var feedbackExtraKeys = []string{"Engine", "Protocol", "Go", "MyGo", "ffmpeg", "Loaded models"}

// feedbackExtra picks the engine's, the models', the versions' and the
// self-check's lines of the diagnostics ("Key: value" per line),
// scrubbed as log lines are.
func feedbackExtra(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "Check ") {
			out[k] = logscrub.Line(v)
			continue
		}
		for _, x := range feedbackExtraKeys {
			if k == x {
				out[k] = logscrub.Line(v)
			}
		}
	}
	return out
}
