package proto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fixturesDir holds the golden transcripts shared with the Swift tests.
const fixturesDir = "../../../engine/Fixtures"

// fixtureLine is one line of a fixture: a wire message and its direction.
type fixtureLine struct {
	To  string          `json:"to"` // "engine" (app → engine) or "app"
	Msg json.RawMessage `json:"msg"`
	// Line is a raw non-v2 line (legacy_v1.jsonl only).
	Line json.RawMessage `json:"line"`
}

func readFixture(t *testing.T, path string) []fixtureLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []fixtureLine
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, MaxLine)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l fixtureLine
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			t.Fatalf("%s:%d: %v", filepath.Base(path), n, err)
		}
		if l.To != "engine" && l.To != "app" {
			t.Fatalf("%s:%d: to %q", filepath.Base(path), n, l.To)
		}
		lines = append(lines, l)
	}
	return lines
}

// strict decodes raw into v rejecting unknown fields, then checks that v
// encodes back to the same JSON: the Go types carry every field the
// fixture has, with the same omitempty behaviour.
func strict(t *testing.T, where string, raw json.RawMessage, v any) {
	t.Helper()
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v\n%s", where, err, raw)
	}
	again, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(raw, again) {
		t.Fatalf("%s: does not round-trip\nwire: %s\ngo:   %s", where, raw, again)
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

var knownCodes = Codes

// TestFixtures decodes every golden transcript with the Go types and
// checks the protocol's sequencing rules on it (docs/protocol-v2.md §4).
func TestFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(fixturesDir, "*.jsonl"))
	if err != nil || len(paths) < 12 {
		t.Fatalf("fixtures: %v %v", paths, err)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		if name == "legacy_v1.jsonl" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			pending := map[string]string{} // request id → method
			done := map[string]bool{}      // jobs that answered
			nextSeq := map[string]int{}
			for i, l := range readFixture(t, path) {
				where := fmt.Sprintf("%s:%d", name, i+1)
				m, err := Decode(l.Msg)
				if err != nil {
					t.Fatalf("%s: %v", where, err)
				}
				// The whole envelope round-trips too.
				enc, err := Encode(m)
				if err != nil || !jsonEqual(l.Msg, bytes.TrimSuffix(enc, []byte("\n"))) {
					t.Fatalf("%s: envelope does not round-trip: %s", where, enc)
				}
				switch k := m.Kind(); {
				case l.To == "engine" && k == KindRequest:
					if _, dup := pending[m.ID]; dup {
						t.Fatalf("%s: id %s reused while pending", where, m.ID)
					}
					pending[m.ID] = m.Method
					if p := ParamsFor(m.Method); p != nil {
						strict(t, where+" params", m.Params, p)
					} else if !slices.Contains(Reserved, m.Method) {
						t.Fatalf("%s: unknown method %s", where, m.Method)
					}
				case l.To == "app" && k == KindNotification:
					p := ParamsFor(m.Method)
					if p == nil {
						t.Fatalf("%s: unknown notification %s", where, m.Method)
					}
					strict(t, where+" params", m.Params, p)
					job := ""
					switch v := p.(type) {
					case *ProgressParams:
						job = v.Job
						if v.Fraction != nil && (*v.Fraction < 0 || *v.Fraction > 1) {
							t.Fatalf("%s: fraction %v", where, *v.Fraction)
						}
					case *ChunkParams:
						job = v.Job
						if v.Seq != nextSeq[job] || v.Encoding != "pcm16le" || len(v.Data)%(2*v.Channels) != 0 {
							t.Fatalf("%s: chunk %+v (want seq %d)", where, v, nextSeq[job])
						}
						nextSeq[job]++
					case *LLMTokenParams:
						job = v.Job
						if pending[job] != MethodLLMGenerate || v.Text == "" {
							t.Fatalf("%s: token %+v of %s", where, v, pending[job])
						}
					case *LevelParams:
						job = v.Job
						if pending[job] != MethodCaptureStart || v.RMS < -120 || v.Peak < v.RMS || v.Peak > 0 {
							t.Fatalf("%s: level %+v of %s", where, v, pending[job])
						}
					}
					// (log lines may name any job, running or not)
					if job != "" {
						if !IsJob(pending[job]) && !done[job] {
							t.Fatalf("%s: notification for %s, which is not a running job", where, job)
						}
						if done[job] {
							t.Fatalf("%s: notification for %s after its response", where, job)
						}
					}
				case l.To == "app" && k == KindResponse:
					method, ok := pending[m.ID]
					if !ok {
						t.Fatalf("%s: response to unknown id %s", where, m.ID)
					}
					delete(pending, m.ID)
					done[m.ID] = true
					if m.Error != nil {
						if !slices.Contains(knownCodes, m.Error.Code) || m.Error.Message == "" {
							t.Fatalf("%s: error %+v", where, m.Error)
						}
						if ParamsFor(method) == nil && m.Error.Code != CodeMethodNotFound {
							t.Fatalf("%s: %s must answer method_not_found", where, method)
						}
						if m.Error.Code == CodeCancelled && !IsJob(method) {
							t.Fatalf("%s: cancelled error for control request %s", where, method)
						}
						break
					}
					r := ResultFor(method)
					if r == nil {
						t.Fatalf("%s: result for %s, which has none", where, method)
					}
					strict(t, where+" result", m.Result, r)
				default:
					t.Fatalf("%s: a %s cannot go to the %s", where, k, l.To)
				}
			}
			if len(pending) > 0 {
				t.Fatalf("unanswered requests: %v", pending)
			}
		})
	}
}

