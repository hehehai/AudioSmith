package wav

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// A credit appended as LIST/INFO keeps the samples as they were and the
// RIFF size right (thread #171).
func TestAppendInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	b := dsp.Buffer{Rate: 24000, Channels: 1, Samples: make([]float32, 2401)}
	for i := range b.Samples {
		b.Samples[i] = float32(i%100) / 200
	}
	var w bytes.Buffer
	if err := Encode(&w, b, PCM16); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, w.Bytes(), 0o644)
	if err := AppendInfo(path, map[string]string{"ICMT": "VOICEVOX:ずんだもん", "IART": ""}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("ICMT")) || !bytes.Contains(data, []byte("VOICEVOX:ずんだもん\x00")) || bytes.Contains(data, []byte("IART")) {
		t.Fatalf("no comment, or an empty artist: % x", data[len(data)-48:])
	}
	if got := int(data[4]) | int(data[5])<<8 | int(data[6])<<16 | int(data[7])<<24; got != len(data)-8 || len(data)%2 != 0 {
		t.Fatalf("RIFF size %d for %d bytes", got, len(data))
	}
	got, _, err := ReadFile(path)
	if err != nil || got.Frames() != b.Frames() {
		t.Fatalf("read back %d frames, %v", got.Frames(), err)
	}
}
