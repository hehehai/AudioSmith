package services

// A longform chapter: one "longform.chapter" job on the gpu lane renders
// every line of a chapter through the take pipeline's pieces (render*.go):
// each span of a line is planned for the model (markup lowered,
// dictionary plus the book's lexicon, normalisation, chunks), each chunk
// comes from the chunk cache or one tts.synthesize call, and the chunks
// are joined with the production's gaps into the chapter. So a chapter
// whose text did not change costs no synthesis, and an edited line costs
// only its own chunks.
//
// The chapter is then brought to the book's loudness target (two passes:
// the join meters it, the second applies gain and a true-peak limiter) and
// written as a 16-bit WAV with a sidecar JSON (ChapterResult): where each
// line and span landed (stems, the read-along player) and the loudness
// report. A chapter whose sidecar says it was made from the same chunks,
// gaps and target is reused as it is (a resumed render).
//
// Files: longform_render.go (types, the job, the chunk cache),
// longform_program.go (planning the chapter), longform_chapter.go
// (rendering and levelling it).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/engine/proto"
	"github.com/hehehai/AudioSmith/internal/jobs"
	"github.com/hehehai/AudioSmith/internal/text/plan"
	"github.com/hehehai/actnowkit/models"
)

// Job kinds and owner of longform renders.
const (
	KindChapter   = "longform.chapter"  // gpu lane: one chapter
	KindAssemble  = "longform.assemble" // cpu lane: the book, after its chapters
	OwnerLongform = "longform:"         // + the render's id
)

// Silence around a chapter (ACX asks for room tone at both ends) and
// between the spans of a line (a voice switch).
const (
	chapterHead = 0.5
	chapterTail = 1.5
	spanGap     = 0.15
)

// chapterKeyVersion changes when the chapter layout changes, so sidecars
// of older renders stop matching.
const chapterKeyVersion = "audiosmith.chapter.v1"

// ChapterParams are a longform.chapter job's params: everything the
// chapter needs, so it can run again after a restart.
type ChapterParams struct {
	Render     string               `json:"render"`
	Run        int                  `json:"run,omitempty"` // RenderBody.Runs when submitted
	Index      int                  `json:"index"`
	Title      string               `json:"title,omitempty"`
	Model      string               `json:"model"`
	Lang       string               `json:"lang,omitempty"`
	Seed       int64                `json:"seed,omitempty"`
	Voices     map[string]core.Take `json:"voices"`
	Lines      []LineSpec           `json:"lines"`
	Production Production           `json:"production"`
	Lexicon    []LexiconEntry       `json:"lexicon,omitempty"`
	Loudness   string               `json:"loudness,omitempty"`
	Scene      string               `json:"scene,omitempty"` // the story's (instructions)
	// Workflow is "story" or "audiobook" (RenderParams.Workflow).
	Workflow string `json:"workflow,omitempty"`
	// Output is the chapter's WAV (absolute); its sidecar is Output+".json".
	Output string `json:"output"`
}

// ChapterResult is a chapter job's result and its sidecar.
type ChapterResult struct {
	Output       string  `json:"output"`
	Key          string  `json:"key"`
	SampleRate   int     `json:"sampleRate"`
	Channels     int     `json:"channels"`
	Frames       int64   `json:"frames"`
	AudioSeconds float64 `json:"audioSeconds"`
	Seconds      float64 `json:"seconds"` // wall time
	Chunks       int     `json:"chunks"`
	Cached       int     `json:"cached"` // chunks from the cache
	// Reused: the file of an earlier run of this render was kept.
	Reused bool `json:"reused,omitempty"`
	// Turns are the merged turns (lines of one character made as one)
	// split back into their lines; TurnsUnsplit the ones that could not
	// be, made line by line instead.
	Turns        int `json:"turns,omitempty"`
	TurnsUnsplit int `json:"turnsUnsplit,omitempty"`
	// Tracks are the stretches of a character's lines said in one
	// synthesis and cut apart (story_track.go), TrackLines their lines;
	// TracksFailed the ones that could not be cut, made line by line.
	Tracks       int `json:"tracks,omitempty"`
	TrackLines   int `json:"trackLines,omitempty"`
	TracksFailed int `json:"tracksFailed,omitempty"`
	// Dialogues are the whole-dialogue segments (story_dialogue.go),
	// DialogueLines their lines from the dialogue model, DialogueRedone
	// the lines it made twice, DialogueFailed the lines (or segments)
	// made on the story's model instead.
	Dialogues      int `json:"dialogues,omitempty"`
	DialogueLines  int `json:"dialogueLines,omitempty"`
	DialogueRedone int `json:"dialogueRedone,omitempty"`
	DialogueFailed int `json:"dialogueFailed,omitempty"`
	// Levelled counts the lines brought toward their character's loudness.
	Levelled int               `json:"levelled,omitempty"`
	Report   LoudnessReport    `json:"report"`
	Warnings []core.TakeNotice `json:"warnings,omitempty"`
	Lines    []LinePlace       `json:"lines"`
}

