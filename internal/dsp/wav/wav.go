// Package wav reads and writes metadata-free WAV files of float32 samples.
//
// Reading accepts integer PCM of 8, 16, 24 and 32 bits and IEEE float of 32
// and 64 bits, WAVE_FORMAT_EXTENSIBLE, unknown chunks (skipped), streamed
// files whose data size is 0 or 0xFFFFFFFF (read to EOF, or to a chunk
// after the samples when the file is seekable) and RF64.
//
// Writing produces 16- or 24-bit PCM or 32-bit float, with a 44-byte
// canonical header and no other chunks (unless Options.Large reserves room
// for an RF64 upgrade). The Writer streams: it patches the header on Close,
// so long renders never sit in memory. Create writes to a temporary file that
// Close renames into place, so a crash never leaves a half-written file.
//
// Integer scaling follows mygo/audio: int16 v ↔ v/32768, int24 v ↔ v/2^23.
// Out-of-range samples are clipped on integer output; float output is
// written as is.
package wav

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// Encoding is a WAV sample encoding.
type Encoding uint8

const (
	PCM16   Encoding = iota + 1 // 16-bit signed integer (WAVE_FORMAT_PCM)
	PCM24                       // 24-bit signed integer (WAVE_FORMAT_PCM)
	Float32                     // 32-bit IEEE float (WAVE_FORMAT_IEEE_FLOAT)
	PCM8                        // 8-bit unsigned integer; read only
	PCM32                       // 32-bit signed integer; read only
	Float64                     // 64-bit IEEE float; read only
)

// String returns a short code: "pcm16", "pcm24", "f32", ….
func (e Encoding) String() string {
	switch e {
	case PCM8:
		return "pcm8"
	case PCM16:
		return "pcm16"
	case PCM24:
		return "pcm24"
	case PCM32:
		return "pcm32"
	case Float32:
		return "f32"
	case Float64:
		return "f64"
	}
	return fmt.Sprintf("encoding(%d)", uint8(e))
}

// Bits returns the bits per sample.
func (e Encoding) Bits() int {
	switch e {
	case PCM8:
		return 8
	case PCM16:
		return 16
	case PCM24:
		return 24
	case PCM32, Float32:
		return 32
	case Float64:
		return 64
	}
	return 0
}

// Format describes a WAV stream.
type Format struct {
	Rate     int
	Channels int
	Encoding Encoding
}

// FrameBytes returns the bytes per frame.
func (f Format) FrameBytes() int { return f.Channels * f.Encoding.Bits() / 8 }

// BytesPerSecond returns the data rate, for size estimates (MB per minute).
func (f Format) BytesPerSecond() int { return f.Rate * f.FrameBytes() }

func (f Format) check(write bool) error {
	if f.Rate <= 0 || f.Channels <= 0 || f.Channels > 64 || f.Encoding.Bits() == 0 {
		return fmt.Errorf("%w: rate %d, channels %d, %v", dsp.ErrFormat, f.Rate, f.Channels, f.Encoding)
	}
	if write && f.Encoding != PCM16 && f.Encoding != PCM24 && f.Encoding != Float32 {
		return fmt.Errorf("wav: cannot write %v", f.Encoding)
	}
	return nil
}

var (
	// ErrInvalid reports a malformed or unsupported WAV file.
	ErrInvalid = errors.New("wav: invalid file")
	// ErrTooLarge reports data beyond 4 GiB written without Options.Large.
	ErrTooLarge = errors.New("wav: data larger than 4 GiB needs Options.Large")
)

const (
	tagPCM        = 1
	tagFloat      = 3
	tagExtensible = 0xfffe
)

// Reader decodes a WAV stream. It needs only an io.Reader.
type Reader struct {
	r      io.Reader
	f      Format
	frames int64 // -1 if unknown
	pos    int64
	buf    []byte
	data   int64 // byte offset of the data chunk's samples
}

