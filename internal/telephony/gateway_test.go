package telephony_test

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/telephony"
	tt "github.com/hehehai/AudioSmith/internal/telephony/telephonytest"
)

const public = "https://calls.example.trycloudflare.com"

func TestClient(t *testing.T) {
	api := tt.NewAPI()
	defer api.Close()
	c := api.Client()
	ctx := context.Background()
	acc, err := c.Account(ctx)
	if err != nil || acc.Status != "active" || acc.SID != tt.AccountSID {
		t.Fatalf("account %+v %v", acc, err)
	}
	nums, err := c.Numbers(ctx)
	if err != nil || len(nums) != 2 || nums[0].Number != tt.Number || !nums[0].Capabilities.Voice || nums[1].Capabilities.Voice {
		t.Fatalf("numbers %+v %v", nums, err)
	}
	n, err := c.SetVoiceWebhook(ctx, tt.NumberSID, telephony.VoiceURL(public), telephony.StatusURL(public))
	if err != nil || n.VoiceURL != public+"/twilio/voice" || api.Webhooks[tt.NumberSID] != public+"/twilio/voice" {
		t.Fatalf("webhook %+v %v", n, err)
	}
	info, err := c.Call(ctx, telephony.CallRequest{To: "+442079460958", From: tt.Number, URL: telephony.VoiceURL(public) + "?call=c1",
		StatusURL: telephony.StatusURL(public) + "?call=c1", TimeLimit: 5*time.Minute + 30*time.Second})
	if err != nil || !strings.HasPrefix(info.SID, "CA") {
		t.Fatalf("call %+v %v", info, err)
	}
	placed := api.PlacedCalls()[0]
	if placed.Get("To") != "+442079460958" || placed.Get("TimeLimit") != "330" || len(placed["StatusCallbackEvent"]) != 4 || placed.Get("Method") != "POST" {
		t.Errorf("placed %v", placed)
	}
	if err := c.Hangup(ctx, info.SID); err != nil || api.HungUp()[0] != info.SID {
		t.Errorf("hangup %v", err)
	}
	// Errors.
	api.FailCall = &telephony.APIError{Status: 400, Code: 21219, Message: "The number is unverified. Trial accounts may only make calls to verified numbers."}
	_, err = c.Call(ctx, telephony.CallRequest{To: "+15550100111", From: tt.Number, URL: "x"})
	var ae *telephony.APIError
	if !errors.As(err, &ae) || ae.Code != 21219 || !strings.Contains(err.Error(), "unverified") {
		t.Errorf("trial error %v", err)
	}
	bad := api.Client()
	bad.AuthToken = "wrong"
	if _, err := bad.Account(ctx); !telephony.Unauthorized(err) {
		t.Errorf("unauthorized %v", err)
	}
	empty := &telephony.Client{Base: api.URL}
	if _, err := empty.Numbers(ctx); !telephony.Unauthorized(err) {
		t.Errorf("no credentials %v", err)
	}
}

// hooks is a gateway's app side for tests.
type hooks struct {
	mu       sync.Mutex
	signed   int
	rejected []string
	statuses []telephony.StatusEvent
	voice    func(telephony.VoiceRequest) telephony.VoiceAnswer
	stream   func(ctx context.Context, callID string, s *telephony.MediaStream)
}

func (h *hooks) Voice(_ context.Context, r telephony.VoiceRequest) telephony.VoiceAnswer {
	if h.voice != nil {
		return h.voice(r)
	}
	if r.CallID != "" {
		return telephony.VoiceAnswer{CallID: r.CallID}
	}
	return telephony.VoiceAnswer{Reject: "busy"}
}

func (h *hooks) Status(_ context.Context, ev telephony.StatusEvent) {
	h.mu.Lock()
	h.statuses = append(h.statuses, ev)
	h.mu.Unlock()
}

