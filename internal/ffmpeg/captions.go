package ffmpeg

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Captions drawn by AudioSmith, for builds of ffmpeg without libass (the
// pinned one has none): each cue becomes a transparent PNG strip as wide
// as the video, and an ffconcat list times them; Burn overlays that
// stream on the bottom of the picture.

// Cue is a subtitle: a time range and its text (lines separated by \n).
type Cue struct {
	Start, End float64
	Text       string
}

// MacFonts are the fonts captions use on a Mac, in order: a bold Latin
// face, then faces covering Chinese, Japanese and Korean.
var MacFonts = []string{
	"/System/Library/Fonts/Helvetica.ttc",
	"/System/Library/Fonts/PingFang.ttc",
	"/System/Library/Fonts/Hiragino Sans GB.ttc",
	"/System/Library/Fonts/STHeiti Medium.ttc",
	"/System/Library/Fonts/AppleSDGothicNeo.ttc",
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
}

// WinFonts are the fonts captions use on Windows, in order: a bold
// Latin face (Segoe UI), then faces covering Simplified Chinese
// (Microsoft YaHei), Japanese (Yu Gothic / Meiryo), Korean (Malgun
// Gothic), and Arial Bold fallback.
var WinFonts = []string{
	`C:\Windows\Fonts\segoeuib.ttf`,
	`C:\Windows\Fonts\segoeui.ttf`,
	`C:\Windows\Fonts\msyhbd.ttc`,
	`C:\Windows\Fonts\msyh.ttc`,
	`C:\Windows\Fonts\YuGothB.ttc`,
	`C:\Windows\Fonts\meiryob.ttc`,
	`C:\Windows\Fonts\malgunbd.ttf`,
	`C:\Windows\Fonts\arialbd.ttf`,
}

// fontOS controls DefaultFonts (tests may override).
var fontOS = runtime.GOOS

// DefaultFonts returns the platform's default caption font fallback
// chain: WinFonts on Windows (resolving %WINDIR% when set), MacFonts
// elsewhere.
func DefaultFonts() []string {
	if fontOS == "windows" {
		return winFontPaths(os.Getenv("WINDIR"))
	}
	return MacFonts
}

func winFontPaths(windir string) []string {
	if windir == "" || strings.EqualFold(windir, `C:\Windows`) {
		return slices.Clone(WinFonts)
	}
	out := make([]string, len(WinFonts))
	for i, p := range WinFonts {
		if rest, ok := strings.CutPrefix(p, `C:\Windows`); ok {
			out[i] = windir + rest
		} else {
			out[i] = p
		}
	}
	return out
}

// fontSet draws text with the first face that has each character.
type fontSet struct {
	em    fixed.Int26_6 // the advance of a character no font has
	fonts []*sfnt.Font
	faces []font.Face
	buf   sfnt.Buffer
}

func loadFonts(paths []string, size float64) *fontSet {
	fs := &fontSet{em: fixed.Int26_6(size * 64)}
	add := func(f *sfnt.Font) {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err == nil {
			fs.fonts = append(fs.fonts, f)
			fs.faces = append(fs.faces, face)
		}
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if c, err := opentype.ParseCollection(data); err == nil {
			// The first face of a collection is the regular or the
			// family's default; prefer a bold one when it has one.
			best := 0
			for i := 0; i < c.NumFonts(); i++ {
				f, err := c.Font(i)
				if err != nil {
					continue
				}
				if name, err := f.Name(nil, sfnt.NameIDSubfamily); err == nil && strings.Contains(strings.ToLower(name), "bold") {
					best = i
					break
				}
			}
			if f, err := c.Font(best); err == nil {
				add(f)
			}
			continue
		}
		if f, err := opentype.Parse(data); err == nil {
			add(f)
		}
	}
	if f, err := opentype.Parse(gobold.TTF); err == nil {
		add(f)
	}
	return fs
}

// faceFor is the face that draws r (the last one when none has it).
func (fs *fontSet) faceFor(r rune) font.Face {
	for i, f := range fs.fonts {
		if g, err := f.GlyphIndex(&fs.buf, r); err == nil && g != 0 {
			return fs.faces[i]
		}
	}
	return fs.faces[len(fs.faces)-1]
}

// measure is the advance of s.
func (fs *fontSet) measure(s string) fixed.Int26_6 {
	var w fixed.Int26_6
	for _, r := range s {
		if a, ok := fs.faceFor(r).GlyphAdvance(r); ok {
			w += a
		} else {
			w += fs.em
		}
	}
	return w
}

// draw draws s at dot in col.
func (fs *fontSet) draw(dst draw.Image, s string, dot fixed.Point26_6, col color.Color) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(col), Dot: dot}
	for _, r := range s {
		d.Face = fs.faceFor(r)
		d.DrawString(string(r))
	}
}

