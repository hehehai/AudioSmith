package voicevox

// The manager: which engines answer, their characters, and the terms and
// samples of each, cached so that views never wait on the network.

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Engine is what the manager last learned of one engine.
type Engine struct {
	Kind Kind   // the slot: which address setting it is
	URL  string // where it was asked
	// Online: it answered; then Flavor is what it says it is (VOICEVOX at
	// AivisSpeech's address is still VOICEVOX), Name and Version its
	// manifest's, Speakers its characters (those that speak).
	Online   bool
	Flavor   Kind
	Name     string
	Version  string
	Speakers []Speaker
	Err      string // why not, when it did not
	Checked  time.Time
}

// Styles counts the engine's speaking styles.
func (e Engine) Styles() int {
	n := 0
	for _, s := range e.Speakers {
		n += len(s.Styles)
	}
	return n
}

// Options configure a Manager.
type Options struct {
	// URLs are the engines' addresses (missing or "" = DefaultURL),
	// asked at every probe so that a change in Settings applies at once.
	URLs func() map[Kind]string
	// Dir holds the voice samples written from /speaker_info ("" = none
	// are kept, previews are not offered).
	Dir string
	// HTTP is the client (nil = Client's default).
	HTTP *http.Client
	// Changed is called after a probe or a character's info changed what
	// the manager knows (on the goroutine that learned it).
	Changed func()
}

// Manager probes the engines and keeps what they said.
type Manager struct {
	o       Options
	mu      sync.Mutex
	engines map[Kind]Engine
	infos   map[string]charInfo // by kind + ":" + uuid
	probing map[Kind]bool
	busy    map[string]bool // infos being fetched
}

// charInfo is the part of a character's /speaker_info the manager keeps.
type charInfo struct {
	policy  string
	samples map[int64]string // style id → WAV file in Dir
}

// New makes a manager.
func New(o Options) *Manager {
	return &Manager{o: o, engines: map[Kind]Engine{}, infos: map[string]charInfo{}, probing: map[Kind]bool{}, busy: map[string]bool{}}
}

// URL is kind's address now.
func (m *Manager) URL(k Kind) string {
	if m.o.URLs != nil {
		if u := m.o.URLs()[k]; u != "" {
			return u
		}
	}
	return DefaultURL(k)
}

func (m *Manager) client(k Kind) Client {
	return Client{Base: m.URL(k), HTTP: m.o.HTTP}
}

func (m *Manager) changed() {
	if m.o.Changed != nil {
		m.o.Changed()
	}
}

// probeTimeout bounds one probe of an engine.
const probeTimeout = 4 * time.Second

// Probe asks engine kind what it is and which characters it has.
func (m *Manager) Probe(ctx context.Context, k Kind) Engine {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	c := m.client(k)
	e := Engine{Kind: k, URL: c.Base, Flavor: k, Checked: time.Now()}
	man, err := c.Manifest(ctx)
	if err == nil {
		e.Flavor, e.Name = KindOf(man, k), man.Name
		e.Version, err = c.Version(ctx)
	}
	var sp []Speaker
	if err == nil {
		sp, err = c.Speakers(ctx)
	}
	if err != nil {
		e.Err = err.Error()
		if errors.Is(err, ErrOffline) {
			e.Err = "not running"
		}
	} else {
		e.Online = true
		for _, s := range sp {
			s.Styles = slices.DeleteFunc(s.Styles, func(st Style) bool { return !st.Talks() })
			if len(s.Styles) > 0 {
				e.Speakers = append(e.Speakers, s)
			}
		}
	}
	m.mu.Lock()
	old := m.engines[k]
	m.engines[k] = e
	if old.URL != e.URL || old.Version != e.Version {
		// Another engine or version: its characters' infos may differ.
		for key := range m.infos {
			if len(key) > len(k) && key[:len(k)+1] == string(k)+":" {
				delete(m.infos, key)
			}
		}
	}
	m.mu.Unlock()
	m.changed()
	return e
}

// Refresh probes every engine whose last probe is older than age (0:
// all), at most one probe of an engine at a time.
func (m *Manager) Refresh(ctx context.Context, age time.Duration) {
	var wg sync.WaitGroup
	for _, k := range Kinds {
		m.mu.Lock()
		e, seen := m.engines[k]
		stale := !seen || age <= 0 || time.Since(e.Checked) > age || e.URL != m.URL(k)
		if !stale || m.probing[k] {
			m.mu.Unlock()
			continue
		}
		m.probing[k] = true
		m.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Probe(ctx, k)
			m.mu.Lock()
			m.probing[k] = false
			m.mu.Unlock()
		}()
	}
	wg.Wait()
}

// Engines are what the last probes found, in Kinds order (an engine
// never probed is offline with no error).
func (m *Manager) Engines() []Engine {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Engine, 0, len(Kinds))
	for _, k := range Kinds {
		e, ok := m.engines[k]
		if !ok {
			e = Engine{Kind: k, URL: m.URL(k), Flavor: k}
		}
		out = append(out, e)
	}
	return out
}

// Online reports whether an engine answered at its last probe.
func (m *Manager) Online() bool {
	for _, e := range m.Engines() {
		if e.Online {
			return true
		}
	}
	return false
}