func (h *hooks) Stream(ctx context.Context, callID string, s *telephony.MediaStream) {
	if h.stream != nil {
		h.stream(ctx, callID, s)
	}
}

func (h *hooks) Signed() {
	h.mu.Lock()
	h.signed++
	h.mu.Unlock()
}

func (h *hooks) Rejected(reason, _ string) {
	h.mu.Lock()
	h.rejected = append(h.rejected, reason)
	h.mu.Unlock()
}

func (h *hooks) rejections() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.rejected...)
}

func startGateway(t *testing.T, h *hooks) (*telephony.Gateway, string) {
	t.Helper()
	g := telephony.NewGateway(func() telephony.GatewayConfig {
		return telephony.GatewayConfig{AccountSID: tt.AccountSID, AuthToken: tt.AuthToken, PublicURL: public}
	}, h, t.Logf)
	if err := g.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.Close(ctx)
	})
	return g, "http://" + g.Addr()
}

func TestGatewaySecurity(t *testing.T) {
	h := &hooks{}
	g, base := startGateway(t, h)
	params := url.Values{"CallSid": {"CA1"}, "From": {"+15550100111"}, "To": {tt.Number}, "Direction": {"inbound"}}

	// A valid incoming call: the hooks said busy.
	resp, body, err := tt.Webhook(base, public, "/twilio/voice", "", params)
	if err != nil || resp.StatusCode != 200 || !strings.Contains(body, `<Reject reason="busy"/>`) {
		t.Fatalf("voice %v %d %s", err, resp.StatusCode, body)
	}
	// Signed for another URL, or with another token: refused.
	resp, _, _ = tt.Webhook(base, "https://evil.example", "/twilio/voice", "", params)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong url: %d", resp.StatusCode)
	}
	resp, _, _ = tt.Webhook(base, public, "/twilio/voice", "other-token", params)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	other := url.Values{"CallSid": {"CA2"}, "AccountSid": {"AC99999999999999999999999999999999"}}
	resp, _, _ = tt.Webhook(base, public, "/twilio/voice", "", other)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong account: %d", resp.StatusCode)
	}
	// Status events reach the hooks.
	resp, _, _ = tt.Webhook(base, public, "/twilio/status?call=c9", "", url.Values{"CallSid": {"CA1"}, "CallStatus": {"completed"}, "CallDuration": {"42"}})
	if resp.StatusCode != http.StatusNoContent || len(h.statuses) != 1 || h.statuses[0].CallID != "c9" || h.statuses[0].Duration != 42*time.Second {
		t.Errorf("status %d %+v", resp.StatusCode, h.statuses)
	}
	// Nothing else is served.
	for _, p := range []string{"/", "/v1/models", "/api/voices", "/twilio/ping/unknown"} {
		r, err := http.Get(base + p)
		if err != nil || r.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %v %v", p, err, r.StatusCode)
		}
	}
	if r, _ := http.Get(base + "/twilio/voice"); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET voice %d", r.StatusCode)
	}
	// The reachability check.
	nonce := g.ExpectPing()
	if r, _ := http.Get(base + telephony.PathPing + nonce); r.StatusCode != http.StatusNoContent || !g.Pinged(nonce) {
		t.Errorf("ping %d", r.StatusCode)
	}
	if got := h.rejections(); strings.Join(got, ",") != "invalid_signature,invalid_signature,wrong_account" {
		t.Errorf("rejections %v", got)
	}
	if h.signed != 2 {
		t.Errorf("signed %d", h.signed)
	}
	// Ten failures in a minute: everything is refused for the rest of it.
	for range 8 {
		tt.Webhook(base, public, "/twilio/voice", "nope", params)
	}
	resp, _, _ = tt.Webhook(base, public, "/twilio/voice", "", params)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after failures: %d", resp.StatusCode)
	}
}

