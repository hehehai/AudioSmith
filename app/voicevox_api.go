package main

// The Local API's and MCP's part of the Japanese character voices
// (thread #171): a request names a character by its voice id, its name
// or "name/style"; the take's credit comes back with it (api.TakeJSON,
// X-AudioSmith-Credit, generate_speech's credit).

import (
	"context"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/voicevox"
)

var _ api.CharacterBackend = (*apiBackend)(nil)

// CharacterVoice implements api.CharacterBackend.
func (b *apiBackend) CharacterVoice(ctx context.Context, name string) (string, bool) {
	vv := b.lib.vv
	if vv == nil {
		return "", false
	}
	vv.Refresh(ctx, 10*time.Second)
	return findCharacter(vv.Engines(), name)
}

// characterVoiceFor is the voice a request speaks with: the one it
// named, else the first style of the first character that answers.
func (b *apiBackend) characterVoiceFor(ctx context.Context, voice string) string {
	if voicevox.IsVoice(voice) || b.lib.vv == nil {
		return voice
	}
	b.lib.vv.Refresh(ctx, 10*time.Second)
	for _, e := range b.lib.vv.Engines() {
		if e.Online && len(e.Speakers) > 0 && len(e.Speakers[0].Styles) > 0 {
			return voicevox.Voice{Kind: e.Kind, Speaker: e.Speakers[0].UUID, Style: e.Speakers[0].Styles[0].ID}.String()
		}
	}
	return voice
}

// findCharacter finds name among the online engines' characters: a voice
// id, "name", "name/style", "name（style）" or "name (style)"; a name alone
// is its first style.
func findCharacter(engines []voicevox.Engine, name string) (string, bool) {
	n := strings.TrimSpace(name)
	if v, ok := voicevox.ParseVoice(n); ok {
		for _, e := range engines {
			if e.Kind != v.Kind || !e.Online {
				continue
			}
			for _, s := range e.Speakers {
				for _, st := range s.Styles {
					if s.UUID == v.Speaker && st.ID == v.Style {
						return n, true
					}
				}
			}
		}
		return "", false
	}
	char, style := n, ""
	for _, sep := range []string{"/", "（", " ("} {
		if c, s, ok := strings.Cut(n, sep); ok {
			char, style = strings.TrimSpace(c), strings.TrimSpace(strings.TrimRight(s, "）) "))
			break
		}
	}
	for _, e := range engines {
		if !e.Online {
			continue
		}
		for _, s := range e.Speakers {
			if !strings.EqualFold(s.Name, char) {
				continue
			}
			for _, st := range s.Styles {
				if style == "" || strings.EqualFold(st.Name, style) {
					return voicevox.Voice{Kind: e.Kind, Speaker: s.UUID, Style: st.ID}.String(), true
				}
			}
		}
	}
	return "", false
}

// characterModel is the connector as a model of the API's list, with
// its characters, while an engine answers.
func (b *apiBackend) characterModel(ctx context.Context) (api.Model, bool) {
	vv, e := b.lib.vv, charactersEntry()
	if vv == nil || e.ID == "" {
		return api.Model{}, false
	}
	vv.Refresh(ctx, 15*time.Second)
	vv.Prefetch(ctx) // the AivisSpeech models' licences (read once)
	m := api.Model{Entry: e, Downloaded: true}
	for _, en := range vv.Engines() {
		if !en.Online {
			continue
		}
		for _, s := range en.Speakers {
			for _, st := range s.Styles {
				id := voicevox.Voice{Kind: en.Kind, Speaker: s.UUID, Style: st.ID}.String()
				tm, _ := vv.Terms(id)
				m.Speakers = append(m.Speakers, api.SpeakerJSON{ID: id, Language: "ja", Name: s.Name, Style: st.Name, Credit: tm.Credit, Use: string(tm.Use)})
			}
		}
	}
	return m, len(m.Speakers) > 0
}
