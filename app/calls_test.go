package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/services"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/store"
	"github.com/hehehai/AudioSmith/internal/telephony"
	tt "github.com/hehehai/AudioSmith/internal/telephony/telephonytest"
)

// memSecret is a Keychain item in memory.
type memSecret struct {
	mu sync.Mutex
	v  string
}

func (m *memSecret) Get() (string, error) { m.mu.Lock(); defer m.mu.Unlock(); return m.v, nil }
func (m *memSecret) Set(v string) error   { m.mu.Lock(); defer m.mu.Unlock(); m.v = v; return nil }
func (m *memSecret) Delete() error        { m.mu.Lock(); defer m.mu.Unlock(); m.v = ""; return nil }

// callTest is an app with a fake Twilio, an in-memory token and a
// designed voice.
type callTest struct {
	a     *app
	eng   *fakeEngine
	tt    *ui.Tester
	api   *tt.API
	tok   *memSecret
	voice core.Voice
}

const callTestDesign = "mlx-community/Qwen3-TTS-12Hz-1.7B-VoiceDesign-4bit"

func newCallTest(t *testing.T, ready bool) *callTest {
	t.Helper()
	d := &callTest{api: tt.NewAPI(), tok: &memSecret{}}
	t.Cleanup(d.api.Close)
	savedBase, savedTok, savedSecrets := twilioAPIBase, twilioTokenStore, llmSecrets
	twilioAPIBase, twilioTokenStore, llmSecrets = d.api.URL, d.tok, llm.StaticSecrets{}
	t.Cleanup(func() { twilioAPIBase, twilioTokenStore, llmSecrets = savedBase, savedTok, savedSecrets })
	if ready {
		d.tok.v = tt.AuthToken
	}
	d.a, d.eng, d.tt = newTestApp(t)
	t.Cleanup(d.a.closeCalls)
	d.tt.SetSize(1100, 2200) // tall: the forms fit without scrolling
	if ready {
		port := freePort(t)
		d.a.updateSettings(func(s *settings.Settings) {
			s.Calls = settings.Calls{Enabled: true, AccountSID: tt.AccountSID, PublicURL: "https://calls.example.trycloudflare.com",
				GatewayPort: port, Number: tt.Number, NumberSID: tt.NumberSID, UserName: "Sam", Verified: time.Now().Unix()}
		})
		d.a.applyCalls()
		useFakeLLM(d.a, &fakeLLM{answer: func(llm.Request) (string, error) { return "SAY: Hello.", nil }})
		v, err := d.a.lib.svc.Voices.Save(context.Background(), core.Voice{Name: "Nova", Kind: core.VoiceDesigned, Model: callTestDesign,
			Design: "A warm, calm woman's voice", Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		d.voice = v
		d.a.voices = append(d.a.voices, v)
	}
	// The token store was read when the calls opened; read it again.
	tok, _ := twilioTokenStore.Get()
	d.a.callToken.set(tok)
	d.a.callHasTok = tok != ""
	return d
}

func (d *callTest) click(t *testing.T, label string) {
	t.Helper()
	d.tt.Frame()
	if err := d.tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %q)", label, err, d.tt.Texts())
	}
	d.tt.Frame()
}

func (d *callTest) shows(t *testing.T, texts ...string) {
	t.Helper()
	d.tt.Frame()
	all := strings.Join(d.tt.Texts(), "\n")
	for _, s := range texts {
		if !strings.Contains(all, s) {
			t.Errorf("%q not shown in:\n%s", s, all)
		}
	}
}

func TestCallsReadinessAndForm(t *testing.T) {
	d := newCallTest(t, false)
	a := d.a
	a.navigate("/calls")
	d.shows(t, "Finish setup to place calls", "of 6 ready", "Twilio account connected", "No calls yet. Your calls will show up here.",
		"No voice can place calls yet.")
	// The number's hints.
	a.callForm.number = "4420"
	d.shows(t, "Start with + and the country code, like +1 or +44.")
	a.callForm.number = "+44 20 7946 0958"
	d.shows(t, "Calling the United Kingdom")
	a.callForm.number = "+15550100199"
	d.tt.Frame()
	if strings.Contains(strings.Join(d.tt.Texts(), "\n"), "Calling") && strings.Contains(strings.Join(d.tt.Texts(), "\n"), "Calling the") {
		t.Error("+1 is named")
	}
	// An example fills the task; the plan shows it with the guardrails.
	d.click(t, "Ask opening hours")
	if !strings.HasPrefix(a.callForm.brief, "Ask what their opening hours are") {
		t.Errorf("brief %q", a.callForm.brief)
	}
	d.shows(t, "Plan preview", "Won't share payment details, passwords or ID numbers.", "Ends politely, and hangs up after 5 min at most.",
		"Finish the setup steps above to place calls.")
	// Without a disclosure, a warning.
	a.callForm.disclose = false
	d.shows(t, "Without a disclosure the other person may not know they are talking to an AI. In many places telling them is required.")
}