func TestGatewayStreamTokens(t *testing.T) {
	h := &hooks{}
	got := make(chan string, 4)
	h.stream = func(ctx context.Context, callID string, s *telephony.MediaStream) {
		got <- callID + " " + s.Start.CallSID
		s.Close()
	}
	_, base := startGateway(t, h)
	params := url.Values{"CallSid": {"CA7"}, "Direction": {"outbound-api"}}
	resp, twiml, err := tt.Webhook(base, public, "/twilio/voice?call=c7", "", params)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err)
	}
	if !strings.Contains(twiml, `<Stream url="wss://calls.example.trycloudflare.com/twilio/stream">`) {
		t.Fatalf("twiml %s", twiml)
	}
	tok := tt.StreamToken(twiml)
	ws := "ws://" + strings.TrimPrefix(base, "http://") + telephony.PathStream
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := tt.Dial(ctx, ws, tok, "CA7")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "c7 CA7" {
			t.Errorf("stream %q", s)
		}
	case <-ctx.Done():
		t.Fatal("no stream")
	}
	<-m.Done()
	// The token was single-use; a made-up one is refused too.
	for _, bad := range []string{tok, "made-up"} {
		m2, err := tt.Dial(ctx, ws, bad, "CA7")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-m2.Done():
		case <-ctx.Done():
			t.Fatal("bad token kept open")
		}
	}
	if r := h.rejections(); len(r) != 2 || r[0] != telephony.RejectToken {
		t.Errorf("rejections %v", r)
	}
	select {
	case s := <-got:
		t.Errorf("a bad token reached the hooks: %s", s)
	default:
	}
}

// fakeSpeech is scripted STT, LLM and TTS for an agent.
type fakeSpeech struct {
	mu      sync.Mutex
	heard   []string // what STT returns, in order
	replies []string // what the LLM answers, in order
	llmMsgs [][]telephony.Message
	said    []string
	ttsWait time.Duration
}

func (f *fakeSpeech) stt(ctx context.Context, pcm []float32) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(pcm) < telephony.ListenRate/4 {
		return "", errors.New("too short")
	}
	if len(f.heard) == 0 {
		return "", nil
	}
	s := f.heard[0]
	f.heard = f.heard[1:]
	return s, nil
}

func (f *fakeSpeech) llm(ctx context.Context, msgs []telephony.Message, onDelta func(string)) (string, error) {
	f.mu.Lock()
	f.llmMsgs = append(f.llmMsgs, msgs)
	r := "SAY: Okay."
	if len(f.replies) > 0 {
		r = f.replies[0]
		f.replies = f.replies[1:]
	}
	f.mu.Unlock()
	for i := 0; i < len(r); i += 6 {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		onDelta(r[i:min(i+6, len(r))])
	}
	return r, nil
}

func (f *fakeSpeech) tts(ctx context.Context, text string, out func([]float32, int) error) error {
	f.mu.Lock()
	f.said = append(f.said, text)
	wait := f.ttsWait
	f.mu.Unlock()
	const rate = 24000
	n := rate / 10 // 100 ms per chunk, two chunks
	for c := range 2 {
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		pcm := make([]float32, n)
		for i := range pcm {
			pcm[i] = float32(0.3 * math.Sin(2*math.Pi*220*float64(c*n+i)/rate))
		}
		if err := out(pcm, rate); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSpeech) saidLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.said...)
}

// callRig runs an agent per stream and keeps its result and events.
type callRig struct {
	t      *testing.T
	base   string
	g      *telephony.Gateway
	agent  chan *telephony.Agent
	result chan telephony.AgentResult
	mu     sync.Mutex
	events []telephony.AgentEvent
}

func newCallRig(t *testing.T, newAgent func() *telephony.Agent) *callRig {
	r := &callRig{t: t, agent: make(chan *telephony.Agent, 1), result: make(chan telephony.AgentResult, 1)}
	h := &hooks{}
	h.stream = func(ctx context.Context, callID string, s *telephony.MediaStream) {
		a := newAgent()
		a.Stream = s
		a.Events = func(ev telephony.AgentEvent) {
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.mu.Unlock()
		}
		a.Logf = t.Logf
		r.agent <- a
		r.result <- a.Run(ctx)
	}
	r.g, r.base = startGateway(t, h)
	return r
}

