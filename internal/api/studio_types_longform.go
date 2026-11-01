package api

// The Stories and Audiobook types of the studio API (split from
// studio_types.go).

import "time"

// --- Stories and Audiobook ---------------------------------------------------------------

// StoryLineIn is a line of a story given as data.
type StoryLineIn struct {
	Text       string  `json:"text" jsonschema:"What is said (script markup works: [pause 500ms], tone tags); for a chapter line, its title."`
	Speaker    string  `json:"speaker,omitempty" jsonschema:"The character who says it (a name; the cast gives it a voice); empty: the narrator."`
	Voice      string  `json:"voice,omitempty" jsonschema:"A voice for this line only (a saved voice's name or id, or a built-in speaker)."`
	Speed      float64 `json:"speed,omitempty" jsonschema:"0.5–2; 0: the story's."`
	PauseAfter int     `json:"pause_after_ms,omitempty" jsonschema:"Silence after the line, in milliseconds (0: the usual gap)."`
	Chapter    bool    `json:"chapter,omitempty" jsonschema:"true: the line starts a chapter titled text."`
}

// LongformCreate makes a Stories or Audiobook project (REST body of
// POST /api/audiobooks and /api/stories).
type LongformCreate struct {
	Kind     string            `json:"-"` // "story" or "audiobook"
	Title    string            `json:"title,omitempty"`
	Path     string            `json:"path,omitempty"`
	Text     string            `json:"text,omitempty"`
	Lines    []StoryLineIn     `json:"lines,omitempty"`
	Split    string            `json:"split,omitempty"`
	Model    string            `json:"model,omitempty"`
	Voice    string            `json:"voice,omitempty"`
	Cast     map[string]string `json:"cast,omitempty"`
	Language string            `json:"language,omitempty"`
	Format   string            `json:"format,omitempty"`
}

// AudiobookCreate is create_audiobook's input.
type AudiobookCreate struct {
	Title    string            `json:"title,omitempty" jsonschema:"The book's title; empty: the book's own or the file's."`
	Path     string            `json:"path,omitempty" jsonschema:"Absolute path of the book: EPUB, TXT, Markdown or DOCX (headings become chapters)."`
	Text     string            `json:"text,omitempty" jsonschema:"Or the manuscript itself: '# Title' lines start chapters, [voice:NAME] switches to a character's voice, [dialogue] … [/dialogue] holds 'Name: line' lines the dialogue model speaks together."`
	Model    string            `json:"model,omitempty" jsonschema:"A speech model id (list_models); empty: AudioSmith's default."`
	Voice    string            `json:"voice,omitempty" jsonschema:"The narrator: a saved voice's name or id (list_voices) or a built-in speaker of the model; empty: the first voice that fits."`
	Cast     map[string]string `json:"cast,omitempty" jsonschema:"[voice:NAME] name → voice (name or id, or a built-in speaker)."`
	Language string            `json:"language,omitempty" jsonschema:"BCP-47 code of the text; empty: the book's, else automatic."`
	Format   string            `json:"format,omitempty" jsonschema:"wav (default) or m4b: also make an M4B audiobook with chapters and cover."`
}

// StoryCreate is create_story's input.
type StoryCreate struct {
	Title    string            `json:"title,omitempty" jsonschema:"The story's title; empty: the file's."`
	Path     string            `json:"path,omitempty" jsonschema:"Absolute path of a file to import: TXT, Markdown, EPUB, DOCX, SRT or VTT."`
	Text     string            `json:"text,omitempty" jsonschema:"Or the script itself, one line per line: lines like 'Name: what they say' go to that character, lines without a name to the narrator; '# Title' lines start chapters."`
	Lines    []StoryLineIn     `json:"lines,omitempty" jsonschema:"Or the lines as data, in order."`
	Split    string            `json:"split,omitempty" jsonschema:"How text becomes lines: speakers (default: as Auto-cast does), paragraphs, sentences or chapters."`
	Model    string            `json:"model,omitempty" jsonschema:"A speech model id (list_models); empty: AudioSmith's default."`
	Voice    string            `json:"voice,omitempty" jsonschema:"The narrator (lines without a character): a saved voice's name or id, or a built-in speaker; empty: the first voice that fits."`
	Cast     map[string]string `json:"cast,omitempty" jsonschema:"Character name → voice (name or id, or a built-in speaker); characters left out get voices in turn."`
	Language string            `json:"language,omitempty" jsonschema:"BCP-47 code of the text; empty: automatic."`
	Format   string            `json:"format,omitempty" jsonschema:"wav (default) or m4b."`
}

