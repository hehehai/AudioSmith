package main

// AI phone calls (M6b, thread #23): the Calls page (/calls: recent calls,
// the live call, a new call) and the Twilio setup (/calls/setup, also
// /integrations/twilio). The domain is internal/telephony (Twilio, the
// gateway, the agent loop) and services.Calls (the library, placing
// calls, the gateway's hooks); this file wires them to the app:
// settings.Calls, the auth token in the Keychain ("twilio.token"), the
// language model (a.llmRouter, skill "call_agent", falling back to the
// default route), voices (designed, or cloned with a verified consent
// recording) and their models, and the gateway's listener.
//
// Files: calls.go (state, wiring), calls_place.go (a new call and the
// actions on a call), calls_text.go (texts), calls_view.go (/calls),
// calls_view_call.go (the open call), calls_view_form.go (the new call
// form), calls_pages.go (shell and Projects hooks), calls_phone.go (number
// hints), twilio.go (setup actions), twilio_view.go (/calls/setup).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/telephony"
)

// callSkill routes the call agent's language model (Settings → Writing
// help's default unless a route is set for it).
const callSkill llm.Skill = "call_agent"

// twilioTokenStore keeps the Twilio auth token: the Keychain item
// "twilio.token"; tests replace it.
var twilioTokenStore interface {
	Get() (string, error)
	Set(string) error
	Delete() error
} = hub.Secret("twilio.token", "AudioSmith Twilio auth token")

// twilioAPIBase is Twilio's REST API (tests point it at a fake).
var twilioAPIBase = telephony.DefaultAPIBase

// callMinutes are the choices of a call's maximum length.
var callMinutes = []int{2, 5, 10, 15, 30}

// callsState is the Calls area's state, on the main thread.
type callsState struct {
	callSvc *services.Calls
	callGW  *telephony.Gateway
	// callToken caches the auth token for the gateway's checks
	// (goroutine-safe).
	callToken *callSecret

	callGWAddr string // the address listened on; "" = off
	callGWErr  string
	callHasTok bool // a token is in the Keychain

	callList   []store.CallRecord        // recent calls
	callFull   map[string]*services.Call // calls read or followed, by id
	callHolds  map[string]func()         // live calls' holds on the engine's models (model_holds.go)
	callLoaded bool
	callSel    string // the call shown
	callTab    int    // narrow windows: 0 new call, 1 the call
	callPageH  ui.Handle
	callHistOn bool // narrow windows: the recent calls' drawer open
	twPageH    ui.Handle
	callForm   callForm
	callBusy   bool // placing a call
	callErr    string
	callAsk    bool   // the confirmation dialog is open
	callSay    string // "What to say next"
	callTicker bool   // the clock ticks while a call is live

	callInOpen  bool // the incoming-calls dialog
	callInMode  int  // 0 greeting, 1 agent
	callInBrief string

	tw twilioSetup
}

// callForm is the new call form.
type callForm struct {
	number        string
	numberBlurred bool
	brief         string
	voice         string // label
	disclose      bool
	disclosure    string
	edited        bool // the disclosure was edited
	minutes       string
}

// callSecret is the token, cached.
type callSecret struct {
	mu  sync.Mutex
	tok string
}

func (s *callSecret) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok
}

func (s *callSecret) set(t string) {
	s.mu.Lock()
	s.tok = t
	s.mu.Unlock()
}

// callDisclosureDefault is the outbound disclosure's default template.
func callDisclosureDefault(record bool) string {
	s := i18n.T("Hi, this is an AI assistant calling on behalf of {name}.")
	if record {
		s += " " + i18n.T("This call may be recorded.")
	}
	return s
}

// callInboundDisclosureDefault is the incoming one's.
func callInboundDisclosureDefault(record bool) string {
	s := i18n.T("Hi, you've reached the AI assistant of {name}.")
	if record {
		s += " " + i18n.T("This call may be recorded.")
	}
	return s
}

