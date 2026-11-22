package voicevox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/voicevox"
	"github.com/hehehai/AudioSmith/internal/voicevox/voicevoxtest"
)

// manager connects to vv (VOICEVOX) and av (AivisSpeech); nil = an
// address nothing listens on.
func manager(t *testing.T, vv, av *voicevoxtest.Fake) *voicevox.Manager {
	t.Helper()
	urls := map[voicevox.Kind]string{voicevox.VOICEVOX: "http://127.0.0.1:1", voicevox.Aivis: "http://127.0.0.1:1"}
	if vv != nil {
		urls[voicevox.VOICEVOX] = vv.URL
	}
	if av != nil {
		urls[voicevox.Aivis] = av.URL
	}
	return voicevox.New(voicevox.Options{URLs: func() map[voicevox.Kind]string { return urls }, Dir: t.TempDir()})
}

func voice(k voicevox.Kind, uuid string, id int64) string {
	return voicevox.Voice{Kind: k, Speaker: uuid, Style: id}.String()
}

func TestVoiceIDs(t *testing.T) {
	v := voicevox.Voice{Kind: voicevox.Aivis, Speaker: voicevoxtest.AnneliUUID, Style: voicevoxtest.AnneliNormal}
	got, ok := voicevox.ParseVoice(v.String())
	if !ok || got != v {
		t.Fatalf("ParseVoice(%q) = %+v, %v", v.String(), got, ok)
	}
	for _, bad := range []string{"", "af_heart", "voicevox:", "voicevox:abc", "voicevox:/3", "other:abc/3", "voicevox:abc/x", "voicevox:a b/3"} {
		if voicevox.IsVoice(bad) {
			t.Errorf("IsVoice(%q) = true", bad)
		}
	}
}

func TestProbeBothFlavours(t *testing.T) {
	vv, av := voicevoxtest.New(voicevoxtest.VOICEVOX), voicevoxtest.New(voicevoxtest.Aivis)
	defer vv.Close()
	defer av.Close()
	m := manager(t, vv, av)
	if m.Online() {
		t.Fatal("online before any probe")
	}
	m.Refresh(context.Background(), 0)
	es := m.Engines()
	if len(es) != 2 || !es[0].Online || !es[1].Online {
		t.Fatalf("engines: %+v", es)
	}
	if es[0].Flavor != voicevox.VOICEVOX || es[0].Version != "0.26.2" || es[1].Flavor != voicevox.Aivis || es[1].Version != "1.2.0" {
		t.Fatalf("flavours: %+v / %+v", es[0], es[1])
	}
	// The singing style is left out.
	if es[0].Styles() != 3 {
		t.Fatalf("VOICEVOX styles = %d, want 3 (no humming)", es[0].Styles())
	}
	if got := m.Label(voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, voicevoxtest.ZundamonSweet)); got != "ずんだもん（あまあま）" {
		t.Fatalf("label = %q", got)
	}
	// A fresh probe is not repeated.
	n := vv.Requests("/speakers")
	m.Refresh(context.Background(), 1<<40)
	if vv.Requests("/speakers") != n {
		t.Fatal("Refresh probed again within its age")
	}
}

func TestOffline(t *testing.T) {
	m := manager(t, nil, nil)
	m.Refresh(context.Background(), 0)
	for _, e := range m.Engines() {
		if e.Online || e.Err != "not running" {
			t.Fatalf("engine %+v", e)
		}
	}
	dir := t.TempDir()
	_, err := m.Synthesize(context.Background(), proto.SynthesizeParams{Text: "こんにちは",
		Voice: voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, 3), Output: proto.Output{Path: filepath.Join(dir, "a.wav")}})
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != voicevox.CodeOffline || je.Retryable {
		t.Fatalf("err = %v", err)
	}
}

func TestTermsAndCredits(t *testing.T) {
	vv, av := voicevoxtest.New(voicevoxtest.VOICEVOX), voicevoxtest.New(voicevoxtest.Aivis)
	defer vv.Close()
	defer av.Close()
	m := manager(t, vv, av)
	ctx := context.Background()
	m.Refresh(ctx, 0)
	cases := []struct {
		voice   string
		use     voicevox.Use
		credit  string
		confirm bool
	}{
		{voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, 3), voicevox.UseCommercial, "VOICEVOX:ずんだもん", false},
		{voice(voicevox.VOICEVOX, voicevoxtest.No7UUID, 29), voicevox.UseNonCommercial, "VOICEVOX:No.7", true},
		{voice(voicevox.Aivis, voicevoxtest.AnneliUUID, voicevoxtest.AnneliNormal), voicevox.UseCommercial, "", false},
		{voice(voicevox.Aivis, voicevoxtest.NCUUID, voicevoxtest.NCNormal), voicevox.UseNonCommercial, "", true},
		{voice(voicevox.Aivis, voicevoxtest.CustomUUID, voicevoxtest.CustomNormal), voicevox.UseAsk, "AivisSpeech:独自さん", true},
	}
	for _, c := range cases {
		if got := m.Credit(ctx, c.voice); got != c.credit {
			t.Errorf("%s: credit %q, want %q", c.voice, got, c.credit)
		}
		tm, ok := m.Terms(c.voice)
		if !ok || tm.Use != c.use || tm.Confirm() != c.confirm {
			t.Errorf("%s: terms %+v", c.voice, tm)
		}
	}
	if m.Credit(ctx, "voicevox:nobody/1") != "" {
		t.Fatal("credits for an unknown voice")
	}
	// The special credits and notes of the table.
	if tm := voicevox.VoicevoxTerms("もち子さん"); tm.Credit != "VOICEVOX:もち子(cv 明日葉よもぎ)" || tm.Use != voicevox.UseIndividual || tm.Note == "" {
		t.Errorf("もち子さん: %+v", tm)
	}
	if tm := voicevox.VoicevoxTerms("知らない人"); tm.Use != voicevox.UseAsk || tm.Credit != "VOICEVOX:知らない人" {
		t.Errorf("unknown character: %+v", tm)
	}
	if tm := voicevox.VoicevoxTerms("ずんだもん"); tm.Note != voicevox.NoteZunko {
		t.Errorf("ずんだもん: %+v", tm)
	}
}

