package main

// Helpers for the voice pages' tests over the fake engine
// (enginefake_test.go): answering their jobs, test clips, voices.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// params decodes a request's params.
func params[T any](t *testing.T, r sentReq) T {
	t.Helper()
	var p T
	if err := json.Unmarshal(r.Params, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// answerTranscribe answers the last stt.transcribe with text.
func answerTranscribe(t *testing.T, eng *fakeEngine, text string) proto.TranscribeParams {
	t.Helper()
	r := eng.lastOf(t, proto.MethodSTTTranscribe)
	p := params[proto.TranscribeParams](t, r)
	eng.succeed(r.ID, proto.TranscribeResult{Model: p.Model, Text: text, Language: "en"})
	return p
}

// answerEnhance answers the last enhance.run, writing its output.
func answerEnhance(t *testing.T, eng *fakeEngine) proto.EnhanceParams {
	t.Helper()
	r := eng.lastOf(t, proto.MethodEnhanceRun)
	p := params[proto.EnhanceParams](t, r)
	data, err := os.ReadFile(p.Audio)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p.Output.Path, data, 0o644)
	eng.succeed(r.ID, proto.EnhanceResult{AudioOut: proto.AudioOut{Output: p.Output.Path, SampleRate: refRate, Channels: 1}})
	return p
}

// writeSpeech writes a mono 16-bit WAV of seconds with a voiced tone in
// the speech spans and near silence elsewhere.
func writeSpeech(path string, seconds float64, speech []span) error {
	const rate = 16000
	n := int(seconds * rate)
	data := make([]byte, 44+2*n)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*n))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], rate)
	binary.LittleEndian.PutUint32(data[28:], rate*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*n))
	for i := range n {
		tm := float64(i) / rate
		amp := 30.0 // a faint hiss
		for _, s := range speech {
			if tm >= s.Start && tm < s.End {
				amp = 12000
			}
		}
		v := amp * math.Sin(2*math.Pi*180*tm) * (0.7 + 0.3*math.Sin(2*math.Pi*3*tm))
		if amp < 100 {
			v = amp * math.Sin(float64(i)*1.7)
		}
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(v)))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// newVoiceTestApp is newTestApp, for the voice pages.
func newVoiceTestApp(t *testing.T) (*app, *fakeEngine) {
	t.Helper()
	a, eng, _ := newTestApp(t)
	return a, eng
}

// addVoice saves a voice through the service, with a reference clip of
// refSeconds for clones.
func addVoice(t *testing.T, a *app, v core.Voice, refSeconds float64) core.Voice {
	t.Helper()
	var f core.VoiceFiles
	if refSeconds > 0 {
		f.Reference = filepath.Join(t.TempDir(), "ref.wav")
		if err := writeSpeech(f.Reference, refSeconds, []span{{0.2, refSeconds - 0.2}}); err != nil {
			t.Fatal(err)
		}
		v.RefSeconds = refSeconds
	}
	saved, err := a.lib.svc.Voices.SaveFiles(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// acceptLicences accepts every licence (OmniVoice is gated).
func acceptLicences(t *testing.T, a *app) {
	t.Helper()
	for _, e := range voiceCatalog().Entries {
		if e.Licence.RequiresAcceptance {
			if err := a.lib.licences.Accept(context.Background(), core.LicenceAcceptance{Licence: e.Licence.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.loadAcceptances()
}