// openCalls wires the calls service and the gateway; app.go calls it.
func (a *app) openCalls(lib *library) {
	a.callFull = map[string]*services.Call{}
	a.callToken = &callSecret{}
	a.callForm = callForm{disclose: true, minutes: minutesLabel(lib.settings.Get().Calls.Minutes())}
	router := a.llmRouter
	tok := a.callToken
	gwOn := func() bool { return lib.settings.Get().Calls.Enabled }
	a.callSvc = services.NewCalls(services.CallsConfig{
		Store: lib.store.Calls(), Bus: lib.bus, Scheduler: lib.sched, Takes: lib.svc.Render, Voices: lib.svc.Voices,
		Options: func() services.CallOptions { return callOptions(lib.settings.Get(), gwOn()) },
		Twilio: func() (*telephony.Client, error) {
			st := lib.settings.Get()
			t := tok.get()
			if st.Calls.AccountSID == "" || t == "" {
				return nil, fmt.Errorf("%s: %w", i18n.T("Connect your Twilio account first."), core.ErrUnsupported)
			}
			return &telephony.Client{AccountSID: st.Calls.AccountSID, AuthToken: t, Base: twilioAPIBase,
				HTTP: hub.NewHTTPClient(st.Models.Proxy, 20*time.Second)}, nil
		},
		Recipe: func(v core.Voice, text string) (core.Take, error) {
			e, err := callModelFor(lib, lib.settings.Get(), v)
			if err != nil {
				return core.Take{}, err
			}
			t := core.Take{Text: text, Model: e.Repo}
			a.fillVoice(&t, &v, e) // reads only the voice store's paths
			return t, nil
		},
		Chat: func(ctx context.Context, msgs []telephony.Message, onDelta func(string)) (string, error) {
			if router == nil || !router.Available(callSkill) {
				return "", errors.New("no language model is set up (Settings → Writing help)")
			}
			return callChat(router)(ctx, msgs, onDelta)
		},
		Signed:   func() { a.post(a.callSigned) },
		CacheDir: filepath.Dir(lib.peaks.Dir), // the app's cache folder
	})
	a.callGW = telephony.NewGateway(func() telephony.GatewayConfig {
		st := lib.settings.Get()
		return telephony.GatewayConfig{AccountSID: st.Calls.AccountSID, AuthToken: tok.get(), PublicURL: st.Calls.Public()}
	}, a.callSvc.Hooks(), nil)
	core.On(lib.bus, func(ev services.CallChanged) { a.post(func() { a.callChanged(ev) }) })
	core.On(lib.bus, func(ev settings.Changed) {
		if ev.Old.Calls != ev.New.Calls {
			a.post(func() {
				a.syncTwilioChanged(ev.Old.Calls, ev.New.Calls)
				a.applyCalls()
			})
		}
	})
	svc := a.callSvc
	a.background(func() {
		t, _ := twilioTokenStore.Get()
		tok.set(t)
		svc.Reconcile(context.Background())
		a.post(func() {
			a.callHasTok = t != ""
			a.applyCalls()
			a.loadCalls()
		})
	})
	a.syncTwilioForm()
}

// closeCalls hangs up running calls and stops the gateway (on quit).
func (a *app) closeCalls() {
	if a.callSvc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a.callSvc.Close(ctx)
	a.callGW.Close(ctx)
}

// applyCalls starts, moves or stops the gateway to match the settings.
func (a *app) applyCalls() {
	gw := a.callGW
	if gw == nil {
		return
	}
	cfg := a.prefs.Calls
	if !cfg.Enabled {
		if a.callGWAddr != "" || a.callGWErr != "" {
			a.callGWAddr, a.callGWErr = "", ""
			a.async(func() { gw.Stop(context.Background()) })
		}
		return
	}
	addr := cfg.GatewayAddr()
	if a.callGWAddr == addr && a.callGWErr == "" {
		return
	}
	a.async(func() {
		err := gw.Start(addr)
		a.post(func() {
			if !a.prefs.Calls.Enabled || a.prefs.Calls.GatewayAddr() != addr {
				return
			}
			switch {
			case errors.Is(err, telephony.ErrPortInUse):
				a.callGWAddr, a.callGWErr = "", i18n.T("Port %d is used by another program: choose another one.", cfg.Port())
			case err != nil:
				a.callGWAddr, a.callGWErr = "", i18n.T("The call gateway could not start: %v", err)
			default:
				a.callGWAddr, a.callGWErr = addr, ""
			}
		})
	})
}

// callSigned records that Twilio reached the gateway with a valid
// signature (the setup's number step is done).
func (a *app) callSigned() {
	now := time.Now().Unix()
	if now-a.prefs.Calls.Verified < 60 {
		return
	}
	a.updateSettings(func(s *settings.Settings) { s.Calls.Verified = now })
}