// wrap breaks text into lines at most maxW wide: at spaces, or between
// any two CJK characters.
func (fs *fontSet) wrap(text string, maxW fixed.Int26_6) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		var tokens []string
		var word strings.Builder
		flush := func() {
			if word.Len() > 0 {
				tokens = append(tokens, word.String())
				word.Reset()
			}
		}
		for _, r := range para {
			switch {
			case unicode.IsSpace(r):
				flush()
				tokens = append(tokens, " ")
			case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) || unicode.IsPunct(r) && r > 0x2E7F:
				flush()
				tokens = append(tokens, string(r))
			default:
				word.WriteRune(r)
			}
		}
		flush()
		line := ""
		for _, tok := range tokens {
			if tok == " " && line == "" {
				continue
			}
			if line != "" && fs.measure(line+tok) > maxW {
				out = append(out, strings.TrimSpace(line))
				line = strings.TrimLeft(tok, " ")
				continue
			}
			line += tok
		}
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// RenderCaptions draws cues for a width × height video of duration
// seconds into dir and returns the ffconcat list and the strips' height
// (they go at the bottom of the picture).
func RenderCaptions(cues []Cue, width, height int, duration float64, fonts []string, dir string) (string, int, error) {
	if width <= 0 || height <= 0 {
		return "", 0, fmt.Errorf("captions: bad video size %d×%d", width, height)
	}
	size := math.Max(14, float64(height)*0.046)
	stripH := int(math.Ceil(size*1.25*3 + float64(height)*0.05))
	stripH += stripH % 2 // even, for yuv420p
	fs := loadFonts(fonts, size)
	cues = slices.Clone(cues)
	slices.SortStableFunc(cues, func(a, b Cue) int {
		switch {
		case a.Start < b.Start:
			return -1
		case a.Start > b.Start:
			return 1
		}
		return 0
	})
	blank := filepath.Join(dir, "blank.png")
	if err := writePNG(blank, image.NewNRGBA(image.Rect(0, 0, width, stripH))); err != nil {
		return "", 0, err
	}
	var list strings.Builder
	list.WriteString("ffconcat version 1.0\n")
	entry := func(file string, d float64) {
		fmt.Fprintf(&list, "file '%s'\nduration %.3f\n", strings.ReplaceAll(file, "'", `'\''`), d)
	}
	at := 0.0
	n := 0
	for _, c := range cues {
		start, end := math.Max(c.Start, at), c.End
		if duration > 0 {
			end = math.Min(end, duration)
		}
		if end-start < 0.04 || strings.TrimSpace(c.Text) == "" {
			continue
		}
		if start > at {
			entry(blank, start-at)
		}
		n++
		file := filepath.Join(dir, fmt.Sprintf("c%05d.png", n))
		if err := writePNG(file, fs.strip(c.Text, width, stripH, size)); err != nil {
			return "", 0, err
		}
		entry(file, end-start)
		at = end
	}
	rest := 1.0
	if duration > at {
		rest = duration - at
	}
	entry(blank, rest)
	fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(blank, "'", `'\''`)) // the demuxer drops the last entry's duration
	path := filepath.Join(dir, "captions.ffconcat")
	return path, stripH, os.WriteFile(path, []byte(list.String()), 0o644)
}

// strip draws one cue: up to three centred lines, white with a dark
// outline, sitting on the strip's bottom margin.
func (fs *fontSet) strip(text string, width, height int, size float64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	lines := fs.wrap(text, fixed.I(int(float64(width)*0.86)))
	if len(lines) > 3 {
		lines = append(lines[:2], strings.Join(lines[2:], " "))
	}
	lineH := size * 1.25
	bottom := float64(height) - size*0.45
	outline := max(1, int(size/14))
	for i, l := range lines {
		y := bottom - float64(len(lines)-1-i)*lineH
		w := fs.measure(l)
		x := fixed.I(width/2) - w/2
		base := fixed.Point26_6{X: x, Y: fixed.Int26_6(y * 64)}
		for dx := -outline; dx <= outline; dx++ {
			for dy := -outline; dy <= outline; dy++ {
				if dx == 0 && dy == 0 {
					continue
				}
				fs.draw(img, l, base.Add(fixed.P(dx, dy)), color.NRGBA{0, 0, 0, 220})
			}
		}
		fs.draw(img, l, base, color.NRGBA{255, 255, 255, 255})
	}
	return img
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