// dial answers the call: the voice webhook, then the stream.
func (r *callRig) dial() (*tt.Media, *telephony.Agent) {
	r.t.Helper()
	_, twiml, err := tt.Webhook(r.base, public, "/twilio/voice?call=c1", "", url.Values{"CallSid": {"CA1"}, "Direction": {"outbound-api"}})
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := tt.Dial(ctx, "ws://"+strings.TrimPrefix(r.base, "http://")+telephony.PathStream, tt.StreamToken(twiml), "CA1")
	if err != nil {
		r.t.Fatal(err)
	}
	select {
	case a := <-r.agent:
		return m, a
	case <-ctx.Done():
		r.t.Fatal("no agent")
	}
	return nil, nil
}

func (r *callRig) wait() telephony.AgentResult {
	r.t.Helper()
	select {
	case res := <-r.result:
		return res
	case <-time.After(15 * time.Second):
		r.t.Fatal("the call did not end")
	}
	return telephony.AgentResult{}
}

// waitListening waits until the agent listens again (its audio played).
func (r *callRig) waitListening() {
	r.t.Helper()
	eventually(r.t, "listening", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i := len(r.events) - 1; i >= 0; i-- {
			if r.events[i].Kind == telephony.EvState {
				return r.events[i].State == telephony.StateListening
			}
		}
		return false
	})
}

func (r *callRig) has(kind telephony.EventKind, fn func(telephony.AgentEvent) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if ev.Kind == kind && (fn == nil || fn(ev)) {
			return true
		}
	}
	return false
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func lines(res telephony.AgentResult) string {
	var out []string
	for _, l := range res.Transcript {
		s := l.Speaker + ": " + l.Text
		if l.Interrupted {
			s += " [cut]"
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func TestAgentConversation(t *testing.T) {
	f := &fakeSpeech{
		heard: []string{"Hello, Luigi's. What time would you like?"},
		replies: []string{
			"SAY: Hi! I'd like to book a table for two this Friday. Is that possible?",
			"SAY: Seven pm, please. My card is 4111 1111 1111 1111.\nOUTCOME: booked — Friday 7 pm for two\nSAY: Thank you, goodbye!\nHANGUP",
		},
	}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: f.llm, TTS: f.tts, Record: true,
			Disclosure: "Hi, this is an AI assistant calling for Sam. This call may be recorded.",
			Brief:      telephony.Brief{Task: "Book a table for two on Friday at 7 pm.", User: "Sam", Disclosure: "Hi, this is an AI assistant calling for Sam. This call may be recorded."}}
	})
	m, _ := rig.dial()
	// The disclosure, then the opening line.
	eventually(t, "opening line", func() bool { return len(f.saidLines()) >= 3 })
	rig.waitListening()
	if err := m.Speak(0.8); err != nil {
		t.Fatal(err)
	}
	res := rig.wait()
	<-m.Done()
	want := "agent: Hi, this is an AI assistant calling for Sam. This call may be recorded.\n" +
		"agent: Hi! I'd like to book a table for two this Friday. Is that possible?\n" +
		"caller: Hello, Luigi's. What time would you like?\n" +
		"agent: Seven pm, please. I'm sorry, I can't share that number on this call. Thank you, goodbye!"
	if got := lines(res); got != want {
		t.Errorf("transcript:\n%s\nwant:\n%s", got, want)
	}
	if res.End != telephony.EndAgent || res.Outcome != telephony.OutcomeBooked || res.Note != "Friday 7 pm for two" {
		t.Errorf("result %s %s %q", res.End, res.Outcome, res.Note)
	}
	if len(res.Turns) != 2 || res.Turns[1].STT < 0 || res.Turns[1].Total < res.Turns[1].TTSFirst ||
		(runtime.GOOS != "windows" && (res.Turns[1].STT <= 0 || res.Turns[1].Total <= 0)) {
		t.Errorf("turns %+v", res.Turns)
	}
	if !rig.has(telephony.EvNotice, func(ev telephony.AgentEvent) bool { return ev.Notice == telephony.NoticeWithheld }) {
		t.Error("no withheld notice")
	}
	for _, s := range []string{telephony.StateThinking, telephony.StateSpeaking, telephony.StateListening} {
		if !rig.has(telephony.EvState, func(ev telephony.AgentEvent) bool { return ev.State == s }) {
			t.Errorf("never %s", s)
		}
	}
	// The model saw the conversation (the disclosure in its instructions).
	last := f.llmMsgs[len(f.llmMsgs)-1]
	if last[0].Role != telephony.RoleSystem || !strings.Contains(last[0].Content, "Book a table") ||
		last[len(last)-1].Content != "Hello, Luigi's. What time would you like?" || last[len(last)-2].Role != telephony.RoleAssistant {
		t.Errorf("messages %+v", last)
	}
	// Played audio: about 0.2 s per sentence, at 8 kHz μ-law.
	if n := len(m.Played()); n < 5*1600*9/10 {
		t.Errorf("played %d bytes", n)
	}
	// The recording has the caller on the left and the agent on the right.
	rec := res.Recording
	if rec == nil || rec.Channels != 2 || rec.Rate != telephony.PhoneRate {
		t.Fatalf("recording %+v", rec)
	}
	l, r := dsp.Deinterleave(rec.Samples, 2)[0], dsp.Deinterleave(rec.Samples, 2)[1]
	if dsp.RMS(l) < 0.01 || dsp.RMS(r) < 0.01 {
		t.Errorf("recording levels %.3f %.3f", dsp.RMS(l), dsp.RMS(r))
	}
}

