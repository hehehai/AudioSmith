package main

// Japanese character voices (thread #171): the VOICEVOX and AivisSpeech
// engines the user installed and runs, which AudioSmith only connects to
// (internal/voicevox). The library's manager probes them, routes their
// takes' tts.synthesize calls to them and records each take's credits;
// the Studio offers their characters while one answers
// (voicevox_view.go), asks once before a character whose terms are
// non-commercial or to be asked about, and keeps their scales.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/AudioSmith/internal/voicevox"
)

// charactersState is the app's part.
type charactersState struct {
	vvPoked      time.Time // the last background probe asked
	vvPrefetched string    // the engines whose characters' infos were read ("url@version …")
	// vvProbe lets a test without a window probe (others never touch the
	// network).
	vvProbe bool
	// vvLast is the last character voice the Studio spoke with, given
	// back when its model is chosen again; vvScales its Advanced scales.
	vvLast   string
	vvScales core.Scales
	// vvAsk is the terms confirmation showing (nil: none).
	vvAsk *characterAsk
}

// characterAsk is a confirmation of a character's terms before its first
// take.
type characterAsk struct {
	voice, name, policy string
	terms               voicevox.Terms
	agree               bool
	then                func()
}

// charactersEntry is the catalogue's entry of the connector.
func charactersEntry() catalog.Entry {
	for _, e := range catalog.Builtin().External {
		if e.Runtime == "voicevox" {
			return e
		}
	}
	return catalog.Entry{}
}

// newCharacterEngines makes the library's manager: the addresses from
// Settings → Models, the voice samples in the cache.
func (l *library) newCharacterEngines(cacheDir string) *voicevox.Manager {
	return voicevox.New(voicevox.Options{
		URLs: func() map[voicevox.Kind]string {
			if l.settings == nil {
				return nil
			}
			ce := l.settings.Get().Models.Characters
			return map[voicevox.Kind]string{voicevox.VOICEVOX: ce.VoicevoxURL, voicevox.Aivis: ce.AivisURL}
		},
		Dir: filepath.Join(cacheDir, "voicevox"),
		Changed: func() {
			if fn := l.vvChanged.Load(); fn != nil {
				(*fn)()
			}
		},
	})
}

// takeCredit is services.Config.Credit: the credit of a take of an
// outside engine's character ("" = none needed).
func (l *library) takeCredit(ctx context.Context, model, voice string) string {
	if l.vv == nil || !entryFor(model).IsExternal() {
		return ""
	}
	if _, ok := l.vv.Lookup(voice); !ok {
		l.vv.Refresh(ctx, 0)
	}
	return l.vv.Credit(ctx, voice)
}

// errCharactersOffline: neither engine answers.
type errCharactersOffline struct{}

func (errCharactersOffline) Error() string {
	return i18n.T("Neither VOICEVOX nor AivisSpeech is running: open one of them, then try again.")
}

// characterEngineReady is modelReady of the connector: an engine answers.
func (l *library) characterEngineReady(ctx context.Context) error {
	if l.vv == nil {
		return errCharactersOffline{}
	}
	l.vv.Refresh(ctx, 10*time.Second)
	if !l.vv.Online() {
		return errCharactersOffline{}
	}
	return nil
}

// externalRouter runs the tts.synthesize calls of the connector's model
// on the outside engines and the others on the app's engine (eng; nil:
// not connected).
func externalRouter(eng, outside jobs.Executor) jobs.Executor {
	return jobs.ExecutorFunc(func(ctx context.Context, job jobs.Job, report func(jobs.Progress), chunk func(json.RawMessage)) (json.RawMessage, error) {
		var head struct {
			Model string `json:"model"`
		}
		json.Unmarshal(job.Params, &head)
		if head.Model != "" && entryFor(head.Model).IsExternal() {
			return outside.Run(ctx, job, report, chunk)
		}
		if eng == nil {
			return nil, &jobs.Error{Code: "engine_unavailable", Message: "The engine is not connected.", Retryable: true}
		}
		return eng.Run(ctx, job, report, chunk)
	})
}

