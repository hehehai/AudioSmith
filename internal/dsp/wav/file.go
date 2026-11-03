package wav

import (
	"bufio"
	"io"
	"os"

	"github.com/hehehai/AudioSmith/internal/dsp"
)

// File is an open WAV file whose frames can be read in any order, so a
// long recording is never held whole: windows, the start, or a stream.
type File struct {
	f      *os.File
	format Format
	frames int64
	data   int64 // byte offset of the samples
}

// Open opens a WAV file and reads its header.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	d, err := NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	wf := &File{f: f, format: d.Format(), frames: d.Frames(), data: d.data}
	if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() {
		// The file's size bounds the frames (and gives them for a header
		// written as it streamed).
		have := max(fi.Size()-d.data, 0) / int64(wf.format.FrameBytes())
		if wf.frames < 0 || wf.frames > have {
			wf.frames = have
		}
	}
	return wf, nil
}

// Format returns the file's format.
func (w *File) Format() Format { return w.format }

// Frames returns the frames in the file (-1 if unknown: not a regular file
// and a streamed header).
func (w *File) Frames() int64 { return w.frames }

// Seconds returns the file's length (0 if unknown).
func (w *File) Seconds() float64 {
	return float64(max(w.frames, 0)) / float64(w.format.Rate)
}

// Close closes the file.
func (w *File) Close() error { return w.f.Close() }

// Stream returns a Reader of frames [from, to) (to < 0: to the end). It
// shares the file's position: use one at a time.
func (w *File) Stream(from, to int64) (*Reader, error) {
	from = max(from, 0)
	if w.frames >= 0 && (to < 0 || to > w.frames) {
		to = w.frames
	}
	if _, err := w.f.Seek(w.data+from*int64(w.format.FrameBytes()), io.SeekStart); err != nil {
		return nil, err
	}
	n := int64(-1)
	if to >= 0 {
		n = max(to-from, 0)
	}
	return &Reader{r: bufio.NewReaderSize(w.f, 64<<10), f: w.format, frames: n, data: w.data}, nil
}

// ReadFrames reads frames [from, to) into memory (clamped to the file;
// to < 0 reads to the end).
func (w *File) ReadFrames(from, to int64) (dsp.Buffer, error) {
	d, err := w.Stream(from, to)
	if err != nil {
		return dsp.Buffer{}, err
	}
	b := dsp.Buffer{Rate: w.format.Rate, Channels: w.format.Channels}
	if d.frames >= 0 {
		b.Samples = make([]float32, d.frames*int64(w.format.Channels))
		got := 0
		for got < len(b.Samples) {
			k, err := d.Read(b.Samples[got:min(len(b.Samples), got+16384*w.format.Channels)])
			got += k
			if err == io.EOF {
				break
			}
			if err != nil {
				return dsp.Buffer{}, err
			}
		}
		b.Samples = b.Samples[:got]
		return b, nil
	}
	buf := make([]float32, 16384*w.format.Channels)
	for {
		k, err := d.Read(buf)
		b.Samples = append(b.Samples, buf[:k]...)
		if err == io.EOF {
			return b, nil
		}
		if err != nil {
			return dsp.Buffer{}, err
		}
	}
}

// ReadRange reads frames [from, to) of the WAV file at path (clamped; to
// < 0 reads to the end) without reading the rest.
func ReadRange(path string, from, to int64) (dsp.Buffer, Format, error) {
	w, err := Open(path)
	if err != nil {
		return dsp.Buffer{}, Format{}, err
	}
	defer w.Close()
	b, err := w.ReadFrames(from, to)
	return b, w.Format(), err
}

// ReadSeconds reads the seconds [from, to) of the WAV file at path, like
// ReadRange.
func ReadSeconds(path string, from, to float64) (dsp.Buffer, Format, error) {
	w, err := Open(path)
	if err != nil {
		return dsp.Buffer{}, Format{}, err
	}
	defer w.Close()
	r := w.Format().Rate
	end := int64(-1)
	if to >= 0 {
		end = int64(dsp.FramesAt(to, r))
	}
	b, err := w.ReadFrames(int64(dsp.FramesAt(from, r)), end)
	return b, w.Format(), err
}