// NewReader parses the header up to the data chunk.
func NewReader(r io.Reader) (*Reader, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var h [12]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return nil, ErrInvalid
	}
	rf64 := string(h[:4]) == "RF64"
	if (string(h[:4]) != "RIFF" && !rf64) || string(h[8:12]) != "WAVE" {
		return nil, ErrInvalid
	}
	d := &Reader{r: br, frames: -1, data: 12}
	var ds64Data int64 = -1
	haveFmt := false
	for {
		var ch [8]byte
		if _, err := io.ReadFull(br, ch[:]); err != nil {
			return nil, ErrInvalid
		}
		size := int64(binary.LittleEndian.Uint32(ch[4:]))
		id := string(ch[:4])
		d.data += 8
		switch id {
		case "ds64":
			if size < 24 || size > 1<<16 {
				return nil, ErrInvalid
			}
			b := make([]byte, size+size&1)
			if _, err := io.ReadFull(br, b); err != nil {
				return nil, ErrInvalid
			}
			ds64Data = int64(binary.LittleEndian.Uint64(b[8:]))
			d.data += int64(len(b))
			continue
		case "fmt ":
			if size < 16 || size > 1<<16 {
				return nil, ErrInvalid
			}
			b := make([]byte, size+size&1)
			if _, err := io.ReadFull(br, b); err != nil {
				return nil, ErrInvalid
			}
			tag := binary.LittleEndian.Uint16(b)
			d.f.Channels = int(binary.LittleEndian.Uint16(b[2:]))
			d.f.Rate = int(binary.LittleEndian.Uint32(b[4:]))
			bits := int(binary.LittleEndian.Uint16(b[14:]))
			if tag == tagExtensible && size >= 40 {
				tag = binary.LittleEndian.Uint16(b[24:])
			}
			switch {
			case tag == tagPCM && bits == 8:
				d.f.Encoding = PCM8
			case tag == tagPCM && bits == 16:
				d.f.Encoding = PCM16
			case tag == tagPCM && bits == 24:
				d.f.Encoding = PCM24
			case tag == tagPCM && bits == 32:
				d.f.Encoding = PCM32
			case tag == tagFloat && bits == 32:
				d.f.Encoding = Float32
			case tag == tagFloat && bits == 64:
				d.f.Encoding = Float64
			default:
				return nil, fmt.Errorf("%w: format tag %d, %d bits", ErrInvalid, tag, bits)
			}
			if err := d.f.check(false); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
			}
			haveFmt = true
			d.data += int64(len(b))
			continue
		case "data":
			if !haveFmt {
				return nil, ErrInvalid
			}
			switch {
			case rf64 && size == 0xffffffff && ds64Data >= 0:
				size = ds64Data
			case size == 0 || size == 0xffffffff:
				size = -1 // written as it streamed: read to EOF
			}
			if size >= 0 {
				d.frames = size / int64(d.f.FrameBytes())
			}
			return d, nil
		}
		if _, err := io.CopyN(io.Discard, br, size+size&1); err != nil {
			return nil, ErrInvalid
		}
		d.data += size + size&1
	}
}

// Format returns the stream's format.
func (d *Reader) Format() Format { return d.f }

// Frames returns the frames in the data chunk, or -1 if unknown.
func (d *Reader) Frames() int64 { return d.frames }

// Read decodes up to len(dst)/channels whole frames into dst and returns
// the samples written, with io.EOF at the end.
func (d *Reader) Read(dst []float32) (int, error) {
	fb := d.f.FrameBytes()
	n := len(dst) / d.f.Channels
	if d.frames >= 0 {
		n = int(min(int64(n), d.frames-d.pos))
	}
	if n <= 0 {
		return 0, io.EOF
	}
	if cap(d.buf) < n*fb {
		d.buf = make([]byte, n*fb)
	}
	b := d.buf[:n*fb]
	got, err := io.ReadFull(d.r, b)
	n = got / fb
	if n == 0 {
		if err == nil || err == io.ErrUnexpectedEOF {
			err = io.EOF
		}
		return 0, err
	}
	decode(dst[:n*d.f.Channels], b, d.f.Encoding)
	d.pos += int64(n)
	return n * d.f.Channels, nil
}

