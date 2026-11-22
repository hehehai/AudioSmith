package main

// The curated voices' bundled audio (thread #94): each designed voice of
// Discover voices ships its reference (<id>.ref.ogg, 10–15 s, the best of
// several takes that says its text) and its preview (<id>.preview.ogg,
// its preview line cloned from that reference, as the Studio would), Ogg
// Vorbis made by scripts/make-curated-voices.sh. "Use voice" decodes them
// into the saved voice, so it is ready at once: no reference to wait for.
// Generated with Qwen3-TTS VoiceDesign and Base (Apache-2.0), the Japanese
// ones with Irodori-TTS (MIT, #172); see NOTICE.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/assets"

	"github.com/egoist/mygo/audio/vorbis"
	"github.com/hehehai/AudioSmith/internal/dsp"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/gallery"
)

var galleryVoicesFS = assets.GalleryVoices

// galleryVoicesDir is where the clips are in galleryVoicesFS.
const galleryVoicesDir = "gallery-voices"

// galleryClip is curated voice id's bundled clip of kind "ref" or
// "preview", or nil.
func galleryClip(id, kind string) []byte {
	name := strings.TrimPrefix(id, gallery.CuratedPrefix)
	if name == id || strings.ContainsAny(name, "/\\") {
		return nil
	}
	file := path.Join(galleryVoicesDir, name+"."+kind+".ogg")
	if kind == "preview" {
		return embeddedClip(galleryVoicesFS, file) // cards ask at every frame
	}
	b, err := fs.ReadFile(galleryVoicesFS, file)
	if err != nil {
		return nil
	}
	return b
}

// galleryRefPath is curated voice arch's bundled reference decoded into
// dir (once per bundled clip: its name has the clip's hash), for models
// that clone it without a saved voice (a dub cast naming it).
func galleryRefPath(dir string, arch gallery.Archetype) (string, error) {
	b := galleryClip(arch.ID, "ref")
	if b == nil {
		return "", fmt.Errorf("no bundled reference for %s", arch.ID)
	}
	sum := sha256.Sum256(b)
	p := filepath.Join(dir, "gallery-"+strings.TrimPrefix(arch.ID, gallery.CuratedPrefix)+"-"+hex.EncodeToString(sum[:6])+".wav")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := decodeBundled(b, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// decodeBundled decodes a bundled Ogg Vorbis clip into a 16-bit WAV at
// dst (mono, its own rate) and says how long it is.
func decodeBundled(ogg []byte, dst string) (float64, error) {
	b, err := decodeVorbis(ogg)
	if err != nil {
		return 0, err
	}
	return b.Seconds(), wav.WriteFile(dst, b, wav.PCM16, wav.Options{})
}

// decodeVorbis decodes an Ogg Vorbis clip, mixed down to mono.
func decodeVorbis(ogg []byte) (dsp.Buffer, error) {
	d, err := vorbis.NewDecoder(bytes.NewReader(ogg))
	if err != nil {
		return dsp.Buffer{}, err
	}
	ch, rate := d.Channels(), d.SampleRate()
	if ch < 1 || rate <= 0 {
		return dsp.Buffer{}, errors.New("vorbis: no channels or sample rate")
	}
	var mono []float32
	buf := make([]float32, 4096/ch*ch)
	for {
		n, err := d.Read(buf)
		n -= n % ch
		for i := 0; i < n; i += ch {
			var s float32
			for c := range ch {
				s += buf[i+c]
			}
			mono = append(mono, s/float32(ch))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return dsp.Buffer{}, err
		}
		if n == 0 {
			return dsp.Buffer{}, io.ErrNoProgress
		}
	}
	return dsp.Buffer{Rate: rate, Channels: 1, Samples: mono}, nil
}