func TestCallsPlaceFromForm(t *testing.T) {
	d := newCallTest(t, true)
	a := d.a
	a.navigate("/calls")
	d.tt.Frame()
	if ready, n := callsReady(a.callChecks()); !ready {
		t.Fatalf("not ready (%d): %+v", n, a.callChecks())
	}
	if a.callGWAddr == "" {
		t.Fatalf("gateway not listening: %s", a.callGWErr)
	}
	a.callForm.number = "+44 20 7946 0958"
	a.callForm.brief = "Book a table for two tonight."
	d.shows(t, "Nova · Designed")
	if a.callForm.disclosure != "Hi, this is an AI assistant calling on behalf of Sam." {
		t.Errorf("disclosure %q", a.callForm.disclosure)
	}
	d.click(t, "Call +442079460958")
	if !a.callAsk {
		t.Fatal("no confirmation")
	}
	d.shows(t, "Call +442079460958 now?", "AudioSmith will call as Nova and follow this task for up to 5 min:")
	// Cancel is focused: Enter would not dial.
	d.click(t, "Cancel")
	if a.callAsk || len(d.api.PlacedCalls()) != 0 {
		t.Fatal("cancel dialled")
	}
	d.click(t, "Call +442079460958")
	if !a.callAsk {
		t.Fatal("no confirmation")
	}
	a.placeCall() // the dialog's Call ("Call" is also the tab's label)
	d.tt.Frame()
	placed := d.api.PlacedCalls()
	if len(placed) != 1 || placed[0].Get("To") != "+442079460958" || placed[0].Get("From") != tt.Number {
		t.Fatalf("placed %v (err %q)", placed, a.callErr)
	}
	c := a.callFull[a.callSel]
	if c == nil || c.Phase != services.CallDialing || c.Disclosure != "Hi, this is an AI assistant calling on behalf of Sam." || c.VoiceName != "Nova" {
		t.Fatalf("call %+v", c)
	}
	if a.callForm.number != "" || a.callForm.brief != "" {
		t.Error("form kept")
	}
	d.shows(t, "Dialing", "Hang up", "+44••••••0958")
	// Twilio reports progress through the gateway: the call rings.
	params := url.Values{"CallSid": {c.SID}, "CallStatus": {"ringing"}}
	resp, _, err := tt.Webhook("http://"+a.callGWAddr, a.prefs.Calls.Public(), "/twilio/status?call="+c.ID, "", params)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status webhook %v %v", err, resp)
	}
	d.tt.Frame()
	if a.callFull[c.ID].Phase != services.CallRinging {
		t.Errorf("phase %s", a.callFull[c.ID].Phase)
	}
	// Hang up while ringing: Twilio is told.
	d.click(t, "Hang up")
	if h := d.api.HungUp(); len(h) != 1 || h[0] != c.SID {
		t.Errorf("hangups %v", h)
	}
	// The call is in the Projects page's library.
	items, _ := a.lib.svc.Library.Search(context.Background(), store.LibraryQuery{Category: store.LibCall})
	if len(items) != 1 || items[0].Source != "call" {
		t.Errorf("library %+v", items)
	}
}