func decode(dst []float32, b []byte, e Encoding) {
	switch e {
	case PCM8:
		for i := range dst {
			dst[i] = (float32(b[i]) - 128) / 128
		}
	case PCM16:
		for i := range dst {
			dst[i] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / (1 << 15)
		}
	case PCM24:
		for i := range dst {
			s := b[3*i:]
			dst[i] = float32(int32(uint32(s[0])<<8|uint32(s[1])<<16|uint32(s[2])<<24)>>8) / (1 << 23)
		}
	case PCM32:
		for i := range dst {
			dst[i] = float32(float64(int32(binary.LittleEndian.Uint32(b[4*i:]))) / (1 << 31))
		}
	case Float32:
		for i := range dst {
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
	case Float64:
		for i := range dst {
			dst[i] = float32(math.Float64frombits(binary.LittleEndian.Uint64(b[8*i:])))
		}
	}
}

// Decode reads a whole WAV stream into memory.
func Decode(r io.Reader) (dsp.Buffer, Format, error) {
	return decodeAll(r, -1)
}

// decodeAll reads a whole WAV stream; size, when known (≥ 0), is the
// stream's length in bytes, which caps what the header may ask for.
func decodeAll(r io.Reader, size int64) (dsp.Buffer, Format, error) {
	d, err := NewReader(r)
	if err != nil {
		return dsp.Buffer{}, Format{}, err
	}
	f := d.Format()
	b := dsp.Buffer{Rate: f.Rate, Channels: f.Channels}
	n := d.Frames()
	if size >= 0 {
		// The file's size bounds the frames, so a hostile header cannot
		// ask for more than the file holds; a streamed header (no size)
		// gets it from the file.
		have := max(size-d.data, 0) / int64(f.FrameBytes())
		if n < 0 || n > have {
			n = have
		}
	} else if n*int64(f.Channels) > 1<<24 {
		// Trust a header with nothing to check it against only so far.
		n = -1
	}
	if n >= 0 {
		b.Samples = make([]float32, 0, int(n)*f.Channels)
	}
	const chunk = 16384
	var buf []float32
	for {
		if n >= 0 && len(b.Samples) < cap(b.Samples) {
			// Decode straight into the buffer, a chunk at a time: no
			// second copy.
			k, err := d.Read(b.Samples[len(b.Samples):min(cap(b.Samples), len(b.Samples)+chunk*f.Channels)])
			b.Samples = b.Samples[:len(b.Samples)+k]
			if err == io.EOF {
				return b, f, nil
			}
			if err != nil {
				return b, f, err
			}
			continue
		}
		if buf == nil {
			buf = make([]float32, chunk*f.Channels)
		}
		k, err := d.Read(buf)
		b.Samples = append(b.Samples, buf[:k]...)
		if err == io.EOF {
			return b, f, nil
		}
		if err != nil {
			return b, f, err
		}
	}
}

// ReadFile reads a whole WAV file into memory, sized from the file so it
// allocates the samples once.
func ReadFile(path string) (dsp.Buffer, Format, error) {
	if fi, err := os.Stat(path); err == nil && !fi.Mode().IsRegular() {
		f, err := os.Open(path) // a pipe: read to EOF
		if err != nil {
			return dsp.Buffer{}, Format{}, err
		}
		defer f.Close()
		return decodeAll(f, -1)
	}
	w, err := Open(path) // frames bounded by the file, chunks after the samples left out
	if err != nil {
		return dsp.Buffer{}, Format{}, err
	}
	defer w.Close()
	b, err := w.ReadFrames(0, -1)
	return b, w.Format(), err
}