// loadCalls reads the recent calls.
func (a *app) loadCalls() {
	svc := a.callSvc
	a.background(func() {
		list, err := svc.List(context.Background(), 50)
		a.post(func() {
			a.callLoaded = true
			if err != nil {
				a.toastWarn(i18n.T("Could not read the calls: %v", err))
				return
			}
			a.callList = list
			if a.callSel == "" && len(list) > 0 {
				a.selectCall(list[0].ID)
			}
		})
	})
}

// selectCall shows a call, reading it when needed.
func (a *app) selectCall(id string) {
	a.callSel, a.callTab = id, 1
	if _, ok := a.callFull[id]; ok || id == "" {
		return
	}
	svc := a.callSvc
	a.background(func() {
		c, err := svc.Get(context.Background(), id)
		if err != nil {
			return
		}
		a.post(func() {
			if _, ok := a.callFull[id]; !ok {
				a.callFull[id] = &c
			}
		})
	})
}

// callChanged follows a call's changes.
func (a *app) callChanged(ev services.CallChanged) {
	a.holdForCall(ev.ID, !ev.Deleted && ev.Call != nil && ev.Call.Live())
	if ev.Deleted {
		delete(a.callFull, ev.ID)
		for i, r := range a.callList {
			if r.ID == ev.ID {
				a.callList = append(a.callList[:i:i], a.callList[i+1:]...)
				break
			}
		}
		if a.callSel == ev.ID {
			a.callSel = ""
		}
		return
	}
	c := ev.Call
	old := a.callFull[c.ID]
	a.callFull[c.ID] = c
	rec := store.CallRecord{ID: c.ID, CreatedAt: c.Created, UpdatedAt: time.Now(), Direction: c.Direction, Number: c.Number,
		Title: c.Title(), Status: c.Phase, Outcome: c.Outcome, Summary: c.Summary, ProviderSID: c.SID, Duration: c.Duration}
	found := false
	for i := range a.callList {
		if a.callList[i].ID == c.ID {
			a.callList[i], found = rec, true
		}
	}
	if !found {
		a.callList = append([]store.CallRecord{rec}, a.callList...)
		if c.Direction == services.CallInbound && c.Live() {
			a.notify(core.SeverityInfo, i18n.T("Incoming call"), telephony.MaskNumber(c.Number), "/calls")
			if a.callSel == "" || !a.callIsLive(a.callSel) {
				a.callSel = c.ID
			}
		}
	}
	if old != nil && old.Live() && !c.Live() {
		a.notify(core.SeveritySuccess, i18n.T("Call ended"), callOutcomeText(c.Outcome)+" · "+telephony.MaskNumber(c.Number), "/calls")
	}
	if old == nil || old.Phase != c.Phase || old.Outcome != c.Outcome || old.Summary != c.Summary {
		a.projects.dirty = true // the Projects page lists calls
	}
	if c.Live() {
		a.callTick()
	}
}

// callIsLive reports whether a call (by id) is running.
func (a *app) callIsLive(id string) bool {
	c := a.callFull[id]
	return c != nil && c.Live()
}

// callTick redraws every second while a call is live (its clock).
func (a *app) callTick() {
	if a.callTicker || a.win == nil {
		return
	}
	a.callTicker = true
	go func() {
		for {
			time.Sleep(time.Second)
			done := make(chan bool, 1)
			a.post(func() {
				live := false
				for _, c := range a.callFull {
					live = live || c.Live()
				}
				if !live {
					a.callTicker = false
				}
				done <- live
			})
			select {
			case live := <-done:
				if !live {
					return
				}
			case <-time.After(10 * time.Second): // the app quit: posts are dropped (#101 L7)
				a.post(func() { a.callTicker = false })
				return
			}
		}
	}()
}

// callVoices are the voices that may make calls: labels and ids.
func (a *app) callVoices() (labels, ids []string) {
	for _, v := range a.voices {
		if services.VoiceAllowed(v) {
			tag := i18n.T("Designed")
			if v.Kind == core.VoiceCloned {
				tag = i18n.T("Your voice")
			}
			labels, ids = append(labels, v.Name+" · "+tag), append(ids, v.ID)
		}
	}
	return labels, ids
}