func TestCallsLiveView(t *testing.T) {
	d := newCallTest(t, true)
	a := d.a
	now := time.Now()
	live := &services.Call{ID: "c1", Direction: services.CallOutbound, Number: "+442079460958", Phase: services.CallLive, Created: now,
		Answered: now.Add(-75 * time.Second), Mode: telephony.ModeAgent, Brief: "Book a table", VoiceName: "Nova", MaxMinutes: 5,
		State: telephony.StateSpeaking, Takeover: true, Transcript: []telephony.Line{
			{Speaker: telephony.SpeakerAgent, Text: "Hi, this is an AI assistant calling for Sam."},
			{Speaker: telephony.SpeakerCaller, Text: "Hello, Luigi's.", At: 4 * time.Second},
			{Speaker: telephony.SpeakerAgent, Text: "I'd like a table", At: 6 * time.Second, Interrupted: true},
		}}
	a.callChanged(services.CallChanged{ID: "c1", Call: live})
	a.navigate("/calls")
	a.selectCall("c1")
	d.shows(t, "Speaking", "1:15 / 5:00", "Hello, Luigi's.", "I'd like a table —", "Take over")
	// Taken over: Say is offered (the call is not the service's, so it
	// answers with a toast).
	a.callSay = "Sam here."
	d.click(t, "Say")
	// The call ends: summary and actions.
	ended := *live
	ended.Phase, ended.State, ended.Outcome, ended.Summary, ended.Duration = services.CallEnded, "", telephony.OutcomeBooked, "Table for two at 7 pm.", 92
	ended.Turns = []telephony.TurnLatency{{STT: 300 * time.Millisecond, LLMFirst: 500 * time.Millisecond, TTSFirst: 400 * time.Millisecond, Total: 1400 * time.Millisecond}}
	a.callChanged(services.CallChanged{ID: "c1", Call: &ended})
	d.shows(t, "Booked", "Table for two at 7 pm.", "Call again", "Copy summary", "Average response 1.4 s")
	d.click(t, "Call again")
	if a.callForm.brief != "Book a table" || a.callForm.number != "+442079460958" || a.callTab != 0 {
		t.Errorf("call again %+v", a.callForm)
	}
	found := false
	for _, n := range a.notices {
		found = found || n.Title == "Call ended"
	}
	if !found {
		t.Error("no notification")
	}
}

func TestCallsInboundDialog(t *testing.T) {
	d := newCallTest(t, true)
	a := d.a
	a.navigate("/calls")
	d.click(t, "Incoming calls")
	d.shows(t, "Choose what happens when someone calls your Twilio number.", "Play your greeting, then hang up.")
	a.callInMode = 1
	a.callInBrief = "Take a message."
	d.click(t, "Save")
	if a.prefs.Calls.InboundMode() != settings.InboundAgent || a.prefs.Calls.InboundBrief != "Take a message." {
		t.Errorf("inbound %+v", a.prefs.Calls)
	}
}

