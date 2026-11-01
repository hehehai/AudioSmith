package telephony

// The agent loop of one call:
//
//	caller μ-law ─▶ 16 kHz ─▶ endpointer ─┬─ speech started ─▶ barge-in: clear playback, cancel the reply
//	                                      └─ utterance ─▶ STT (one at a time, in order) ─▶ transcript
//	transcript ─▶ LLM (streamed, SAY/OUTCOME/HANGUP lines) ─▶ sentences ─▶ guard ─▶ TTS ─▶ 8 kHz μ-law ─▶ Twilio, a mark per sentence
//
// The disclosure is spoken first and cannot be interrupted. One goroutine
// (Run's) owns the state; the stream reader, the STT worker and each
// reply (LLM + speaker) run on their own and report back by message.
// Every reply turn has a generation: cancelling it (barge-in, take-over,
// "Say…", hang-up) bumps the generation, and audio of an older one is
// dropped before it reaches the stream.
//
// Files: agent.go (types, the Agent and its controls), agent_run.go (Run
// and its state), agent_reply.go (the replies), agent_playback.go (audio
// out), agent_recorder.go (the recording).

import (
	"context"
	"sync"
	"time"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// Stream is the agent's side of a Media Stream (*MediaStream; fakes in
// tests).
type Stream interface {
	Next() (Event, error)
	SendAudio(mu []byte) error
	SendMark(name string) error
	Clear() error
	Close()
}

// Agent modes.
const (
	ModeAgent    = "agent"    // a conversation with the language model
	ModeGreeting = "greeting" // say the disclosure and the greeting, then hang up
)

// Agent states.
const (
	StateListening = "listening"
	StateThinking  = "thinking"
	StateSpeaking  = "speaking"
)

// Speakers of transcript lines.
const (
	SpeakerAgent  = "agent"  // the AI, in the call's voice
	SpeakerCaller = "caller" // the other person
	SpeakerUser   = "user"   // the user's own words (take-over), spoken in the call's voice
)

// Why a call ended.
const (
	EndCaller = "caller" // the other side hung up (the stream stopped)
	EndAgent  = "agent"  // the agent said goodbye and hung up
	EndUser   = "user"   // Hang up in the app (or the app quit)
	EndLimit  = "limit"  // the maximum length was reached
	EndError  = "error"  // speech failed repeatedly
)

// Notice codes of EvNotice.
const (
	NoticeWithheld = "withheld"   // the guard replaced a sentence
	NoticeWrapUp   = "wrap_up"    // 30 s left: the model was told to wrap up
	NoticeSTT      = "stt_failed" // an utterance could not be transcribed
	NoticeLLM      = "llm_failed"
	NoticeTTS      = "tts_failed"
	NoticeTakeover = "takeover" // Detail "on" / "off"
	NoticeDigit    = "digit"    // the caller pressed a key (Detail)
)

// Line is a transcript line.
type Line struct {
	Speaker     string        `json:"speaker"`
	Text        string        `json:"text"`
	At          time.Duration `json:"at"` // since the call was answered
	Interrupted bool          `json:"interrupted,omitempty"`
}

// TurnLatency measures one turn, from the end of the caller's speech (or
// the start of the turn) to the first audio sent.
type TurnLatency struct {
	STT      time.Duration `json:"stt"`      // transcription
	LLMFirst time.Duration `json:"llmFirst"` // the model's first text
	Sentence time.Duration `json:"sentence"` // the first complete sentence
	TTSFirst time.Duration `json:"ttsFirst"` // the first sentence's audio, after the sentence
	Total    time.Duration `json:"total"`    // speech end → first audio
}

// EventKind is the kind of an agent event.
type EventKind int

const (
	EvState   EventKind = iota + 1 // State changed
	EvLine                         // Line (Index) was added or grew
	EvLatency                      // a turn's Latency
	EvOutcome                      // the model reported Outcome and Note
	EvNotice                       // Notice (and Detail)
)

// AgentEvent is something that happened in the call.
type AgentEvent struct {
	Kind    EventKind
	State   string
	Index   int // of Line in the transcript
	Line    Line
	Latency TurnLatency
	Outcome string
	Note    string
	Notice  string
	Detail  string
}

// AgentResult is how the call went.
type AgentResult struct {
	End        string // End*
	Outcome    string // as the model reported it ("" = unknown)
	Note       string
	Transcript []Line
	Turns      []TurnLatency
	// Recording is the call in stereo at 8 kHz (left the caller, right
	// the agent) when Agent.Record was set.
	Recording *dsp.Buffer
}

// Agent is one call's conversation. Fill the fields, then Run.
type Agent struct {
	Stream Stream
	// STT transcribes an utterance (16 kHz mono).
	STT func(ctx context.Context, pcm []float32) (string, error)
	// LLM answers the conversation, calling onDelta with streamed text.
	LLM func(ctx context.Context, msgs []Message, onDelta func(string)) (string, error)
	// TTS speaks text, handing audio (mono, at rate) to out as it is made.
	TTS func(ctx context.Context, text string, out func(pcm []float32, rate int) error) error

	Mode       string // ModeAgent (default) or ModeGreeting
	Brief      Brief
	Disclosure string // spoken first, uninterruptibly ("" = none)
	Greeting   string // ModeGreeting: said after the disclosure
	// ClosingLine is said when the time limit is reached; "" = a default
	// English line.
	ClosingLine string
	MaxDuration time.Duration // 0 = no limit
	Record      bool
	Endpoint    EndpointOptions
	// Events reports what happens, on Run's goroutine; it must not block.
	Events func(AgentEvent)
	Logf   func(string, ...any)

	now      func() time.Time
	msgs     chan agentMsg
	done     chan struct{}
	controls chan agentMsg
	once     sync.Once
}

// DefaultClosingLine ends a call at its limit.
const DefaultClosingLine = "I'm sorry, I have to end the call now. Thank you, goodbye."

type msgKind int

const (
	mSpeechStart msgKind = iota + 1
	mUtterance
	mMark
	mDigit
	mStreamEnd
	mTranscript
	mSpoken
	mFirstAudio
	mFirstText
	mTurnDone
	mWithheld
	mTakeover
	mSay
	mHangup
)

type agentMsg struct {
	kind  msgKind
	gen   int
	text  string
	audio []float32
	on    bool
	at    time.Time
	dur   time.Duration
	err   error
	reply Reply
}

func (a *Agent) init() {
	a.once.Do(func() {
		if a.now == nil {
			a.now = time.Now
		}
		a.msgs = make(chan agentMsg, 256)
		a.controls = make(chan agentMsg, 16)
		a.done = make(chan struct{})
		if a.Logf == nil {
			a.Logf = func(string, ...any) {}
		}
	})
}

// send hands a message to Run's goroutine, unless Run has returned.
func (a *Agent) send(m agentMsg) {
	select {
	case a.msgs <- m:
	case <-a.done:
	}
}

func (a *Agent) control(m agentMsg) {
	a.init()
	select {
	case a.controls <- m:
	case <-a.done:
	}
}

// Takeover pauses (on) or resumes the language model: while taken over,
// the caller is still transcribed and Say speaks the user's lines.
func (a *Agent) Takeover(on bool) { a.control(agentMsg{kind: mTakeover, on: on}) }

// Say speaks text in the call's voice now, interrupting the agent.
func (a *Agent) Say(text string) { a.control(agentMsg{kind: mSay, text: text}) }

// Hangup ends the call now.
func (a *Agent) Hangup() { a.control(agentMsg{kind: mHangup}) }
