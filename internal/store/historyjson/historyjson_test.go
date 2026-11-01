package historyjson

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mvpIndex is a history.json as the MVP wrote it.
const mvpIndex = `[
  {
    "id": "20261013-215200.000",
    "createdAt": "2026-10-13T21:52:00+08:00",
    "file": "20261013-215200.000.wav",
    "text": "Newer",
    "model": "mlx-community/Qwen3-TTS-12Hz-0.6B-CustomVoice-8bit",
    "voice": "Vivian",
    "language": "English",
    "temperature": 0.4,
    "topP": 0.8,
    "maxTokens": 900,
    "sampleRate": 24000,
    "audioSeconds": 2.5,
    "genSeconds": 1.25
  },
  {
    "id": "20261013-215000.000",
    "createdAt": "2026-10-13T21:50:00+08:00",
    "file": "20261013-215000.000.wav",
    "text": "Older",
    "model": "mlx-community/Kokoro-82M-bf16",
    "voice": "af_heart",
    "speed": 1.25,
    "sampleRate": 24000,
    "audioSeconds": 1,
    "genSeconds": 0.5
  },
  {
    "id": "gone",
    "createdAt": "2026-10-13T21:55:00+08:00",
    "file": "gone.wav",
    "text": "Audio deleted by hand",
    "model": "m",
    "sampleRate": 0,
    "audioSeconds": 0,
    "genSeconds": 0
  }
]`

func TestMVPHistory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "clips"), 0o755)
	for _, f := range []string{"20261013-215200.000.wav", "20261013-215000.000.wav"} {
		os.WriteFile(filepath.Join(dir, "clips", f), []byte("RIFF"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "history.json"), []byte(mvpIndex), 0o644)

	// Newest first; "gone" has no audio file, so Read drops it.
	all, err := Read(dir)
	if err != nil || len(all) != 2 || all[0].Text != "Newer" || all[1].Speed != 1.25 || all[0].RTF() != 2 {
		t.Fatalf("takes %+v %v", all, err)
	}
}

func TestEmpty(t *testing.T) {
	if all, err := Read(t.TempDir()); err != nil || all != nil {
		t.Errorf("%v %v", all, err)
	}
	if NewTakeID(time.Date(2026, 1, 2, 3, 4, 5, 6e6, time.UTC)) != "20260102-030405.006" {
		t.Error(NewTakeID(time.Date(2026, 1, 2, 3, 4, 5, 6e6, time.UTC)))
	}
}