func TestAgentBargeIn(t *testing.T) {
	f := &fakeSpeech{
		heard:   []string{"Sorry, can you hold on?", "Okay, go ahead."},
		replies: []string{"SAY: First sentence here. Second sentence here. Third sentence here. Fourth one.", "SAY: Of course.", "SAY: Thanks.\nHANGUP"},
		ttsWait: 30 * time.Millisecond,
	}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: f.llm, TTS: f.tts, Brief: telephony.Brief{Task: "Ask opening hours"}}
	})
	m, _ := rig.dial()
	m.SetHold(true) // playback "takes long": marks are not echoed
	eventually(t, "speaking", func() bool {
		return rig.has(telephony.EvState, func(ev telephony.AgentEvent) bool { return ev.State == telephony.StateSpeaking })
	})
	if err := m.Speak(0.6); err != nil {
		t.Fatal(err)
	}
	eventually(t, "clear", func() bool { return m.Clears() > 0 })
	m.SetHold(false)
	eventually(t, "reply to the interruption", func() bool {
		return rig.has(telephony.EvLine, func(ev telephony.AgentEvent) bool { return ev.Line.Text == "Of course." })
	})
	time.Sleep(300 * time.Millisecond)
	m.Speak(0.6)
	res := rig.wait()
	tr := res.Transcript
	if len(tr) < 4 || !tr[0].Interrupted || tr[1].Speaker != telephony.SpeakerCaller {
		t.Errorf("transcript:\n%s", lines(res))
	}
	if strings.Contains(tr[0].Text, "Fourth") {
		t.Errorf("the interrupted line went on: %q", tr[0].Text)
	}
	// The model was told it was interrupted.
	found := false
	for _, msgs := range f.llmMsgs {
		for _, m := range msgs {
			if strings.HasPrefix(m.Content, "[They interrupted you.]") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the model was not told of the interruption")
	}
}

