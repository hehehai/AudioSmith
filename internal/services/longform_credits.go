package services

// The credits of a longform render (thread #171): the attributions its
// voices' audio must carry (VOICEVOX characters'), kept in its summary
// and written as credits.txt beside its files.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// CreditsFile is the name of the credits beside a render's or an
// export's files.
const CreditsFile = "credits.txt"

// credits are the credits of the voices spec's lines speak with.
func (s *Longform) credits(ctx context.Context, spec *LongformSpec) []string {
	if s.render == nil || s.render.credit == nil {
		return nil
	}
	used := map[string]bool{}
	var out []string
	for _, ch := range spec.Chapters {
		for _, l := range ch.Lines {
			for _, sp := range l.Spans {
				if used[sp.Voice] {
					continue
				}
				used[sp.Voice] = true
				v := spec.Voices[sp.Voice]
				model := v.Model
				if model == "" {
					model = spec.Model
				}
				if c := s.render.credit(ctx, model, v.Voice); c != "" && !slices.Contains(out, c) {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

// CreditsText is the text of a credits.txt: what to name when the audio
// is published, one credit a line.
func CreditsText(credits []string) string {
	if len(credits) == 0 {
		return ""
	}
	return "Credits (name them wherever this audio is published):\n" + strings.Join(credits, "\n") + "\n"
}

// WriteCredits writes credits.txt in dir; nothing when there are none.
func WriteCredits(dir string, credits []string) error {
	if len(credits) == 0 {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, CreditsFile), []byte(CreditsText(credits)), 0o644)
}