// openCharacters redraws the window when the manager learns something.
func (a *app) openCharacters(lib *library) {
	redraw := func() { a.post(func() {}) }
	lib.vvChanged.Store(&redraw)
}

// pokeCharacters probes the engines in the background, at most every few
// seconds (views call it while they show the characters); online engines'
// characters' infos (terms, samples) are read once per engine version.
func (a *app) pokeCharacters() {
	if a.lib == nil || a.lib.vv == nil || a.win == nil && !a.vvProbe || time.Since(a.vvPoked) < 5*time.Second {
		return
	}
	a.vvPoked = time.Now()
	vv := a.lib.vv
	a.background(func() {
		vv.Refresh(context.Background(), 15*time.Second)
		key := ""
		for _, e := range vv.Engines() {
			if e.Online {
				key += e.URL + "@" + e.Version + " "
			}
		}
		if key == "" {
			return
		}
		a.post(func() {
			if a.vvPrefetched == key {
				return
			}
			a.vvPrefetched = key
			a.background(func() { vv.Prefetch(context.Background()) })
		})
	})
}

// characterVoices are the characters' styles of the online engines, in
// their order: the Studio's voice list.
func (a *app) characterVoices() []voicevox.Character {
	var out []voicevox.Character
	if a.lib == nil || a.lib.vv == nil {
		return nil
	}
	for _, e := range a.lib.vv.Engines() {
		if !e.Online {
			continue
		}
		for _, s := range e.Speakers {
			for _, st := range s.Styles {
				out = append(out, voicevox.Character{Engine: e, Speaker: s, Style: st})
			}
		}
	}
	return out
}

// characterVoiceID is a Character's voice id.
func characterVoiceID(ch voicevox.Character) string {
	return voicevox.Voice{Kind: ch.Engine.Kind, Speaker: ch.Speaker.UUID, Style: ch.Style.ID}.String()
}

// studioCharacterVoice is the voice the Studio speaks with on the
// connector: the chosen one, else the first style of the first online
// character ("" when none answers).
func (a *app) studioCharacterVoice() string {
	if voicevox.IsVoice(a.voice) {
		return a.voice
	}
	if vs := a.characterVoices(); len(vs) > 0 {
		return characterVoiceID(vs[0])
	}
	return ""
}

// confirmKey is the settings' key of a voice's character.
func confirmKey(voice string) string {
	v, _ := voicevox.ParseVoice(voice)
	return string(v.Kind) + ":" + v.Speaker
}

// characterConfirmed reports whether the user may speak with voice
// without being asked: its terms need no confirmation, or it was given.
func (a *app) characterConfirmed(voice string) bool {
	if a.lib == nil || a.lib.vv == nil {
		return true
	}
	t, ok := a.lib.vv.Terms(voice)
	if !ok || !t.Confirm() {
		return true
	}
	return slices.Contains(a.lib.settings.Get().Models.Characters.Confirmed, confirmKey(voice))
}

// askCharacterTerms shows the terms of voice's character, then runs then
// once the user confirms them.
func (a *app) askCharacterTerms(voice string, then func()) {
	ch, _ := a.lib.vv.Lookup(voice)
	t, _ := a.lib.vv.Terms(voice)
	a.vvAsk = &characterAsk{voice: voice, name: ch.Speaker.Name, policy: a.lib.vv.Policy(voice), terms: t, then: then}
}

// confirmCharacter records the confirmation of a voice's character.
func (a *app) confirmCharacter(voice string) {
	key := confirmKey(voice)
	a.updateSettings(func(s *settings.Settings) {
		if !slices.Contains(s.Models.Characters.Confirmed, key) {
			s.Models.Characters.Confirmed = append(s.Models.Characters.Confirmed, key)
		}
	})
}

// characterScales are the Studio's scales for a recipe (nil: none set).
func (a *app) characterScales() *core.Scales {
	if a.vvScales == (core.Scales{}) {
		return nil
	}
	s := a.vvScales
	return &s
}

// isCharactersOffline reports whether err is errCharactersOffline.
func isCharactersOffline(err error) bool { return errors.As(err, new(errCharactersOffline)) }