func TestAgentTakeoverSayHangup(t *testing.T) {
	f := &fakeSpeech{heard: []string{"Who is this?"}, replies: []string{"SAY: Hello, I'm calling about an appointment."}}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: f.llm, TTS: f.tts, Brief: telephony.Brief{Task: "Reschedule"}}
	})
	m, a := rig.dial()
	eventually(t, "opening", func() bool { return len(f.saidLines()) >= 1 })
	time.Sleep(300 * time.Millisecond)
	a.Takeover(true)
	eventually(t, "takeover notice", func() bool {
		return rig.has(telephony.EvNotice, func(ev telephony.AgentEvent) bool { return ev.Notice == telephony.NoticeTakeover && ev.Detail == "on" })
	})
	m.Speak(0.6)
	eventually(t, "caller line", func() bool {
		return rig.has(telephony.EvLine, func(ev telephony.AgentEvent) bool { return ev.Line.Speaker == telephony.SpeakerCaller })
	})
	time.Sleep(200 * time.Millisecond)
	f.mu.Lock()
	calls := len(f.llmMsgs)
	f.mu.Unlock()
	if calls != 1 {
		t.Errorf("the model answered while taken over: %d calls", calls)
	}
	a.Say("This is Sam myself, sorry about that.")
	eventually(t, "user line", func() bool {
		return rig.has(telephony.EvLine, func(ev telephony.AgentEvent) bool { return ev.Line.Speaker == telephony.SpeakerUser })
	})
	a.Hangup()
	res := rig.wait()
	<-m.Done()
	if res.End != telephony.EndUser {
		t.Errorf("end %s", res.End)
	}
	if got := lines(res); !strings.Contains(got, "user: This is Sam myself, sorry about that.") || !strings.Contains(got, "caller: Who is this?") {
		t.Errorf("transcript:\n%s", got)
	}
}

func TestAgentLimitGreetingAndCallerHangup(t *testing.T) {
	// The time limit: the closing line, then the end.
	f := &fakeSpeech{replies: []string{"SAY: Hello there."}}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: f.llm, TTS: f.tts, MaxDuration: 800 * time.Millisecond, ClosingLine: "Time is up, goodbye."}
	})
	m, _ := rig.dial()
	res := rig.wait()
	<-m.Done()
	if res.End != telephony.EndLimit || !strings.Contains(lines(res), "agent: Time is up, goodbye.") {
		t.Errorf("limit: %s\n%s", res.End, lines(res))
	}

	// Greeting mode: disclosure and greeting, then hang up.
	g := &fakeSpeech{}
	rig2 := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: g.stt, LLM: g.llm, TTS: g.tts, Mode: telephony.ModeGreeting,
			Disclosure: "This call uses an AI voice.", Greeting: "Sam can't take your call. Please call back later."}
	})
	m2, _ := rig2.dial()
	res2 := rig2.wait()
	<-m2.Done()
	if res2.End != telephony.EndAgent || len(g.llmMsgs) != 0 || g.saidLines()[0] != "This call uses an AI voice. Sam can't take your call. Please call back later." {
		t.Errorf("greeting: %s %v %q", res2.End, len(g.llmMsgs), g.saidLines())
	}

	// The caller hangs up.
	h := &fakeSpeech{replies: []string{"SAY: Hi, I'm calling for Sam."}}
	rig3 := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: h.stt, LLM: h.llm, TTS: h.tts}
	})
	m3, _ := rig3.dial()
	eventually(t, "opening", func() bool { return len(h.saidLines()) >= 1 })
	m3.Stop()
	if res3 := rig3.wait(); res3.End != telephony.EndCaller {
		t.Errorf("caller hangup: %s", res3.End)
	}
}

