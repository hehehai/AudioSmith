package services

// The book: a "longform.assemble" job on the cpu lane, after every chapter
// job of a render, joins the chapter WAVs into the book (float WAV, the
// intermediate the encoder reads), writes the cue sheet, a stem per
// character (the book's timeline with only that character's lines, for
// mixing elsewhere) and the loudness report, then — for M4B — asks the
// engine's media.encode (AVFoundation: AAC, chapters, cover, metadata) for
// the audiobook file.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/hehehai/AudioSmith/internal/doc"
	"github.com/hehehai/AudioSmith/internal/dsp/wav"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
)

// KindEncode is the engine's media.encode (proposed for the engine
// thread; see EncodeParams). The app offers M4B only when the engine has
// it (Config.Encode returns an executor).
const KindEncode = "media.encode"

// EncodeParams are media.encode's params (media worker, a job): one audio
// file (WAV) encoded into Output, with chapters, metadata and a cover.
type EncodeParams struct {
	Input  string       `json:"input"`  // WAV (any bit depth, RF64 allowed)
	Output proto.Output `json:"output"` // path + format: "m4b", "m4a" (AAC); later "mp3"
	Codec  string       `json:"codec,omitempty"`
	// Bitrate in bits per second; 0 = the encoder's choice for speech
	// (64 kb/s mono, 96 kb/s stereo AAC).
	Bitrate    int             `json:"bitrate,omitempty"`
	SampleRate int             `json:"sampleRate,omitempty"` // 0 = the input's
	Chapters   []EncodeChapter `json:"chapters,omitempty"`
	Metadata   EncodeMetadata  `json:"metadata"`
	Cover      string          `json:"cover,omitempty"` // PNG or JPEG
}