func TestTwilioSetup(t *testing.T) {
	d := newCallTest(t, false)
	a := d.a
	a.navigate("/calls/setup")
	d.shows(t, "Twilio setup", "Not set up", "1 · Twilio account", "2 · Public tunnel", "3 · Phone number", "4 · Voice and behaviour",
		"cloudflared tunnel --url http://127.0.0.1:3950", "Readiness")
	// Step 1: a bad SID, then the right credentials.
	a.tw.sid, a.tw.token = "AC123", "x"
	d.click(t, "Save and check")
	d.shows(t, "The Account SID starts with AC followed by 32 letters and digits.")
	a.tw.sid, a.tw.token = tt.AccountSID, "wrong"
	d.click(t, "Save and check")
	d.shows(t, "Twilio did not accept the Account SID or auth token.")
	a.tw.sid, a.tw.token = tt.AccountSID, tt.AuthToken
	d.click(t, "Save and check")
	if d.tok.v != tt.AuthToken || a.prefs.Calls.AccountSID != tt.AccountSID || a.tw.account == nil {
		t.Fatalf("account %q %q %+v (%s)", d.tok.v, a.prefs.Calls.AccountSID, a.tw.account, a.tw.accountErr)
	}
	d.shows(t, "Account AC012345…", "Test account", "Replace", "Remove")
	// Step 2: the tunnel; ngrok's commands; a bad address.
	a.tw.tool = 1
	d.shows(t, "ngrok http http://127.0.0.1:3950")
	a.tw.publicURL = "http://insecure.example"
	a.saveTunnel()
	d.shows(t, "Twilio needs an https address.")
	port := freePort(t)
	a.tw.publicURL, a.tw.port = "https://calls.example.trycloudflare.com/", float64(port)
	a.saveTunnel()
	if a.prefs.Calls.Public() != "https://calls.example.trycloudflare.com" || a.prefs.Calls.Port() != port || a.prefs.Calls.TunnelTool() != "ngrok" {
		t.Fatalf("tunnel %+v", a.prefs.Calls)
	}
	// Check before calls are on: the gateway does not listen.
	a.checkTunnel()
	d.shows(t, "Turn on calls first: the gateway is not listening.")
	d.click(t, "Turn on calls")
	if a.callGWAddr == "" {
		t.Fatalf("gateway: %s", a.callGWErr)
	}
	// The tunnel check reaches the gateway (the public host is routed to it).
	gw := a.callGWAddr
	saved := twilioHTTP
	twilioHTTP = func(string) *http.Client {
		return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", gw)
		}}}
	}
	t.Cleanup(func() { twilioHTTP = saved })
	// The test transport speaks plain HTTP to the gateway: point the
	// check at http for it.
	a.prefs.Calls.PublicURL = "http://calls.example.trycloudflare.com"
	a.checkTunnel()
	d.shows(t, "The tunnel reaches the gateway on "+gw+".")
	a.prefs.Calls.PublicURL = "https://calls.example.trycloudflare.com"
	// Step 3: the numbers; the SMS-only one is refused; the voice one is used.
	d.click(t, "Load my numbers")
	if len(a.tw.numbers) != 2 {
		t.Fatalf("numbers %v (%s)", a.tw.numbers, a.tw.numbersErr)
	}
	a.tw.numberSel = numberLabel(a.tw.numbers[1])
	d.click(t, "Use this number")
	d.shows(t, "This number cannot take voice calls.")
	a.tw.numberSel = numberLabel(a.tw.numbers[0])
	d.click(t, "Use this number")
	if a.prefs.Calls.Number != tt.Number || d.api.Webhooks[tt.NumberSID] != "https://calls.example.trycloudflare.com/twilio/voice" {
		t.Fatalf("number %q webhook %q (%s)", a.prefs.Calls.Number, d.api.Webhooks[tt.NumberSID], a.tw.hookMsg)
	}
	d.shows(t, "Incoming calls to +15550100100 now reach AudioSmith.", "https://calls.example.trycloudflare.com/twilio/voice",
		"This step is done once a request signed by Twilio arrives: call your number, or place a call.")
	// A signed request marks it done.
	resp, _, err := tt.Webhook("http://"+gw, "https://calls.example.trycloudflare.com", "/twilio/status", "", url.Values{"CallSid": {"CA9"}, "CallStatus": {"completed"}})
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("webhook %v %v", err, resp)
	}
	if a.prefs.Calls.Verified == 0 {
		t.Error("not verified")
	}
	d.shows(t, "Twilio reached AudioSmith with a signed request on")
	// Step 4: behaviour.
	a.tw.userName, a.tw.inbound, a.tw.greeting, a.tw.record = "Sam", 0, "Sam is away.", true
	a.tw.disclosure = "Hi, I'm Sam's AI assistant."
	d.shows(t, "Your disclosure does not say the call is recorded, so calls won't be.")
	a.saveTwilioBehaviour()
	if c := a.prefs.Calls; c.UserName != "Sam" || c.Greeting != "Sam is away." || !c.Record || c.Disclosure != "Hi, I'm Sam's AI assistant." {
		t.Errorf("behaviour %+v", c)
	}
	// Remove forgets the token and turns calls off.
	d.click(t, "Remove")
	if d.tok.v != "" || a.prefs.Calls.Enabled || a.callGWAddr != "" {
		t.Errorf("removed: %q %v %q", d.tok.v, a.prefs.Calls.Enabled, a.callGWAddr)
	}
}

func TestTwilioIntegrationCard(t *testing.T) {
	d := newCallTest(t, true)
	a := d.a
	a.navigate("/integrations/twilio")
	d.shows(t, "Twilio setup", "Test", "Play phone-quality preview")
	a.navigate("/integrations")
	d.shows(t, "Twilio", "An AI agent that places and answers phone calls in your voice.")
}

// TestCallsInertByDefault: unconfigured, calls open no listener, reach no
// network, and the pages only point at the setup.
func TestCallsInertByDefault(t *testing.T) {
	d := newCallTest(t, false)
	a := d.a
	for _, p := range []string{"/studio", "/calls", "/calls/setup", "/projects", "/integrations"} {
		a.navigate(p)
		d.tt.Frame()
	}
	if a.prefs.Calls.Enabled || a.callGWAddr != "" || a.callGW.Addr() != "" {
		t.Errorf("gateway on: %v %q %q", a.prefs.Calls.Enabled, a.callGWAddr, a.callGW.Addr())
	}
	if n := d.api.RequestCount(); n != 0 {
		t.Errorf("%d requests to Twilio", n)
	}
	a.navigate("/calls")
	d.shows(t, "Finish setup to place calls", "Set up Twilio")
	// Placing a call is refused before anything is sent.
	if _, err := a.callSvc.Place(context.Background(), services.CallRequest{To: "+15550100111", Brief: "x", VoiceID: "v"}); err == nil {
		t.Error("placed without setup")
	}
	if n := d.api.RequestCount(); n != 0 {
		t.Errorf("%d requests to Twilio", n)
	}
}