func TestAgentSpeechFailures(t *testing.T) {
	f := &fakeSpeech{}
	failing := func(ctx context.Context, msgs []telephony.Message, onDelta func(string)) (string, error) {
		return "", errors.New("model offline")
	}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: failing, TTS: f.tts, Brief: telephony.Brief{Task: "x"}}
	})
	m, _ := rig.dial()
	eventually(t, "first failure", func() bool {
		return rig.has(telephony.EvNotice, func(ev telephony.AgentEvent) bool { return ev.Notice == telephony.NoticeLLM })
	})
	f.mu.Lock()
	f.heard = []string{"Hello?"}
	f.mu.Unlock()
	m.Speak(0.6)
	res := rig.wait()
	if res.End != telephony.EndError || res.Outcome != telephony.OutcomeFailed {
		t.Errorf("failures: %s %s", res.End, res.Outcome)
	}
}

// TestAgentIgnoresEarlyHangup replays Qwen3 4B's English call recorded by
// thread #44 (HANGUP after every reply): the call goes on until the other
// person says goodbye, and HANGUP is never spoken.
func TestAgentIgnoresEarlyHangup(t *testing.T) {
	f := &fakeSpeech{
		heard: []string{"Good evening, Luigi's, how can I help you?", "Friday at seven... I'm sorry, we're fully booked at seven. I have seven forty-five.",
			"Great. Any dietary requirements?", "Perfect, see you Friday."},
		replies: []string{
			"SAY: Hi, I'm calling on behalf of Sam to book a table for two this Friday at 7 pm.  \nSAY: Would 7 pm work, or would 7:30 or 8 be better?  \nOUTCOME: needs_you — Sam needs to confirm preferred time  \nHANGUP",
			"SAY: We'd like to book a table for two this Friday at 7 pm, or if that's not available, 7:30 or 8.  \nSAY: Could you please confirm the availability?  \nOUTCOME: needs_you — Sam needs to confirm preferred time  \nHANGUP",
			"SAY: Thank you for letting me know. Would 7:45 work for Sam?  \nOUTCOME: needs_you — Sam needs to confirm 7:45 or another time  \nHANGUP",
			"SAY: Sam doesn't have any specific dietary requirements.  \nOUTCOME: needs_you — Sam needs to confirm preferred time  \nHANGUP",
			"SAY: Thank you, Sam. We’ll see you Friday at 7:45.  \nOUTCOME: booked — Table confirmed for 7:45 Friday  \nHANGUP",
		},
	}
	rig := newCallRig(t, func() *telephony.Agent {
		return &telephony.Agent{STT: f.stt, LLM: f.llm, TTS: f.tts,
			Brief: telephony.Brief{Task: "Book a table for two at 7 pm this Friday under the name Sam. If 7 is not available, 7:30 or 8 is fine.", User: "Sam"}}
	})
	m, _ := rig.dial()
	agentLines := func() int {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		n := 0
		for _, ev := range rig.events {
			if ev.Kind == telephony.EvLine && ev.Line.Speaker == telephony.SpeakerAgent && ev.Index >= n {
				n = ev.Index + 1
			}
		}
		return n
	}
	for turn := range 4 {
		// The agent answered the previous turn and still listens.
		eventually(t, "agent reply", func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return len(f.replies) == 4-turn && agentLines() > 0
		})
		rig.waitListening()
		time.Sleep(50 * time.Millisecond)
		select {
		case <-m.Done():
			t.Fatalf("the call ended after reply %d", turn+1)
		default:
		}
		if err := m.Speak(0.6); err != nil {
			t.Fatal(err)
		}
	}
	res := rig.wait()
	<-m.Done()
	if res.End != telephony.EndAgent || res.Outcome != telephony.OutcomeBooked {
		t.Errorf("result %s %s", res.End, res.Outcome)
	}
	if n := strings.Count(lines(res), "caller: "); n != 4 {
		t.Errorf("caller turns %d:\n%s", n, lines(res))
	}
	for _, s := range f.saidLines() {
		if strings.Contains(s, "HANGUP") || strings.Contains(s, "OUTCOME") {
			t.Errorf("spoke %q", s)
		}
	}
}