// LinePlace is where a line landed in its chapter (seconds).
type LinePlace struct {
	ID    string      `json:"id,omitempty"`
	Start float64     `json:"start"`
	End   float64     `json:"end"`
	Spans []SpanPlace `json:"spans"`
}

// SpanPlace is where a span landed, with what it says (markup) and who.
type SpanPlace struct {
	Character string  `json:"character,omitempty"`
	Text      string  `json:"text"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
}

// Sidecar is a chapter WAV's sidecar file.
func Sidecar(wavPath string) string { return wavPath + ".json" }

// ReadChapter reads a chapter's sidecar.
func ReadChapter(wavPath string) (ChapterResult, error) {
	var r ChapterResult
	data, err := os.ReadFile(Sidecar(wavPath))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(data, &r)
	return r, err
}

// chapterStep is one step of a chapter's program: a silence, or a chunk,
// or a merged turn (a chunk saying several lines, split back into them).
type chapterStep struct {
	silence float64 // seconds; used when chunk is nil
	chunk   *renderChunk
	brk     plan.Break
	line    int
	span    int
	turn    *turnStep
	track   *trackStep // a line of a character's track (story_track.go)
	// dialogue: a line of a whole-dialogue segment (story_dialogue.go).
	dialogue *dialogueStep
}

// turnStep is a merged turn: its lines, their expected lengths, and the
// steps that make them one by one when the turn cannot be split.
type turnStep struct {
	lines    []int
	weights  []float64
	fallback []chapterStep
}

// chapterRenderer runs longform.chapter jobs.
type chapterRenderer struct{ r *renderer }

func (cr chapterRenderer) Run(ctx context.Context, job jobs.Job, report func(jobs.Progress), _ func(json.RawMessage)) (json.RawMessage, error) {
	var p ChapterParams
	if err := json.Unmarshal(job.Params, &p); err != nil {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "params: " + err.Error()}
	}
	if p.Output == "" || !filepath.IsAbs(p.Output) {
		return nil, &jobs.Error{Code: proto.CodeBadRequest, Message: "output: an absolute path is required"}
	}
	r := cr.r
	modelPath, err := r.modelReady(ctx, p.Model)
	if err != nil {
		return nil, err
	}
	info := r.model(p.Model)
	for attempt := 0; ; attempt++ {
		res, err := cr.once(ctx, job, &p, info, modelPath, report)
		var je *jobs.Error
		if attempt < 2 && errors.As(err, &je) && je.Code == proto.CodeInputTooLong {
			// As a take's render: plan again with shorter chunks.
			info.MaxChars = max(info.MaxChars*6/10, 20)
			info.MaxCharsCJK = max(info.MaxCharsCJK*6/10, 8)
			r.logf("chapter %s: the engine wants shorter chunks (%s); planning again at %d characters", job.ID, je.Message, info.MaxChars)
			continue
		}
		return res, err
	}
}

// modelReady checks a model's licence and finds its local snapshot.
func (r *renderer) modelReady(ctx context.Context, model string) (string, error) {
	if r.licences != nil {
		if err := r.licences.Check(ctx, model); err != nil {
			if errors.Is(err, core.ErrLicenceRequired) {
				data, _ := json.Marshal(map[string]string{"model": model})
				return "", &jobs.Error{Code: proto.CodeLicenceRequired, Message: err.Error(), Data: data}
			}
			return "", &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
		}
	}
	if r.modelPath == nil {
		return "", nil
	}
	dir, err := r.modelPath(ctx, model)
	if err != nil {
		if errors.Is(err, core.ErrNotDownloaded) {
			data, _ := json.Marshal(map[string]string{"model": model})
			return "", &jobs.Error{Code: proto.CodeModelNotDownloaded, Message: err.Error(), Data: data}
		}
		return "", &jobs.Error{Code: proto.CodeInternal, Message: err.Error()}
	}
	return dir, nil
}

// longformChunkDir holds the chunks of longform renders: kept apart from
// the Studio's cache, whose size cap would evict a long book's chunks,
// and out of ~/Library/Caches altogether (in the data folder): Resume
// of a stopped or failed book depends on them, and cache cleaners (or
// macOS short of space) empty Caches, which would cost hours of GPU work
// again (family audit A7). Time Machine leaves them out: they can be
// rendered again. Chunks of the old place are moved over once.
func (r *renderer) longformChunkDir() string {
	old := filepath.Join(r.cacheDir, "longform", "chunks")
	if r.dataDir == "" {
		return old
	}
	dir := filepath.Join(r.dataDir, "longform-chunks")
	r.chunksDir.Do(func() {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if _, err := os.Stat(old); err == nil {
				if err := os.Rename(old, dir); err != nil && r.logf != nil {
					r.logf("longform: moving the chunks to %s: %v", dir, err)
				}
			}
		}
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = models.SetBackupExcluded(dir, true)
		}
	})
	return dir
}

// Limits of the longform chunk cache: chunks unused for longformKeep are
// deleted (a cache hit touches a chunk), and the least recently used
// beyond longformCap, or 5 % of the disk when that is less (code review
// 1102 H1: they were kept 60 days, up to 24 GB).
const (
	longformKeep = 14 * 24 * time.Hour
	longformCap  = 24 << 30
)

// longformCapFor is the chunk cache's cap on the disk of dir.
func longformCapFor(dir string) int64 {
	limit := int64(longformCap)
	if size := diskSize(dir); size > 0 {
		limit = min(limit, size/20)
	}
	return limit
}

// pruneLongformChunks keeps the longform chunk cache within its limits
// (after a book is assembled, renders are deleted, and by Sweep).
func (r *renderer) pruneLongformChunks() {
	r.pruneChunksMu.Lock()
	defer r.pruneChunksMu.Unlock()
	type file struct {
		path string
		size int64
		at   time.Time
	}
	var files []file
	var total int64
	entries, _ := os.ReadDir(r.longformChunkDir())
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		path := filepath.Join(r.longformChunkDir(), e.Name())
		if !strings.HasSuffix(e.Name(), ".wav") {
			// A dialogue line's sidecars (.codes, .fail, .segfail) go with age.
			if time.Since(fi.ModTime()) > longformKeep && dialogueSidecar(e.Name()) {
				os.Remove(path)
			}
			continue
		}
		if time.Since(fi.ModTime()) > longformKeep {
			os.Remove(path)
			continue
		}
		files = append(files, file{path, fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	limit := longformCapFor(r.longformChunkDir())
	if total <= limit {
		return
	}
	slices.SortFunc(files, func(a, b file) int { return a.at.Compare(b.at) })
	for _, f := range files {
		if total <= limit*9/10 {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			os.Remove(f.path + ".codes")
		}
	}
}

// dialogueSidecar reports whether a cache file is a dialogue line's
// sidecar (dialogue_run.go).
func dialogueSidecar(name string) bool {
	return strings.HasSuffix(name, ".wav.codes") || strings.HasSuffix(name, ".wav.fail") || strings.HasSuffix(name, ".wav.segfail")
}