func TestLegacyFixture(t *testing.T) {
	lines := readFixture(t, filepath.Join(fixturesDir, "legacy_v1.jsonl"))
	for _, l := range lines {
		if _, err := Decode(l.Line); err == nil {
			t.Errorf("a v1 line decoded as v2: %s", l.Line)
		}
		if v, ok := LegacyReady(l.Line); !ok || v != 1 {
			t.Errorf("LegacyReady(%s) = %d, %v", l.Line, v, ok)
		}
	}
	if _, ok := LegacyReady([]byte(`{"id":"c1","result":{}}`)); ok {
		t.Error("a v2 response looks legacy")
	}
}

func TestEncodeDecode(t *testing.T) {
	speed := 1.25
	req, err := NewRequest("j1", MethodTTSSynthesize, SynthesizeParams{
		Model: "m", Text: "Hi \"there\"\nnew line", Speed: &speed, Output: Output{Path: "/tmp/x.wav"},
	})
	if err != nil {
		t.Fatal(err)
	}
	line, err := Encode(req)
	if err != nil || bytes.Count(line, []byte("\n")) != 1 || !bytes.HasSuffix(line, []byte("\n")) {
		t.Fatalf("one message must be one line: %q %v", line, err)
	}
	m, err := Decode(line)
	if err != nil || m.Kind() != KindRequest {
		t.Fatalf("%+v %v", m, err)
	}
	var p SynthesizeParams
	if err := m.DecodeParams(&p); err != nil || *p.Speed != 1.25 || p.Text != "Hi \"there\"\nnew line" {
		t.Fatalf("%+v %v", p, err)
	}

	res, _ := NewResult("c1", nil)
	if res.Kind() != KindResponse || string(res.Result) != "{}" {
		t.Errorf("empty result %+v", res)
	}
	n, _ := NewNotification(MethodLog, LogParams{Level: "info", Message: "x"})
	if n.Kind() != KindNotification {
		t.Errorf("notification kind %v", n.Kind())
	}

	for _, bad := range []string{`{}`, `{"id":"1"}`, `{"id":"1","result":{},"error":{"code":"x","message":"y"}}`, `{"method":"x","result":{}}`, `not json`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("Decode(%s) succeeded", bad)
		}
	}
}

func TestErrors(t *testing.T) {
	var err error = NewError("j1", Errorf(CodeBusy, "busy with %s", "j0")).Error
	if !errors.Is(err, &Error{Code: CodeBusy}) || errors.Is(err, &Error{Code: CodeOOM}) {
		t.Error("errors.Is by code")
	}
	if !strings.Contains(err.Error(), "busy with j0") {
		t.Error(err)
	}
	if !Retryable(CodeOOM) || Retryable(CodeCancelled) {
		t.Error("Retryable")
	}
	// Every job method has params and result types.
	for _, m := range JobMethods {
		if !IsJob(m) || ParamsFor(m) == nil || ResultFor(m) == nil {
			t.Errorf("%s", m)
		}
	}
	for _, m := range []string{MethodMediaProbe, MethodCaptureDevices, MethodCaptureStop, MethodStats} {
		if IsJob(m) || ParamsFor(m) == nil || ResultFor(m) == nil {
			t.Errorf("control %s", m)
		}
	}
	for _, m := range Reserved {
		if ParamsFor(m) != nil || IsJob(m) {
			t.Errorf("reserved %s is implemented", m)
		}
	}
	if RoleOf(MethodMediaDecode) != RoleMedia || RoleOf(MethodCaptureStart) != RoleListen || RoleOf(MethodSTTTranscribe) != RoleGPU {
		t.Error("RoleOf")
	}
}

// TestFixturesCoverMethods: every method has a fixture showing it.
func TestFixturesCoverMethods(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(fixturesDir, "*.jsonl"))
	seen := map[string]bool{}
	for _, path := range paths {
		if filepath.Base(path) == "legacy_v1.jsonl" {
			continue
		}
		for _, l := range readFixture(t, path) {
			var m Message
			json.Unmarshal(l.Msg, &m)
			seen[m.Method] = true
		}
	}
	all := append(slices.Clone(JobMethods), MethodHello, MethodStats, MethodFlush, MethodShutdown, MethodModelsUnload, MethodModelsLoaded,
		MethodJobCancel, MethodJobProgress, MethodTTSChunk, MethodLog, MethodMediaProbe, MethodCaptureDevices, MethodCaptureStop, MethodCaptureLevel, MethodLLMToken, MethodTTSTurn, MethodEngineMemory)
	for _, m := range all {
		if !seen[m] {
			t.Errorf("no fixture shows %s", m)
		}
	}
}
