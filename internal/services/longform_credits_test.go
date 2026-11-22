package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hehehai/AudioSmith/internal/core"
)

// A render's credits are its voices' (once each, in order of first use),
// written as credits.txt (thread #171).
func TestLongformCredits(t *testing.T) {
	s := &Longform{render: &renderer{credit: func(_ context.Context, model, voice string) string {
		if model == "external/voicevox" && strings.HasPrefix(voice, "voicevox:") {
			return "VOICEVOX:" + strings.TrimPrefix(voice, "voicevox:")
		}
		return ""
	}}}
	spec := LongformSpec{Model: "external/voicevox", Voices: map[string]core.Take{
		"n": {Voice: "voicevox:ずんだもん"}, "a": {Voice: "voicevox:四国めたん"}, "b": {Voice: "voicevox:ずんだもん"}, "x": {Voice: "other"}},
		Chapters: []ChapterSpec{{Lines: []LineSpec{{Spans: []SpanSpec{{Voice: "n"}, {Voice: "x"}}}, {Spans: []SpanSpec{{Voice: "a"}, {Voice: "b"}}}}}}}
	got := s.credits(context.Background(), &spec)
	if strings.Join(got, "|") != "VOICEVOX:ずんだもん|VOICEVOX:四国めたん" {
		t.Fatalf("credits %q", got)
	}
	dir := t.TempDir()
	if err := WriteCredits(dir, got); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, CreditsFile))
	if err != nil || !strings.Contains(string(b), "VOICEVOX:四国めたん\n") {
		t.Fatalf("credits.txt %q %v", b, err)
	}
	empty := t.TempDir()
	WriteCredits(empty, nil)
	if _, err := os.Stat(filepath.Join(empty, CreditsFile)); err == nil {
		t.Fatal("credits.txt without credits")
	}
}
