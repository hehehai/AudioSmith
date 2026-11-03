package wav

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// cr1103WAV builds the code review 1103 F8 PoC: 24-bit extensible stereo,
// 5 frames, an odd-sized LIST before the data and a 12-byte "id3 " chunk
// after it; dataSize is what the header says.
func cr1103WAV(dataSize uint32, trailer bool) []byte {
	var b bytes.Buffer
	chunk := func(id string, body []byte) {
		b.WriteString(id)
		binary.Write(&b, binary.LittleEndian, uint32(len(body)))
		b.Write(body)
		if len(body)%2 == 1 {
			b.WriteByte(0)
		}
	}
	b.WriteString("RIFF\x00\x00\x00\x00WAVE")
	fmtBody := make([]byte, 40)
	binary.LittleEndian.PutUint16(fmtBody, tagExtensible)
	binary.LittleEndian.PutUint16(fmtBody[2:], 2)
	binary.LittleEndian.PutUint32(fmtBody[4:], 48000)
	binary.LittleEndian.PutUint32(fmtBody[8:], 48000*6)
	binary.LittleEndian.PutUint16(fmtBody[12:], 6)
	binary.LittleEndian.PutUint16(fmtBody[14:], 24)
	binary.LittleEndian.PutUint16(fmtBody[16:], 22)
	binary.LittleEndian.PutUint16(fmtBody[24:], tagPCM)
	chunk("fmt ", fmtBody)
	chunk("LIST", []byte("INFOISFT\x03\x00\x00\x00ab\x00")) // odd: 15 bytes
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, dataSize)
	for i := range 10 { // 5 frames × 2 channels, 24-bit
		v := int32((i + 1) * 100000)
		b.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16)})
	}
	if trailer {
		chunk("id3 ", []byte("ID3\x04\x00\x00\x00\x00\x00\x02ab"))
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

// Code review 1103 F8: a data size of 0, 0xFFFFFFFF or past the end of the
// file, with a chunk after the data, read that chunk as samples (7 frames
// instead of 5). Open, ReadFile and windows now stop at the chunk.
func TestTrailingChunkAfterUnsizedData(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.wav")
	if err := os.WriteFile(good, cr1103WAV(30, true), 0o644); err != nil {
		t.Fatal(err)
	}
	want, _, err := ReadFile(good)
	if err != nil || want.Frames() != 5 {
		t.Fatalf("sized: %d frames, %v", want.Frames(), err)
	}
	for _, size := range []uint32{0, 0xffffffff, 0x7fffffff} {
		p := filepath.Join(dir, "s.wav")
		if err := os.WriteFile(p, cr1103WAV(size, true), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _, err := ReadFile(p)
		if err != nil || !slices.Equal(got.Samples, want.Samples) {
			t.Errorf("size %#x: ReadFile %d frames, %v", size, got.Frames(), err)
		}
		f, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		if f.Frames() != 5 {
			t.Errorf("size %#x: Open frames %d", size, f.Frames())
		}
		w, err := f.ReadFrames(2, 10)
		if err != nil || !slices.Equal(w.Samples, want.Samples[4:]) {
			t.Errorf("size %#x: window 2.. %d frames, %v", size, w.Frames(), err)
		}
		f.Close()
	}
	// Without a trailing chunk an unsized data chunk still reads to the end.
	p := filepath.Join(dir, "n.wav")
	if err := os.WriteFile(p, cr1103WAV(0, false), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, err := ReadFile(p); err != nil || got.Frames() != 5 {
		t.Errorf("no trailer: %d frames, %v", got.Frames(), err)
	}
}

// Samples that happen to look like a chunk id but do not chain to the end
// of the file stay samples.
func TestTrailingChunkNeedsAChain(t *testing.T) {
	data := make([]byte, 64)
	copy(data[16:], "LIST\x10\x00\x00\x00") // claims 16 bytes, then 32 more follow
	if got := samplesEnd(bytes.NewReader(data), 0, int64(len(data)), 4); got != int64(len(data)) {
		t.Errorf("end %d", got)
	}
	data = make([]byte, 64)
	copy(data[40:], "LIST\x10\x00\x00\x00") // 40 + 8 + 16 = 64: the end
	if got := samplesEnd(bytes.NewReader(data), 0, int64(len(data)), 4); got != 40 {
		t.Errorf("end %d, want 40", got)
	}
}
