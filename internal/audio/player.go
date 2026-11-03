package audio

import (
	"os"
	"time"

	myaudio "github.com/egoist/mygo/audio"
)

// Player plays one take at a time, identified by an id, streaming it from
// its file (myaudio.OpenStream decodes about a second ahead), so long
// audio plays without being read into memory. Loading opens the file and
// reads its header only. Use it from the UI thread (not goroutine-safe).
type Player struct {
	id     string
	p      *myaudio.Player
	stream *Stream       // the audio of a take still rendering (PlayStream); nil for files
	reader *StreamReader // the player's reader of stream
}

func (pl *Player) load(id, path string) error {
	pl.Stop()
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	p, err := myaudio.OpenStream(f) // closes f when the player closes
	if err != nil {
		f.Close()
		return err
	}
	pl.id = id
	pl.p = p
	return nil
}

// Toggle plays id's audio at path, loading it first, or pauses it while it
// plays. A preview stream of id (PlayStream) that is paused or ended gives
// way to the file, which can seek.
func (pl *Player) Toggle(id, path string) error {
	if pl.id == id && pl.stream != nil && pl.p != nil && !pl.p.Playing() && pl.stream.Done() {
		pl.Stop()
	}
	if pl.id != id || pl.p == nil {
		if err := pl.load(id, path); err != nil {
			return err
		}
	}
	if pl.p.Playing() {
		pl.p.Pause()
	} else {
		pl.p.Play()
	}
	return nil
}

// PlayStream plays s, audio that grows while it plays, as id's: the
// preview of a take still rendering. Stop (or another Toggle or
// PlayStream) closes it.
func (pl *Player) PlayStream(id string, s *Stream) {
	pl.Stop()
	pl.id, pl.stream, pl.reader = id, s, s.NewReader()
	pl.p = myaudio.NewPlayer(pl.reader)
	pl.p.Play()
}

// Streaming reports whether id's audio is a preview stream.
func (pl *Player) Streaming(id string) bool { return pl.p != nil && pl.id == id && pl.stream != nil }

// Stop stops, closes the stream (and its file) and forgets the audio.
func (pl *Player) Stop() {
	if pl.reader != nil {
		pl.reader.Close() // wakes the player's decoder if it waits for audio
	}
	if pl.p != nil {
		pl.p.Close()
	}
	pl.p, pl.stream, pl.reader = nil, nil, nil
	pl.id = ""
}

// Current is the id of the loaded audio, playing or paused; "" when none.
func (pl *Player) Current() string { return pl.id }

// Active reports whether anything is playing.
func (pl *Player) Active() bool { return pl.p != nil && pl.p.Playing() }

// Playing reports whether id is playing.
func (pl *Player) Playing(id string) bool {
	return pl.p != nil && pl.id == id && pl.p.Playing()
}

// Left is when to look whether the audio playing ended: soon after the
// time it has left, within leftMax, or leftStream for a stream still
// growing; 0 when nothing plays.
func (pl *Player) Left() time.Duration {
	if !pl.Active() {
		return 0
	}
	d := pl.p.Duration()
	if pl.stream != nil || d <= 0 {
		return leftStream
	}
	return min(max(d-pl.p.Position()+leftEnd, leftEnd), leftMax)
}

// Left's bounds: a margin past the end, the longest wait, a stream's.
const (
	leftEnd    = 50 * time.Millisecond
	leftMax    = 2 * time.Second
	leftStream = 200 * time.Millisecond
)

// Progress is where id is, from 0 to 1 (0 when it is not loaded).
func (pl *Player) Progress(id string) float32 {
	if pl.p == nil || pl.id != id {
		return 0
	}
	d := pl.p.Duration()
	if pl.stream != nil {
		d = time.Duration(pl.stream.Seconds() * float64(time.Second))
	}
	if d <= 0 {
		return 0
	}
	return min(float32(pl.p.Position())/float32(d), 1)
}

// Seek moves id to fraction (0–1) of its length, if it is loaded.
func (pl *Player) Seek(id string, fraction float32) {
	if pl.p == nil || pl.id != id {
		return
	}
	d := pl.p.Duration()
	if pl.stream != nil {
		d = time.Duration(pl.stream.Seconds() * float64(time.Second))
	}
	pl.p.Seek(time.Duration(float64(d) * float64(fraction)))
}
