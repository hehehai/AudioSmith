// Package voicevox connects AudioSmith to a VOICEVOX-compatible engine the
// user installed and runs: VOICEVOX ENGINE (the VOICEVOX app's engine,
// http://127.0.0.1:50021) or AivisSpeech Engine (http://127.0.0.1:10101),
// thread #171. AudioSmith bundles and redistributes nothing of theirs: it
// speaks their HTTP API (/version, /engine_manifest, /speakers,
// /speaker_info, /audio_query, /synthesis) like any other client.
//
// The package has the HTTP client (client.go), the engines and voice ids
// (engines.go), the characters' terms and the credits their audio must
// carry (terms.go), the manager that probes the engines and caches their
// characters (manager.go) and the tts.synthesize executor the render
// pipeline calls instead of the Swift engine's (executor.go).
//
// Imports: engine/proto (the synthesize params and result), jobs (the
// executor), dsp/wav (the synthesized file's format).
package voicevox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one engine at Base ("http://127.0.0.1:50021").
type Client struct {
	Base string
	// HTTP is the client to use; nil = a client with no proxy (the
	// engine is on this Mac: a system proxy must not see the requests).
	HTTP *http.Client
}

// localHTTP is the default client: no proxy; idle connections are let
// go before the engines' server (uvicorn) closes them after 5 s, so a
// request never goes out on a connection being closed (seen on the Mac,
// thread #171).
var localHTTP = &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, IdleConnTimeout: 2 * time.Second}}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return localHTTP
}

// Manifest is the part of /engine_manifest the app reads.
type Manifest struct {
	Name      string `json:"name"`       // "VOICEVOX Engine", "AivisSpeech Engine"
	BrandName string `json:"brand_name"` // "VOICEVOX", "AivisSpeech"
	Version   string `json:"version"`
	URL       string `json:"url"`
}

// Speaker is a character of /speakers with its styles.
type Speaker struct {
	Name    string  `json:"name"`
	UUID    string  `json:"speaker_uuid"`
	Version string  `json:"version,omitempty"`
	Styles  []Style `json:"styles"`
}

// Style is one of a character's voices ("ノーマル", "あまあま"…). ID is
// the engine's style id (AivisSpeech's are 32-bit hashes).
type Style struct {
	Name string `json:"name"`
	ID   int64  `json:"id"`
	// Type is "talk" (or "" from old engines) for speech; the singing
	// types ("sing", "frame_decode"…) are not offered.
	Type string `json:"type,omitempty"`
}

// Talks reports whether the style speaks (rather than sings).
func (s Style) Talks() bool { return s.Type == "" || s.Type == "talk" }

// SpeakerInfo is /speaker_info: the character's terms (VOICEVOX) or its
// model's licence (AivisSpeech), as Markdown or plain text, and each
// style's voice samples (base64 WAV).
type SpeakerInfo struct {
	Policy     string      `json:"policy"`
	StyleInfos []StyleInfo `json:"style_infos"`
}

// StyleInfo is a style's part of SpeakerInfo.
type StyleInfo struct {
	ID           int64    `json:"id"`
	VoiceSamples []string `json:"voice_samples"`
}

// HTTPError is an engine's answer other than 2xx.
type HTTPError struct {
	Status int
	Detail string
}

func (e *HTTPError) Error() string {
	if e.Detail == "" {
		return "HTTP " + strconv.Itoa(e.Status)
	}
	return "HTTP " + strconv.Itoa(e.Status) + ": " + e.Detail
}

// ErrOffline wraps the errors of an engine that does not answer
// (refused, timed out, not an engine).
var ErrOffline = errors.New("engine not running")

func (c Client) do(ctx context.Context, method, path string, q url.Values, body any, out any) ([]byte, error) {
	u := strings.TrimRight(c.Base, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	send := func() (*http.Response, error) {
		var rd io.Reader
		if raw != nil {
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, rd)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return c.http().Do(req)
	}
	resp, err := send()
	if err != nil && ctx.Err() == nil {
		// Once more on a fresh connection: the engines' requests are
		// safe to repeat, and a kept-alive connection may have just been
		// closed by the server.
		resp, err = send()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Status: resp.StatusCode, Detail: detail(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return data, nil
}

// detail is the message of a FastAPI error body ({"detail": "…"} or a
// validation list), cut short.
func detail(b []byte) string {
	var e struct {
		Detail json.RawMessage `json:"detail"`
	}
	s := strings.TrimSpace(string(b))
	if json.Unmarshal(b, &e) == nil && len(e.Detail) > 0 {
		var str string
		if json.Unmarshal(e.Detail, &str) == nil {
			s = str
		} else {
			s = string(e.Detail)
		}
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

// Version is /version ("0.26.2").
func (c Client) Version(ctx context.Context) (string, error) {
	var v string
	_, err := c.do(ctx, http.MethodGet, "/version", nil, nil, &v)
	return v, err
}

// Manifest is /engine_manifest.
func (c Client) Manifest(ctx context.Context) (Manifest, error) {
	var m Manifest
	_, err := c.do(ctx, http.MethodGet, "/engine_manifest", nil, nil, &m)
	return m, err
}

// Speakers is /speakers.
func (c Client) Speakers(ctx context.Context) ([]Speaker, error) {
	var s []Speaker
	_, err := c.do(ctx, http.MethodGet, "/speakers", nil, nil, &s)
	return s, err
}

// SpeakerInfo is /speaker_info of the character with uuid, its samples
// inline (base64).
func (c Client) SpeakerInfo(ctx context.Context, uuid string) (SpeakerInfo, error) {
	var s SpeakerInfo
	_, err := c.do(ctx, http.MethodGet, "/speaker_info", url.Values{"speaker_uuid": {uuid}, "resource_format": {"base64"}}, nil, &s)
	return s, err
}

// AudioQuery is /audio_query: the engine's reading of text for style,
// kept as JSON fields so that the fields this package does not know
// (AivisSpeech's tempoDynamicsScale, newer engines') go back untouched.
func (c Client) AudioQuery(ctx context.Context, text string, style int64) (map[string]any, error) {
	var q map[string]any
	_, err := c.do(ctx, http.MethodPost, "/audio_query", url.Values{"text": {text}, "speaker": {strconv.FormatInt(style, 10)}}, nil, &q)
	return q, err
}

// Synthesis is /synthesis of query for style: a WAV file.
func (c Client) Synthesis(ctx context.Context, query map[string]any, style int64) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/synthesis", url.Values{"speaker": {strconv.FormatInt(style, 10)}}, query, nil)
}