// EncodeChapter is a chapter mark (seconds into the input).
type EncodeChapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// EncodeMetadata are the file's tags (iTunes atoms in M4B: ©nam, ©ART,
// aART, ©alb, ©wrt for the narrator, ©day, ©gen, desc/©cmt).
type EncodeMetadata struct {
	Title       string `json:"title,omitempty"`
	Author      string `json:"author,omitempty"`
	Narrator    string `json:"narrator,omitempty"`
	Year        string `json:"year,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Description string `json:"description,omitempty"`
}

// EncodeResult is media.encode's result.
type EncodeResult struct {
	Output   string  `json:"output"`
	Format   string  `json:"format"`
	Bytes    int64   `json:"bytes"`
	Duration float64 `json:"duration"`
	Bitrate  int     `json:"bitrate,omitempty"`
}

// AssembleParams are a longform.assemble job's params.
type AssembleParams struct {
	Render string `json:"render"`
	Run    int    `json:"run,omitempty"` // RenderBody.Runs when submitted
}

// AssembleResult is what the book is made of.
type AssembleResult struct {
	Book     string        `json:"book"` // float WAV
	Seconds  float64       `json:"seconds"`
	Bytes    int64         `json:"bytes"`
	Chapters []BookChapter `json:"chapters"`
	Cues     string        `json:"cues"`   // the cue sheet (.txt)
	Report   string        `json:"report"` // loudness report (.json)
	// Loudness is the book's own; each chapter has its report in its sidecar.
	Loudness    LoudnessReport `json:"loudness"`
	Stems       []Stem         `json:"stems,omitempty"`
	Encoded     string         `json:"encoded,omitempty"` // the M4B
	EncodeError string         `json:"encodeError,omitempty"`
}

// BookChapter is a chapter of the book.
type BookChapter struct {
	Title   string         `json:"title"`
	File    string         `json:"file"`
	Start   float64        `json:"start"`
	Seconds float64        `json:"seconds"`
	Report  LoudnessReport `json:"report"`
}

// Stem is one character's stem.
type Stem struct {
	Character string `json:"character"` // "" = the narrator
	File      string `json:"file"`
}

// BookReport is the report.json written next to the book.
type BookReport struct {
	Title    string         `json:"title"`
	Target   string         `json:"target,omitempty"`
	Book     LoudnessReport `json:"book"`
	Chapters []BookChapter  `json:"chapters"`
	Made     time.Time      `json:"made"`
}

// assembler runs longform.assemble jobs.
type assembler struct{ s *Longform }

func (a assembler) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p AssembleParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "params: " + err.Error()}
	}
	rec, body, err := a.s.load(ctx, p.Render)
	if err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	spec := body.Spec
	chs := make([]ChapterResult, len(spec.Chapters))
	var total int64
	for i := range spec.Chapters {
		r, err := ReadChapter(chapterPath(body.Dir, i))
		if err != nil {
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("chapter %d is missing: %v", i+1, err), Retryable: false}
		}
		chs[i] = r
	}
	if len(chs) == 0 {
		return nil, &jobs.Error{Code: CodeNothingToSay, Message: "The book has no chapters."}
	}
	// Chapters of other rates (a 48 kHz model's narration beside a 24 kHz
	// dialogue model's chapter, thread #165) join at the highest.
	if err := matchChapterFormats(ctx, chs); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	for _, r := range chs {
		total += r.Frames
	}
	rate, channels := chs[0].SampleRate, chs[0].Channels
	res := AssembleResult{Book: filepath.Join(body.Dir, "book.wav"), Cues: filepath.Join(body.Dir, "cues.txt"),
		Report: filepath.Join(body.Dir, "report.json")}
	book, err := wav.Create(res.Book, wav.Format{Rate: rate, Channels: channels, Encoding: wav.Float32}, wav.Options{Large: true})
	if err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	stems, err := a.openStems(&spec, chs, body.Dir, rate, channels)
	if err != nil {
		book.Abort()
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	abort := func() {
		book.Abort()
		for _, s := range stems {
			s.w.Abort()
		}
	}
	v := newVerifier(rate, channels)
	var done int64
	var rendered []doc.RenderedChapter
	for i, c := range chs {
		ranges := stemRanges(c, stems, rate)
		at := int64(0) // frame within the chapter
		start := float64(done) / float64(rate)
		err := eachBlock(ctx, c.Output, func(s []float32) error {
			v.Write(s)
			if err := book.Write(s); err != nil {
				return err
			}
			n := int64(len(s) / channels)
			for si := range stems {
				stems[si].route(s, at, ranges[si], channels)
			}
			at += n
			done += n
			report(jobs.Progress{Stage: "assembling", Fraction: float64(done) / float64(max(total, 1)), AudioSeconds: float64(done) / float64(rate)})
			return nil
		})
		if err != nil {
			abort()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: fmt.Sprintf("chapter %d: %v", i+1, err)}
		}
		secs := float64(at) / float64(rate)
		res.Chapters = append(res.Chapters, BookChapter{Title: spec.Chapters[i].Title, File: c.Output, Start: start, Seconds: secs, Report: c.Report})
		rendered = append(rendered, doc.RenderedChapter{Title: spec.Chapters[i].Title, Duration: time.Duration(secs * float64(time.Second)), OK: true})
	}
	if err := book.Close(); err != nil {
		abort()
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	for _, s := range stems {
		if s.err != nil {
			abort()
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: s.err.Error()}
		}
	}
	for _, s := range stems {
		if err := s.w.Close(); err != nil {
			return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
		}
		res.Stems = append(res.Stems, Stem{Character: s.character, File: s.path})
	}
	res.Seconds = float64(done) / float64(rate)
	if fi, err := os.Stat(res.Book); err == nil {
		res.Bytes = fi.Size()
	}
	res.Loudness = v.Report(loudnessTarget(spec.Book.Loudness))
	cues := doc.CueSheet(doc.MarksFromDurations(rendered), Untitled)
	if err := writeFileAtomic(res.Cues, []byte(cues)); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}
	rep, _ := json.MarshalIndent(BookReport{Title: rec.Title, Target: spec.Book.Loudness, Book: res.Loudness, Chapters: res.Chapters, Made: time.Now()}, "", "  ")
	if err := writeFileAtomic(res.Report, rep); err != nil {
		return nil, &jobs.Error{Code: CodeRenderFailed, Message: err.Error()}
	}

	a.s.render.pruneLongformChunks()
	if spec.Format == FormatM4B {
		res.Encoded, res.EncodeError = a.encode(ctx, job, &spec, rec.Title, &res, report)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return json.Marshal(res)
}

// encode makes the M4B through the engine; a failure leaves the WAV book
// and is reported, not fatal.
func (a assembler) encode(ctx context.Context, job jobs.Job, spec *LongformSpec, title string, res *AssembleResult, report func(jobs.Progress)) (string, string) {
	var ex jobs.Executor
	if a.s.encode != nil {
		ex = a.s.encode()
	}
	if ex == nil {
		return "", "The engine cannot encode M4B yet: the book is a WAV file."
	}
	out := filepath.Join(filepath.Dir(res.Book), SafeFileName(title, "audiobook")+".m4b")
	p := EncodeParams{Input: res.Book, Output: proto.Output{Path: out, Format: FormatM4B}, Codec: "aac", Cover: spec.Book.Cover,
		Metadata: EncodeMetadata{Title: title, Author: spec.Book.Author, Narrator: spec.Book.Narrator, Year: spec.Book.Year,
			Genre: spec.Book.Genre, Description: spec.Book.Description}}
	for i, c := range res.Chapters {
		t := c.Title
		if t == "" {
			t = Untitled(i + 1)
		}
		p.Chapters = append(p.Chapters, EncodeChapter{Title: t, Start: math.Round(c.Start*1000) / 1000, End: math.Round((c.Start+c.Seconds)*1000) / 1000})
	}
	params, _ := json.Marshal(p)
	sub := jobs.Job{ID: job.ID + ".encode", Spec: jobs.Spec{Kind: KindEncode, Lane: jobs.LaneMedia, Params: params, Owner: job.Owner}}
	raw, err := ex.Run(ctx, sub, func(pr jobs.Progress) {
		pr.Stage = "encoding"
		report(pr)
	}, nil)
	if err != nil {
		return "", err.Error()
	}
	var r EncodeResult
	json.Unmarshal(raw, &r)
	if r.Output == "" {
		r.Output = out
	}
	return r.Output, ""
}

// stemWriter is one character's stem.
type stemWriter struct {
	character string
	path      string
	w         *wav.FileWriter
	buf       []float32
	err       error
}

// frameRange is a span of a chapter (frames) a stem keeps.
type frameRange struct{ start, end int64 }

// openStems opens a stem per character of the book (when asked).
func (a assembler) openStems(spec *LongformSpec, chs []ChapterResult, dir string, rate, channels int) ([]*stemWriter, error) {
	if !spec.Stems || spec.Audition {
		return nil, nil
	}
	var names []string
	seen := map[string]bool{}
	for _, c := range chs {
		for _, l := range c.Lines {
			for _, sp := range l.Spans {
				if !seen[sp.Character] {
					seen[sp.Character] = true
					names = append(names, sp.Character)
				}
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "stems"), 0o755); err != nil {
		return nil, err
	}
	var out []*stemWriter
	for i, n := range names {
		label := n
		if label == "" {
			label = "Narrator"
		}
		path := filepath.Join(dir, "stems", fmt.Sprintf("%02d-%s.wav", i+1, SafeFileName(label, "voice")))
		w, err := wav.Create(path, wav.Format{Rate: rate, Channels: channels, Encoding: wav.PCM16}, wav.Options{Large: true})
		if err != nil {
			for _, s := range out {
				s.w.Abort()
			}
			return nil, err
		}
		out = append(out, &stemWriter{character: n, path: path, w: w})
	}
	return out, nil
}

// stemRanges are the frames of chapter c each stem keeps.
func stemRanges(c ChapterResult, stems []*stemWriter, rate int) [][]frameRange {
	out := make([][]frameRange, len(stems))
	for si, s := range stems {
		for _, l := range c.Lines {
			for _, sp := range l.Spans {
				if sp.Character != s.character || sp.End <= sp.Start {
					continue
				}
				out[si] = append(out[si], frameRange{int64(math.Round(sp.Start * float64(rate))), int64(math.Round(sp.End * float64(rate)))})
			}
		}
	}
	return out
}

// route writes block s (starting at chapter frame at) to the stem: the
// samples inside its ranges, silence elsewhere.
func (w *stemWriter) route(s []float32, at int64, ranges []frameRange, channels int) {
	if cap(w.buf) < len(s) {
		w.buf = make([]float32, len(s))
	}
	w.buf = w.buf[:len(s)]
	clear(w.buf)
	n := int64(len(s) / channels)
	for _, r := range ranges {
		a, b := max(r.start, at), min(r.end, at+n)
		if a >= b {
			continue
		}
		copy(w.buf[(a-at)*int64(channels):(b-at)*int64(channels)], s[(a-at)*int64(channels):(b-at)*int64(channels)])
	}
	if w.err == nil {
		w.err = w.w.Write(w.buf)
	}
}

// chapterPath is the WAV of chapter i (0-based) of a render.
func chapterPath(dir string, i int) string {
	return filepath.Join(dir, "chapters", fmt.Sprintf("%03d.wav", i+1))
}

// SafeFileName makes s usable as a file name: path separators and
// control characters dropped, at most 80 characters; fallback when empty.
func SafeFileName(s, fallback string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			return '-'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	s = strings.Trim(s, ". ")
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimSpace(string(r[:80]))
	}
	if s == "" {
		return fallback
	}
	return s
}