func TestPrefetchSamples(t *testing.T) {
	vv := voicevoxtest.New(voicevoxtest.VOICEVOX)
	defer vv.Close()
	m := manager(t, vv, nil)
	ctx := context.Background()
	m.Refresh(ctx, 0)
	m.Prefetch(ctx)
	v := voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, voicevoxtest.ZundamonSweet)
	p := m.Sample(v)
	if p == "" {
		t.Fatal("no sample")
	}
	if _, _, err := wav.ReadFile(p); err != nil {
		t.Fatal(err)
	}
	if m.Policy(v) == "" {
		t.Fatal("no policy")
	}
	n := vv.Requests("/speaker_info")
	m.Prefetch(ctx)
	if vv.Requests("/speaker_info") != n {
		t.Fatal("infos read twice")
	}
}

func TestExecutor(t *testing.T) {
	for _, fl := range []voicevoxtest.Flavor{voicevoxtest.VOICEVOX, voicevoxtest.Aivis} {
		t.Run(string(fl), func(t *testing.T) {
			f := voicevoxtest.New(fl)
			defer f.Close()
			var m *voicevox.Manager
			v := voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, voicevoxtest.ZundamonNormal)
			if fl == voicevoxtest.Aivis {
				m = manager(t, nil, f)
				v = voice(voicevox.Aivis, voicevoxtest.AnneliUUID, voicevoxtest.AnneliNormal)
			} else {
				m = manager(t, f, nil)
			}
			out := filepath.Join(t.TempDir(), "chunk.wav")
			speed := 1.25
			params, _ := json.Marshal(proto.SynthesizeParams{Model: "voicevox", Text: "今日はいい天気ですね。", Voice: v, Speed: &speed,
				Scales: &proto.Scales{Pitch: 0.05, Intonation: 1.4, Volume: 0.8, TempoDynamics: 1.2}, Output: proto.Output{Path: out}})
			raw, err := m.Executor().Run(context.Background(), jobs.Job{ID: "j1", Spec: jobs.Spec{Kind: proto.MethodTTSSynthesize, Params: params}}, func(jobs.Progress) {}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var res proto.SynthesizeResult
			json.Unmarshal(raw, &res)
			if res.SampleRate != f.Rate || res.Channels != 1 || res.Frames == 0 || res.AudioSeconds <= 0 || res.Output != out {
				t.Fatalf("result %+v", res)
			}
			if _, err := os.Stat(out); err != nil {
				t.Fatal(err)
			}
			q := f.Queries()[0]
			if q["speedScale"] != 1.25 || q["pitchScale"] != 0.05 || q["intonationScale"] != 1.4 || q["volumeScale"] != 0.8 {
				t.Fatalf("query %v", q)
			}
			if fl == voicevoxtest.Aivis && (q["tempoDynamicsScale"] != 1.2 || q["kana"] != "今日はいい天気ですね。") {
				t.Fatalf("AivisSpeech query %v", q)
			}
			if _, ok := q["tempoDynamicsScale"]; fl == voicevoxtest.VOICEVOX && ok {
				t.Fatal("tempoDynamicsScale sent to VOICEVOX")
			}
		})
	}
}

func TestUnknownStyle(t *testing.T) {
	vv := voicevoxtest.New(voicevoxtest.VOICEVOX)
	defer vv.Close()
	m := manager(t, vv, nil)
	_, err := m.Synthesize(context.Background(), proto.SynthesizeParams{Text: "あ",
		Voice: voice(voicevox.VOICEVOX, voicevoxtest.ZundamonUUID, 999), Output: proto.Output{Path: filepath.Join(t.TempDir(), "a.wav")}})
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != voicevox.CodeUnknownVoice {
		t.Fatalf("err = %v", err)
	}
	_, err = m.Synthesize(context.Background(), proto.SynthesizeParams{Text: "あ", Voice: "af_heart", Output: proto.Output{Path: "/tmp/x.wav"}})
	if !errors.As(err, &je) || je.Code != proto.CodeBadRequest {
		t.Fatalf("err = %v", err)
	}
}
