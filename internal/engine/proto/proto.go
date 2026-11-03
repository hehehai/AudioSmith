// Package proto is the wire format of protocol v2 between AudioSmith and
// audiosmith-engine: JSON-RPC-shaped messages, one per line, over the
// engine's stdin (app → engine) and stdout (engine → app). The spec is
// docs/protocol-v2.md; engine/Fixtures/*.jsonl are golden transcripts that
// both this package's tests and the Swift tests decode, so the two sides
// cannot drift apart silently.
//
// Differences from JSON-RPC 2.0: no "jsonrpc" member, ids are strings,
// error codes are strings (see the Code* constants), and the engine never
// sends requests (only responses and notifications).
//
// This package has no dependencies and no behaviour beyond encoding: the
// supervisor (internal/engine) owns process handling.
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the protocol this app speaks, sent in engine.hello.
const Version = 2

// MaxLine bounds one message on the wire, in bytes (chunks stay far below).
const MaxLine = 4 << 20

// Message is one line on the wire. Exactly one shape applies:
//
//	request       id + method (+ params)        app → engine
//	notification  method (+ params), no id      either way
//	response      id + result | id + error      engine → app
type Message struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Kind is a message's shape.
type Kind int

const (
	KindInvalid Kind = iota
	KindRequest
	KindNotification
	KindResponse
)

func (k Kind) String() string {
	return [...]string{"invalid", "request", "notification", "response"}[k]
}

// Kind classifies m.
func (m *Message) Kind() Kind {
	switch {
	case m.Method != "" && m.Result == nil && m.Error == nil:
		if m.ID != "" {
			return KindRequest
		}
		return KindNotification
	case m.Method == "" && m.ID != "" && (m.Result == nil) != (m.Error == nil):
		return KindResponse
	}
	return KindInvalid
}

// Decode parses one line. It fails on malformed JSON and on messages that
// are none of the three shapes.
func Decode(line []byte) (Message, error) {
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return Message{}, fmt.Errorf("proto: %w", err)
	}
	if m.Kind() == KindInvalid {
		return Message{}, fmt.Errorf("proto: not a request, notification or response: %.200s", line)
	}
	return m, nil
}

// Encode returns m as one line, with its trailing newline.
func Encode(m Message) ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// NewRequest builds a request; params may be nil.
func NewRequest(id, method string, params any) (Message, error) {
	raw, err := marshalParams(params)
	return Message{ID: id, Method: method, Params: raw}, err
}

// NewNotification builds a notification; params may be nil.
func NewNotification(method string, params any) (Message, error) {
	raw, err := marshalParams(params)
	return Message{Method: method, Params: raw}, err
}

// NewResult builds a successful response; a nil result encodes as {}.
func NewResult(id string, result any) (Message, error) {
	if result == nil {
		return Message{ID: id, Result: json.RawMessage(`{}`)}, nil
	}
	raw, err := json.Marshal(result)
	return Message{ID: id, Result: raw}, err
}

// NewError builds an error response.
func NewError(id string, e *Error) Message { return Message{ID: id, Error: e} }

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

// DecodeParams decodes m.Params into v. Unknown fields are ignored, so a
// newer peer may add optional fields within protocol 2 (the fixture tests
// decode strictly). Missing params decode as the zero value.
func (m *Message) DecodeParams(v any) error { return decodeRaw(m.Params, v) }

// DecodeResult decodes m.Result into v.
func (m *Message) DecodeResult(v any) error { return decodeRaw(m.Result, v) }

func decodeRaw(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Error is a response's failure.
type Error struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Is lets errors.Is(err, &proto.Error{Code: proto.CodeBusy}) match by code.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Code == e.Code && t.Message == ""
}

// Errorf builds an Error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Error codes. The engine may only send these; the app treats an unknown
// code as CodeInternal. "Retryable" means the scheduler may run the job
// again on its own (after a restart for CodeOOM).
const (
	CodeBadRequest         = "bad_request"          // malformed params
	CodeMethodNotFound     = "method_not_found"     // unknown method (or not in this role)
	CodeNotInitialized     = "not_initialized"      // request before engine.hello
	CodeProtocolMismatch   = "protocol_mismatch"    // hello with a protocol the engine does not speak; data: {"supported":[2]}
	CodeUnsupported        = "unsupported"          // the model lacks the capability (e.g. cloning); data: {"capability":"clone"}
	CodeModelNotDownloaded = "model_not_downloaded" // offline and missing files; data: {"model":"…","missing":["…"]}
	CodeModelLoadFailed    = "model_load_failed"    // files present but unloadable (bad config, unknown model_type)
	CodeLicenceRequired    = "licence_required"     // reserved: the app gates licences before sending
	CodeOOM                = "oom"                  // out of memory: retryable after unloading or a restart
	CodeBusy               = "busy"                 // over the concurrency hello announced: retryable
	CodeCancelled          = "cancelled"            // the job was cancelled (job.cancel or shutdown)
	CodeInternal           = "internal"             // anything else; message carries the detail

	// Added in M1 (additive: an older app reads them as internal).
	CodeInputTooLong      = "input_too_long"     // text over the model's limit for one request: chunk it; data: {"limit":510,"actual":731,"unit":"phonemes"|"chars"|"tokens"}
	CodeFileNotFound      = "file_not_found"     // an input path does not exist; data: {"path":"…"}
	CodeUnsupportedMedia  = "unsupported_media"  // an input exists but cannot be decoded; data: {"path":"…"}
	CodePermissionDenied  = "permission_denied"  // macOS refused a permission; data: {"permission":"microphone"}
	CodeDeviceUnavailable = "device_unavailable" // the capture device is unknown, gone or busy; data: {"device":"…"}
	CodeContextTooLong    = "context_too_long"   // llm.generate: prompt + maxTokens over the context the engine allows; data: {"limit":16384,"actual":20000,"unit":"tokens"}

	// Added for the ActNow family (its spec §7.1): the job named no
	// snapshot directory and the cache, shared with other apps, holds
	// several of the model; data: {"model":"…","snapshots":["<commit>",…]}.
	// The app always names one (the executors' ModelPath).
	CodeModelPathRequired = "model_path_required"

	// Thread #153: a recording brought no sound, even with voice
	// processing (capture.start); data: NoInputData.
	CodeNoInput = "no_input"
)

// ParamAuxPaths is the member any model job may carry beside model and
// modelPath (path for models.load): the snapshot directories of the
// model's auxiliary repos, repo id → directory (Kokoro's G2P repos,
// Chatterbox's S3 tokenizer, Whisper's tokenizer). mlx-audio-swift finds
// them by repo id in its own folders of the cache (mlx-audio/…): the
// engine links those folders to these snapshots before the job, so that
// a snapshot of another commit (another ActNow app's) is never used; a
// directory that is not there fails the job with model_not_downloaded.
const ParamAuxPaths = "auxPaths"

// Codes lists every error code the engine may send.
var Codes = []string{
	CodeBadRequest, CodeMethodNotFound, CodeNotInitialized, CodeProtocolMismatch, CodeUnsupported,
	CodeModelNotDownloaded, CodeModelLoadFailed, CodeLicenceRequired, CodeOOM, CodeBusy, CodeCancelled, CodeInternal,
	CodeInputTooLong, CodeFileNotFound, CodeUnsupportedMedia, CodePermissionDenied, CodeDeviceUnavailable,
	CodeContextTooLong, CodeModelPathRequired, CodeNoInput,
}

// Retryable reports whether a job failing with code may be retried automatically.
func Retryable(code string) bool { return code == CodeOOM || code == CodeBusy }
