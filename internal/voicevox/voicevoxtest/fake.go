// Package voicevoxtest is a fake VOICEVOX-compatible engine for tests: an
// httptest server answering /version, /engine_manifest, /speakers,
// /speaker_info, /audio_query and /synthesis like VOICEVOX ENGINE or
// AivisSpeech Engine, with a few characters and a tone for speech.
package voicevoxtest

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"unicode/utf8"
)

// Flavor is which engine the fake plays.
type Flavor string

const (
	VOICEVOX Flavor = "voicevox"
	Aivis    Flavor = "aivis"
)

// Character UUIDs and style ids of the fake.
const (
	ZundamonUUID = "388f246b-8c41-4ac1-8e2d-5d79f3ff56d9"
	No7UUID      = "0156da66-4300-474a-a398-49eb2e8dd853"
	AnneliUUID   = "e756b8e4-b606-4e15-99b1-3f9c6a1b2317" // AivisSpeech: ACML
	NCUUID       = "a59cb814-0083-4369-8542-f51a29e72af7" // AivisSpeech: ACML-NC
	CustomUUID   = "d1f4a7c2-0000-4000-8000-000000000001" // AivisSpeech: own licence

	ZundamonNormal = 3
	ZundamonSweet  = 1
	No7Normal      = 29
	AnneliNormal   = 888753760
	NCNormal       = 1431611904
	CustomNormal   = 606865152
)

// Fake is a running fake engine.
type Fake struct {
	*httptest.Server
	Flavor Flavor
	Rate   int // of the synthesized audio

	mu       sync.Mutex
	queries  []map[string]any // the queries /synthesis got
	requests map[string]int   // by path
}

// New starts a fake engine of flavor; Close it.
func New(f Flavor) *Fake {
	fk := &Fake{Flavor: f, Rate: 24000, requests: map[string]int{}}
	if f == Aivis {
		fk.Rate = 44100
	}
	fk.Server = httptest.NewServer(http.HandlerFunc(fk.serve))
	return fk
}

// Queries are the AudioQueries /synthesis received, oldest first.
func (f *Fake) Queries() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.queries...)
}

// Requests counts the requests to path.
func (f *Fake) Requests(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

type style struct {
	Name string `json:"name"`
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type speaker struct {
	Name   string  `json:"name"`
	UUID   string  `json:"speaker_uuid"`
	Styles []style `json:"styles"`
}

func (f *Fake) speakers() []speaker {
	if f.Flavor == Aivis {
		return []speaker{
			{Name: "まい", UUID: AnneliUUID, Styles: []style{{"ノーマル", AnneliNormal, "talk"}}},
			{Name: "非商用ちゃん", UUID: NCUUID, Styles: []style{{"ノーマル", NCNormal, "talk"}}},
			{Name: "独自さん", UUID: CustomUUID, Styles: []style{{"ノーマル", CustomNormal, "talk"}}},
		}
	}
	return []speaker{
		{Name: "ずんだもん", UUID: ZundamonUUID, Styles: []style{{"ノーマル", ZundamonNormal, "talk"}, {"あまあま", ZundamonSweet, "talk"}, {"ハミング", 3001, "frame_decode"}}},
		{Name: "No.7", UUID: No7UUID, Styles: []style{{"ノーマル", No7Normal, "talk"}}},
	}
}

func (f *Fake) policy(uuid string) string {
	switch uuid {
	case AnneliUUID:
		return "# Aivis Common Model License (ACML) 1.0\n\nこの音声合成モデルは…"
	case NCUUID:
		return "# Aivis Common Model License (ACML) - Non Commercial 1.0\n\n…"
	case CustomUUID:
		return "このモデルは作者の許可なく利用できません。"
	}
	return "利用規約: クレジット表記「VOICEVOX:キャラ名」が必要です。"
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests[r.URL.Path]++
	f.mu.Unlock()
	js := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/version":
		if f.Flavor == Aivis {
			js("1.2.0")
		} else {
			js("0.26.2")
		}
	case "/engine_manifest":
		if f.Flavor == Aivis {
			js(map[string]string{"name": "AivisSpeech Engine", "brand_name": "AivisSpeech", "version": "1.2.0"})
		} else {
			js(map[string]string{"name": "VOICEVOX Engine", "brand_name": "VOICEVOX", "version": "0.26.2"})
		}
	case "/speakers":
		js(f.speakers())
	case "/speaker_info":
		uuid := r.URL.Query().Get("speaker_uuid")
		for _, s := range f.speakers() {
			if s.UUID != uuid {
				continue
			}
			var infos []map[string]any
			for _, st := range s.Styles {
				infos = append(infos, map[string]any{"id": st.ID, "icon": "",
					"voice_samples": []string{base64.StdEncoding.EncodeToString(Tone(f.Rate, 0.3))}})
			}
			js(map[string]any{"policy": f.policy(uuid), "portrait": "", "style_infos": infos})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		js(map[string]string{"detail": "話者が見つかりません"})
	case "/audio_query":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		id, _ := strconv.ParseInt(r.URL.Query().Get("speaker"), 10, 64)
		if !f.known(id) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			js(map[string]string{"detail": "該当する話者(speaker)が見つかりません"})
			return
		}
		q := map[string]any{"accent_phrases": []any{}, "speedScale": 1.0, "pitchScale": 0.0, "intonationScale": 1.0,
			"volumeScale": 1.0, "prePhonemeLength": 0.1, "postPhonemeLength": 0.1, "outputSamplingRate": f.Rate,
			"outputStereo": false, "kana": "", "text": r.URL.Query().Get("text")}
		if f.Flavor == Aivis {
			q["tempoDynamicsScale"] = 1.0
		}
		js(q)
	case "/synthesis":
		id, _ := strconv.ParseInt(r.URL.Query().Get("speaker"), 10, 64)
		var q map[string]any
		if json.NewDecoder(r.Body).Decode(&q) != nil || !f.known(id) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			js(map[string]string{"detail": "bad query"})
			return
		}
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		text, _ := q["text"].(string)
		speed, _ := q["speedScale"].(float64)
		if speed <= 0 {
			speed = 1
		}
		secs := 0.12 * float64(max(utf8.RuneCountInString(text), 1)) / speed
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(Tone(f.Rate, secs))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *Fake) known(id int64) bool {
	for _, s := range f.speakers() {
		for _, st := range s.Styles {
			if st.ID == id {
				return true
			}
		}
	}
	return false
}

// Tone is a 16-bit mono WAV of seconds of a 220 Hz tone at rate.
func Tone(rate int, seconds float64) []byte {
	n := int(float64(rate) * seconds)
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + 2*n))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(2 * n))
	for i := range n {
		w(int16(8000 * math.Sin(2*math.Pi*220*float64(i)/float64(rate))))
	}
	return b.Bytes()
}