// Character is a voice's character and style, as the last probe found.
type Character struct {
	Engine  Engine
	Speaker Speaker
	Style   Style
}

// Lookup finds voice in what the engines said.
func (m *Manager) Lookup(voice string) (Character, bool) {
	v, ok := ParseVoice(voice)
	if !ok {
		return Character{}, false
	}
	m.mu.Lock()
	e, seen := m.engines[v.Kind]
	m.mu.Unlock()
	if !seen {
		return Character{Engine: Engine{Kind: v.Kind, URL: m.URL(v.Kind), Flavor: v.Kind}}, false
	}
	for _, s := range e.Speakers {
		if s.UUID != v.Speaker {
			continue
		}
		for _, st := range s.Styles {
			if st.ID == v.Style {
				return Character{Engine: e, Speaker: s, Style: st}, true
			}
		}
	}
	return Character{Engine: e}, false
}

// Label is a voice's name: "ずんだもん（ノーマル）", or "" when unknown.
func (m *Manager) Label(voice string) string {
	ch, ok := m.Lookup(voice)
	if !ok {
		return ""
	}
	return ch.Speaker.Name + "（" + ch.Style.Name + "）"
}

func infoKey(k Kind, uuid string) string { return string(k) + ":" + uuid }

// Terms are the terms of voice's character from what is known now (no
// network): an AivisSpeech model whose licence was not read yet is
// UseAsk until Info reads it. ok is false for an unknown voice.
func (m *Manager) Terms(voice string) (Terms, bool) {
	ch, ok := m.Lookup(voice)
	if !ok {
		return Terms{}, false
	}
	m.mu.Lock()
	info := m.infos[infoKey(ch.Engine.Kind, ch.Speaker.UUID)]
	m.mu.Unlock()
	return TermsOf(ch.Engine.Flavor, ch.Speaker.Name, info.policy), true
}

// Policy is the character's terms or model licence the engine gave, as
// it gave it ("" until Info read it).
func (m *Manager) Policy(voice string) string {
	v, ok := ParseVoice(voice)
	if !ok {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infos[infoKey(v.Kind, v.Speaker)].policy
}

// Sample is the WAV file of a style's first voice sample, or "" until
// Info wrote it.
func (m *Manager) Sample(voice string) string {
	v, ok := ParseVoice(voice)
	if !ok {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infos[infoKey(v.Kind, v.Speaker)].samples[v.Style]
}

// HasInfo reports whether the character of voice's info was read.
func (m *Manager) HasInfo(voice string) bool {
	v, ok := ParseVoice(voice)
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok = m.infos[infoKey(v.Kind, v.Speaker)]
	return ok
}

// Info reads the /speaker_info of the character with uuid on engine
// kind, once: its policy, and its styles' first samples written to Dir.
func (m *Manager) Info(ctx context.Context, k Kind, uuid string) error {
	key := infoKey(k, uuid)
	m.mu.Lock()
	_, have := m.infos[key]
	if have || m.busy[key] {
		m.mu.Unlock()
		return nil
	}
	m.busy[key] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, key)
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	si, err := m.client(k).SpeakerInfo(ctx, uuid)
	if err != nil {
		return err
	}
	ci := charInfo{policy: si.Policy, samples: map[int64]string{}}
	if m.o.Dir != "" {
		dir := filepath.Join(m.o.Dir, string(k), safeName(uuid))
		if err := os.MkdirAll(dir, 0o755); err == nil {
			for _, s := range si.StyleInfos {
				if len(s.VoiceSamples) == 0 {
					continue
				}
				b, err := base64.StdEncoding.DecodeString(s.VoiceSamples[0])
				if err != nil || len(b) < 44 {
					continue
				}
				p := filepath.Join(dir, strconv.FormatInt(s.ID, 10)+".wav")
				if os.WriteFile(p, b, 0o644) == nil {
					ci.samples[s.ID] = p
				}
			}
		}
	}
	m.mu.Lock()
	m.infos[key] = ci
	m.mu.Unlock()
	m.changed()
	return nil
}

// Prefetch reads the info of every character of the online engines that
// was not read yet, one at a time (the picker's previews and marks).
func (m *Manager) Prefetch(ctx context.Context) {
	for _, e := range m.Engines() {
		if !e.Online {
			continue
		}
		for _, s := range e.Speakers {
			if ctx.Err() != nil {
				return
			}
			m.Info(ctx, e.Kind, s.UUID)
		}
	}
}

// safeName keeps a uuid usable as a file name.
func safeName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

// Credit is the credit a take spoken by voice carries: the character's
// (VOICEVOX), or an AivisSpeech model's when its licence asks for one.
// It reads the character's info if needed (an AivisSpeech model's
// licence); "" for no credit or an unknown voice.
func (m *Manager) Credit(ctx context.Context, voice string) string {
	ch, ok := m.Lookup(voice)
	if !ok {
		return ""
	}
	if ch.Engine.Flavor == Aivis && !m.HasInfo(voice) {
		m.Info(ctx, ch.Engine.Kind, ch.Speaker.UUID)
	}
	t, _ := m.Terms(voice)
	return t.Credit
}