// Longform is the request of an audiobook's input.
func (in AudiobookCreate) Longform() LongformCreate {
	return LongformCreate{Kind: "audiobook", Title: in.Title, Path: in.Path, Text: in.Text, Model: in.Model, Voice: in.Voice,
		Cast: in.Cast, Language: in.Language, Format: in.Format}
}

// Longform is the request of a story's input.
func (in StoryCreate) Longform() LongformCreate {
	return LongformCreate{Kind: "story", Title: in.Title, Path: in.Path, Text: in.Text, Lines: in.Lines, Split: in.Split,
		Model: in.Model, Voice: in.Voice, Cast: in.Cast, Language: in.Language, Format: in.Format}
}

// ChapterJSON is a chapter of a project.
type ChapterJSON struct {
	Index int    `json:"index"` // 1-based
	Title string `json:"title,omitempty"`
	Lines int    `json:"lines"`
	Chars int    `json:"chars"`
}

// CastJSON is a character and the voice that plays it.
type CastJSON struct {
	Name      string `json:"name"`
	Voice     string `json:"voice,omitempty"` // a reference: a voice id, "speaker:<id>"
	VoiceName string `json:"voice_name,omitempty"`
	Lines     int    `json:"lines,omitempty"`
	Persona   string `json:"persona,omitempty"` // stories: the character's performance baseline
	// Dialogue: an audiobook's name that speaks in a dialogue block.
	Dialogue bool `json:"dialogue,omitempty"`
}

// DialogueBlockJSON is a dialogue block of an audiobook's manuscript
// ([dialogue] … [/dialogue]): where it is, who speaks, and how it is made.
type DialogueBlockJSON struct {
	Chapter  int      `json:"chapter"` // 1-based
	Index    int      `json:"index"`   // 1-based in its chapter (render_longform's dialogue_block)
	Start    int      `json:"start"`   // byte offsets of the block in script
	End      int      `json:"end"`
	Speakers []string `json:"speakers"`
	Lines    int      `json:"lines"`            // lines with a speaker
	Uncast   []string `json:"uncast,omitempty"` // speakers without a voice: render_longform gives them their own
	// Mode is dialogue (the dialogue model speaks it) or per_line (each
	// line in its voice on the book's model), Reason why per_line:
	// dialogue_off, not_downloaded, memory, language or one_speaker.
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// StoryLineJSON is a line of a story.
type StoryLineJSON struct {
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	Speaker    string  `json:"speaker,omitempty"`
	Voice      string  `json:"voice,omitempty"`
	Speed      float64 `json:"speed,omitempty"`
	PauseAfter int     `json:"pause_after_ms,omitempty"`
	Chapter    bool    `json:"chapter,omitempty"`
	Direction  string  `json:"direction,omitempty"` // how the line is said (never spoken)
	Intensity  int     `json:"intensity,omitempty"` // of the direction: 1 restrained, 2 clear, 3 strong; with an emotion 0–3
	Emotion    string  `json:"emotion,omitempty"`   // the AI director's emotion of the line (steady, happy, hurt…)
	Turn       bool    `json:"turn,omitempty"`      // a turn of the scene: its feeling may change at once
}

// ProblemJSON is something that stops (Blocking) or affects a render.
type ProblemJSON struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking,omitempty"`
	Chapter  int    `json:"chapter,omitempty"` // 1-based; 0 = none
	Line     string `json:"line,omitempty"`    // a story line's id
}

// BookJSON are a book's details.
type BookJSON struct {
	Author      string `json:"author,omitempty"`
	Narrator    string `json:"narrator,omitempty"`
	Year        string `json:"year,omitempty"`
	Genre       string `json:"genre,omitempty"`
	Description string `json:"description,omitempty"`
	Cover       string `json:"cover,omitempty"`
	Loudness    string `json:"loudness,omitempty"` // acx, podcast or ""
}

// RenderJSON is a render of a project.
type RenderJSON struct {
	ID      string             `json:"render_id"`
	JobID   string             `json:"job_id"` // "longform:<render_id>"
	State   string             `json:"state"`  // running, done, failed, stopped
	Partial bool               `json:"partial,omitempty"`
	Seconds float64            `json:"seconds,omitempty"`
	Book    string             `json:"book,omitempty"` // the book as WAV
	M4B     string             `json:"m4b,omitempty"`
	Cues    string             `json:"cues,omitempty"`
	Error   string             `json:"error,omitempty"`
	Encode  string             `json:"encode_error,omitempty"`
	Chapter []RenderChapterOut `json:"chapters,omitempty"`
	Stems   map[string]string  `json:"stems,omitempty"` // character ("" = narrator) → WAV
	Created time.Time          `json:"created_at"`
}

// RenderChapterOut is a chapter of a render.
type RenderChapterOut struct {
	Title   string  `json:"title,omitempty"`
	State   string  `json:"state"`
	File    string  `json:"file,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// LongformJSON is a Stories or Audiobook project.
type LongformJSON struct {
	ID        string          `json:"project_id"`
	Kind      string          `json:"kind"` // story, audiobook
	Title     string          `json:"title"`
	Model     string          `json:"model"` // catalogue id
	Voice     string          `json:"voice,omitempty"`
	VoiceName string          `json:"voice_name,omitempty"`
	Language  string          `json:"language,omitempty"`
	Format    string          `json:"format"`
	Stems     bool            `json:"stems,omitempty"`
	Speed     float64         `json:"speed,omitempty"`
	Chapters  []ChapterJSON   `json:"chapters"`
	Cast      []CastJSON      `json:"cast,omitempty"`
	Lines     []StoryLineJSON `json:"lines,omitempty"`
	// Stories: the scene, the pacing (dialogue, reading, relaxed or
	// fixed) and what the model does with scene, personas and directions
	// (continuity: instruct, intensity or lines).
	Scene      string `json:"scene,omitempty"`
	Pacing     string `json:"pacing,omitempty"`
	Continuity string `json:"continuity,omitempty"`
	// Dialogue: the characters' lines are spoken by the dialogue model,
	// a scene at a time (whole dialogue).
	Dialogue      bool   `json:"dialogue,omitempty"`
	DialogueModel string `json:"dialogue_model,omitempty"`
	// Audiobooks: the manuscript (when it is at most 100 KB; longer ones
	// are left out) and its dialogue blocks.
	Script         string              `json:"script,omitempty"`
	DialogueBlocks []DialogueBlockJSON `json:"dialogue_blocks,omitempty"`
	Words          int                 `json:"words"`
	Estimate       float64             `json:"estimated_seconds"`
	Problems       []ProblemJSON       `json:"problems,omitempty"`
	Book           BookJSON            `json:"book"`
	Render         *RenderJSON         `json:"last_render,omitempty"`
	Updated        time.Time           `json:"updated_at"`
	// Warning: the request named a removed model; its replacement is used.
	Warning string `json:"warning,omitempty"`
}

// LongformUpdate changes a project; empty fields stay as they are.
type LongformUpdate struct {
	ID       string            `json:"project_id" jsonschema:"The project (create_audiobook, create_story or list_projects)."`
	Title    string            `json:"title,omitempty" jsonschema:"A new title."`
	Model    string            `json:"model,omitempty" jsonschema:"Another speech model (voices that do not fit it are reported as problems)."`
	Voice    string            `json:"voice,omitempty" jsonschema:"The narrator (default voice): a saved voice's name or id, or a built-in speaker."`
	Cast     map[string]string `json:"cast,omitempty" jsonschema:"Character name → voice; the voice default gives the character the narrator's voice."`
	Lines    map[string]string `json:"line_voices,omitempty" jsonschema:"Stories: line id → voice for that line only (default: back to its character's)."`
	Language string            `json:"language,omitempty" jsonschema:"BCP-47 code; auto: automatic."`
	Speed    float64           `json:"speed,omitempty" jsonschema:"Speaking rate 0.5–2."`
	Format   string            `json:"format,omitempty" jsonschema:"wav or m4b (M4B needs the engine's encoder)."`
	Loudness string            `json:"loudness,omitempty" jsonschema:"The book's loudness target: acx (Audible, −19 LUFS), podcast (−16 LUFS) or off."`
	Author   string            `json:"author,omitempty" jsonschema:"Book details for the M4B's tags."`
	Narrator string            `json:"narrator,omitempty" jsonschema:"The narrator's name, for the M4B's tags."`
	Year     string            `json:"year,omitempty" jsonschema:"The book's year, for its tags."`
	Genre    string            `json:"genre,omitempty" jsonschema:"The book's genre, for its tags."`
	About    string            `json:"description,omitempty" jsonschema:"The book's description, for its tags."`
	Cover    string            `json:"cover,omitempty" jsonschema:"Absolute path of a PNG or JPEG cover (≤ 8 MB)."`
	Stems    *bool             `json:"stems,omitempty" jsonschema:"true: renders also write one WAV per character (stems)."`
	Script   *string           `json:"script,omitempty" jsonschema:"Audiobooks: the whole manuscript, replacing it: '# Title' lines start chapters, [voice:NAME] switches voice, and [dialogue] … [/dialogue] (or [对话] … [/对话]) marks a dialogue block of 'Name: line' lines that the dialogue model speaks together (lines without a name are narration; a line in parentheses is a stage direction, not read)."`
	// Stories' performance.
	Scene         *string           `json:"scene,omitempty" jsonschema:"Stories: where and when, who the characters are to each other, the mood (\"\" clears). Models that take instructions hear it with every line; never spoken."`
	Personas      map[string]string `json:"personas,omitempty" jsonschema:"Stories: character name → persona, its performance baseline (\"seven, lively, coaxing\"; \"\" clears)."`
	Directions    map[string]string `json:"directions,omitempty" jsonschema:"Stories: line id → direction, how that line is said (\"pleading\", \"softly, holding back tears\"; \"\" clears). Never spoken."`
	Emotions      map[string]string `json:"emotions,omitempty" jsonschema:"Stories: line id → its emotion, as the AI director writes it: steady, happy, excited, tender, pleading, hesitant, worried, hurt, sad, angry, scared, surprised or apologetic (\"\" clears). Models that take instructions hear a short instruction made from it and its intensity. Never spoken."`
	Intensities   map[string]int    `json:"intensities,omitempty" jsonschema:"Stories: line id → intensity: of an emotion 0 barely, 1 restrained, 2 clear, 3 strong; of a direction 1–3 (0 clears it)."`
	Pacing        string            `json:"pacing,omitempty" jsonschema:"Stories: gaps between lines: dialogue (tight turns), reading, relaxed, or fixed (one gap for every line)."`
	Merge         *bool             `json:"merge_turns,omitempty" jsonschema:"Stories, with a pacing: say consecutive lines of one character in one go (default true)."`
	Level         *bool             `json:"even_out_voices,omitempty" jsonschema:"Stories, with a pacing: bring each character's lines toward one loudness (default true)."`
	Dialogue      *bool             `json:"dialogue,omitempty" jsonschema:"Stories: true speaks the characters' lines with the dialogue model (dialogue_model; Chinese and English) a scene at a time, each line hearing the lines before it; narration stays on the story's model. Audiobooks: true speaks the [dialogue] blocks with it (on by default once the manuscript has blocks); false reads their lines one by one in their voices."`
	DialogueModel string            `json:"dialogue_model,omitempty" jsonschema:"Stories and audiobooks: the dialogue model: soulx (SoulX-Podcast, the default: a 2.4 GB download, 16 GB of memory, up to four voices) or moss (MOSS-TTSD: an 11 GB download, 24 GB of memory, up to five voices, a scene as one recording)."`
	Direct        string            `json:"direct,omitempty" jsonschema:"Stories: run the AI director (the writing-help language model reads the story and plans its emotions: a baseline per character, an emotion, intensity and turn per line) after the other changes: fill (only what is empty) or all (replace what you did not set). Takes up to a minute or two."`
}

// LongformRender starts a render.
type LongformRender struct {
	ID       string `json:"project_id" jsonschema:"The project to render."`
	Chapters []int  `json:"chapters,omitempty" jsonschema:"Chapter numbers (1-based) to render; empty: the whole book or story."`
	// DialogueBlock previews one dialogue block of an audiobook.
	DialogueBlock *DialogueBlockRef `json:"dialogue_block,omitempty" jsonschema:"Audiobooks: preview one dialogue block only (get_longform's dialogue_blocks: chapter and index), without the book or its loudness target."`
}

// DialogueBlockRef names a dialogue block: its chapter (1-based) and its
// index in the chapter (1-based).
type DialogueBlockRef struct {
	Chapter int `json:"chapter" jsonschema:"The block's chapter (1-based)."`
	Index   int `json:"index" jsonschema:"The block's index in its chapter (1-based)."`
}

// LongformExport saves a finished render.
type LongformExport struct {
	ID       string `json:"project_id" jsonschema:"The project; its last render is exported."`
	Render   string `json:"render_id,omitempty" jsonschema:"Or a render of it (render_longform's job, without longform:)."`
	Format   string `json:"format,omitempty" jsonschema:"m4b (rendered with format m4b), wav (default), mp3 or opus (need ffmpeg), chapters (one WAV per chapter), cues (chapter times, .txt) or stems (one WAV per character)."`
	Bits     int    `json:"bits,omitempty" jsonschema:"WAV bit depth: 16, 24 or 32 (float); 0: as rendered (32-bit float)."`
	Dir      string `json:"directory,omitempty" jsonschema:"Absolute folder to write to; empty: AudioSmith's agents' folder."`
	Filename string `json:"filename,omitempty" jsonschema:"The file's name (no folders; for chapters and stems, the name of a folder made inside directory); empty: from the title."`
}

// ExportJSON is what an export wrote.
type ExportJSON struct {
	Files    []string `json:"files"`
	Format   string   `json:"format"`
	Bytes    int64    `json:"bytes,omitempty"`
	Seconds  float64  `json:"seconds,omitempty"`
	Loudness float64  `json:"loudness_lufs,omitempty"`
}
