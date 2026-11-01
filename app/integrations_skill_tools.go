package main

// The agent skill's catalogue of tools and their arguments; split from
// integrations_skill.go.

import (
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// skillArg is one or more arguments of a tool and what to know of them.
type skillArg struct {
	Names    []string
	Text     string
	Required bool
}

// skillTool is a tool in the skill: what it is for, its arguments, and
// what it returns.
type skillTool struct {
	Name, Lead string
	Args       []skillArg
	After      string
}

// skillTools are the MCP tools as the skill explains them; their
// arguments are all of the schema's (TestAgentSkill).
func skillTools() []skillTool {
	arg := func(text string, names ...string) skillArg { return skillArg{Names: names, Text: text} }
	must := func(text, name string) skillArg { return skillArg{Names: []string{name}, Text: text, Required: true} }
	return append([]skillTool{
		{Name: "generate_speech", Lead: i18n.T("text to speech, as a WAV (only to hear a voice, preview_voice is instant)."), Args: []skillArg{
			must(i18n.T("What to say. Long text is fine (up to 100,000 characters): AudioSmith splits it into sentences and joins the take; for a whole book, one call per chapter or section keeps each call short. %s inserts a pause.", "`[pause 500ms]`"), "text"),
			arg(i18n.T("A saved voice's name or id (list_voices), a built-in speaker of the model (list_models → voices) or an OpenAI name (alloy, nova…). Empty: your own voice, else the model's default."), "voice"),
			arg(i18n.T("A model id from list_models. Empty: AudioSmith's default, or a model that can speak the voice. Models marked needs_reference speak cloned voices only. A removed model's id is answered by its replacement (replaced_by in the result)."), "model"),
			arg(i18n.T("BCP-47 code (en, zh, ja…). Empty: the voice's or the model's."), "language"),
			arg(i18n.T("How to say it, on models that take it (\"calm and slow, with a smile\"); on a voice-design model without a voice, the voice to design."), "instructions"),
			arg(i18n.T("Speaking rate, 0.5–2 (1 = normal)."), "speed"),
			arg(i18n.T("The same seed gives the same take again."), "seed"),
			arg(i18n.T("true evens out the volume."), "normalize"),
			arg(i18n.T("%s only: target length in seconds; diffusion steps (8 draft, 32 quality).", skillModel(idOmniVoice)), "duration", "steps"),
			arg(i18n.T("%s writes a WAV to the agents' folder and returns its path in %s; %s returns the audio in the result; %s does both. Empty: AudioSmith's setting. Pass %s: inline audio comes into your context as base64.", "`file`", "`file`", "`inline`", "`both`", "`output: \"file\"`"), "output"),
			arg(i18n.T("The WAV's name in file mode (no folders; .wav is added). A file of that name is replaced."), "filename"),
		}, After: i18n.T("Returns take_id, model, voice, audio_seconds and file. Every take is also in AudioSmith's History.")},
		{Name: "transcribe", Lead: i18n.T("speech in an audio or video file to text."), Args: []skillArg{
			arg(i18n.T("Absolute path of an audio or video file on this Mac (~/ works; wav, mp3, m4a, mp4, mov…)."), "path"),
			arg(i18n.T("Or the audio itself as base64, with its file name for the format."), "audio_base64", "filename"),
			arg(i18n.T("BCP-47 code; empty: detected."), "language"),
			arg(i18n.T("Names and terms to expect, for their spelling."), "prompt"),
			arg(i18n.T("A speech-to-text model (list_models with task stt); empty: AudioSmith's default for the language: %s (Chinese, English and 28 more), else %s. A removed model's id is answered by its replacement (replaced_by in the result).",
				skillModel(catalog.DefaultSTT), skillModel(catalog.FallbackSTT)), "model"),
			arg(i18n.T("true returns segments with start and end in seconds (and words): use them for subtitles."), "timestamps"),
			arg(i18n.T("true tells who says what with %s (slower; download it first): each segment gets a speaker (S1, S2…, and speaker_name when the user named them) and text reads as one paragraph per turn (%s). For meetings, interviews, dialogue.",
				skillModel(catalog.DefaultSpeakers), "`S1: …`"), "speakers"),
			arg(i18n.T("accurate (default) or fast: Transcribe's two modes, whose models Settings → Transcribe names; ignored when model is given."), "mode"),
		}, After: i18n.T("Returns text, language, duration and transcript_id. The transcript is also kept on AudioSmith's Transcribe page.")},
		{Name: "clone_voice", Lead: i18n.T("a new saved voice from a recording."), Args: []skillArg{
			must(i18n.T("The new voice's name: one line, up to 80 characters (AudioSmith cleans and cuts it)."), "name"),
			arg(i18n.T("Absolute path of the recording: 5–15 s of clean speech by one person, without music or noise."), "path"),
			arg(i18n.T("Or the recording as base64, with its file name."), "audio_base64", "filename"),
			arg(i18n.T("What the recording says; empty: the engine listens."), "ref_text"),
			arg(i18n.T("The speaker's language (BCP-47); a delivery hint kept with the voice (calm, whisper…)."), "language", "style"),
			must(i18n.T("true only after the user has confirmed that the speaker agreed to being cloned; without it the tool refuses."), "consent"),
		}, After: i18n.T("Then speak with it: generate_speech with its name as voice. Only on this Mac or with the API key: over the LAN with the PIN alone it answers unauthorized.")},
		{Name: "design_voice", Lead: i18n.T("a new saved voice from a description."), Args: []skillArg{
			must(i18n.T("The voice in plain words: gender, age, pitch, accent or dialect, style (\"a warm older British man\", \"女，青年，四川话\")."), "description"),
			arg(i18n.T("Empty: made from the description."), "name"),
			arg(i18n.T("The language it will speak."), "language"),
			arg(i18n.T("Its identity: another seed gives another voice from the same description."), "seed"),
			arg(i18n.T("A model that designs voices (list_models); empty: the first."), "model"),
		}, After: i18n.T("Returns the voice and the words it could not map (unmatched). Only on this Mac or with the API key, like clone_voice.")},
		{Name: "describe_voice", Lead: i18n.T("how AudioSmith reads a voice description, without saving anything."), Args: []skillArg{
			must(i18n.T("Use it to refine a description before design_voice: rephrase the words it cannot map."), "description"),
		}},
		{Name: "list_voices", Lead: i18n.T("the saved voices (id, name, kind, language, style), your_voice and the OpenAI names."), Args: []skillArg{
			arg(i18n.T("Filter by name, style, description or tags."), "search"),
		}},
		{Name: "list_models", Lead: i18n.T("the models: id, display_name and ui_name (the friendly name the user sees in AudioSmith: say it to the user, pass the id), description, tags, task, capabilities, built-in voices, downloaded, licence, licence_to_accept, needs_reference."), Args: []skillArg{
			arg(i18n.T("tts or stt; empty: both."), "task"),
		}},
		{Name: "list_languages", Lead: i18n.T("the languages a model speaks."), Args: []skillArg{
			arg(i18n.T("A model id; empty: every speech model (models that speak more than 100 languages are named once, with their count)."), "model"),
			arg(i18n.T("Only languages whose code or name contains this (zh, Chinese, 中文…)."), "query"),
		}},
		{Name: "check_health", Lead: i18n.T("whether AudioSmith's engine is ready, starting or unavailable. No arguments.")},
		// Thread #35: the app's modules.
		{Name: "search_voices", Lead: i18n.T("search Discover voices: about 35 curated voices, ready to speak, with the page's filters."), Args: []skillArg{
			arg(i18n.T("Words that must all occur in a voice's name, description, persona or tags (English or Chinese)."), "query"),
			arg(i18n.T("The page's filters; the answer's facets list each filter's values with how many voices they give. Use cases (category): narration, conversational, characters, announcing."), "category", "gender", "age", "language"),
			arg(i18n.T("One voice by its id (the other filters are ignored); ids of the earlier, larger gallery still resolve."), "id"),
			arg(i18n.T("Paging: at most limit voices (20 by default, up to 100) after offset."), "limit", "offset"),
		}, After: i18n.T("Returns voices (id, name, description, kind, uses, gender, age, languages, persona or model and speaker, sample_text, saved_voice_id when it was used already) and facets.")},
		{Name: "use_gallery_voice", Lead: i18n.T("save a voice of Discover voices to My voices, as its Use voice button does (once)."), Args: []skillArg{
			must(i18n.T("The voice's id from search_voices."), "id"),
		}, After: i18n.T("Returns the saved voice, the model it was designed for, the models that can speak it, and whether that model is downloaded. Then use its name as voice anywhere.")},
		{Name: "export_transcript", Lead: i18n.T("save a transcript as a file."), Args: []skillArg{
			must(i18n.T("The transcript's id (transcribe returns it)."), "transcript_id"),
			arg(i18n.T("txt (default), srt, vtt or json."), "format"),
			arg(i18n.T("An absolute folder to write to (made if missing); empty: the agents' folder. The file's name, without folders; empty: from the transcript's title."), "directory", "filename"),
		}, After: i18n.T("Returns files: the path written.")},
		{Name: "create_audiobook", Lead: i18n.T("an Audiobook project from a book or a manuscript."), Args: []skillArg{
			arg(i18n.T("Absolute path of the book: EPUB, TXT, Markdown or DOCX; its headings become chapters, its title, author and language are kept."), "path"),
			arg(i18n.T("Or the manuscript itself: %s lines start chapters, %s switches to a character's voice until the next switch or chapter, and %s … %s holds a dialogue block of %s lines (see update_longform's script).", "`# Title`", "`[voice:NAME]`", "`[dialogue]`", "`[/dialogue]`", "`Name: line`"), "text"),
			arg(i18n.T("A speech model id (list_models); empty: AudioSmith's default."), "model"),
			arg(i18n.T("The narrator: a saved voice's name or id, or a built-in speaker of the model; empty: the first voice that fits the model."), "voice"),
			arg(i18n.T("The voices of the [voice:NAME] names and the dialogue blocks' speakers: an object NAME → voice."), "cast"),
			arg(i18n.T("The title; empty: the book's. The text's language (BCP-47); empty: the book's, else automatic."), "title", "language"),
			arg(i18n.T("wav (default), or m4b to make an M4B audiobook with chapters and cover as well."), "format"),
		}, After: i18n.T("Returns the project: project_id, chapters, cast, problems (blocking ones stop a render) and the estimated length. It is saved in Projects and opens on the Audiobook page.")},
		{Name: "create_story", Lead: i18n.T("a Stories project (several voices) from a script, a file or lines."), Args: []skillArg{
			arg(i18n.T("The script: lines like %s go to that character, as Auto-cast does; %s lines start chapters.", "`Alice: Good morning.`", "`# Title`"), "text"),
			arg(i18n.T("Or a file to import (TXT, Markdown, EPUB, DOCX, SRT, VTT)."), "path"),
			arg(i18n.T("Or the lines as data: objects with text, speaker, voice, speed, pause_after_ms, chapter."), "lines"),
			arg(i18n.T("How text becomes lines: speakers (default), paragraphs, sentences or chapters."), "split"),
			arg(i18n.T("Character name → voice; characters left out get voices in turn, the ones nobody plays first."), "cast"),
			arg(i18n.T("The narrator (lines without a character), the model, the title, the language and the format, as for create_audiobook."), "voice", "model", "title", "language", "format"),
		}, After: i18n.T("Returns the project with its lines (ids), cast and problems.")},
		{Name: "get_longform", Lead: i18n.T("a story or audiobook project: chapters, cast and voices, problems, the last render and its files; an audiobook's script (up to 100 KB) and its dialogue_blocks (chapter, index, speakers, lines, uncast, mode dialogue or per_line and why)."), Args: []skillArg{
			must(i18n.T("The project (create_audiobook, create_story, or list_projects with category story or audiobook)."), "project_id"),
		}},
		{Name: "update_longform", Lead: i18n.T("set a story's or audiobook's voices and settings; what you leave out stays."), Args: []skillArg{
			must(i18n.T("The project."), "project_id"),
			arg(i18n.T("The narrator (default voice): a saved voice's name or id, or a built-in speaker."), "voice"),
			arg(i18n.T("Character → voice; %s gives the character the narrator's voice again.", "`default`"), "cast"),
			arg(i18n.T("Stories: line id → a voice for that line only (%s: back to its character's).", "`default`"), "line_voices"),
			arg(i18n.T("Another speech model; voices that do not fit it show as problems."), "model"),
			arg(i18n.T("wav or m4b (M4B: chapters, cover and tags; needs the engine's encoder)."), "format"),
			arg(i18n.T("The book's loudness target: acx (Audible, −19 LUFS), podcast (−16 LUFS) or off."), "loudness"),
			arg(i18n.T("true: renders also write one WAV per character (stems)."), "stems"),
			arg(i18n.T("Title, language (BCP-47, or auto) and speaking rate (0.5–2)."), "title", "language", "speed"),
			arg(i18n.T("Book details for the M4B's tags; cover is the absolute path of a PNG or JPEG (≤ 8 MB)."), "author", "narrator", "year", "genre", "description", "cover"),
			arg(i18n.T("Stories' performance, never spoken: the scene (where, who, the mood), character → persona (%s), line id → emotion (%s: steady, happy, hurt…), line id → its intensity 0–3 and line id → a direction in your own words (%s). Models that take instructions (%s and %s) hear a short instruction made from them; get_longform's continuity says what the model does with them.", "`seven, lively, coaxing`", "`pleading`", "`softly`", skillModel(idCustomVoice), skillModel(idVoiceDesign)), "scene", "personas", "emotions", "intensities", "directions"),
			arg(i18n.T("Stories: the gaps between lines: dialogue (tight turns, the default of new stories), reading, relaxed or fixed (one gap); merge_turns says a character's consecutive lines in one go, even_out_voices levels each character (both default true)."), "pacing", "merge_turns", "even_out_voices"),
			arg(i18n.T("Audiobooks: the whole manuscript, replacing it. A dialogue block is a scene the dialogue model speaks together, each line hearing the ones before it: %s on its own line, then one %s line per turn (%s is narration, a line without a name too; a whole line in parentheses is a stage direction, not read; %s adds a pause), then %s. %s also works. The block's names are the book's cast; nobody cast gets a voice of their own when it renders.", "`[dialogue]`", "`Name: line`", "`Narrator: …`", "`[pause 1s]`", "`[/dialogue]`", "`[对话]` … `[/对话]`"), "script"),
			arg(i18n.T("True speaks the characters' lines (a story's, or an audiobook's dialogue blocks: on by default once it has some) with a dialogue model (Chinese and English) a scene at a time, each line hearing the lines before it; narration stays on the project's model, and a block is read line by line without it. dialogue_model picks it: soulx (%s, the default; 16 GB of memory) or moss (%s; 24 GB, up to five voices, a scene as one recording).", skillModel("soulx-podcast-1.7b-8bit"), skillModel("moss-ttsd-v1.0-8bit")), "dialogue", "dialogue_model"),
			arg(i18n.T("Stories: %s or %s runs the AI director: the writing-help language model reads the story and plans its emotions, a baseline per character and an emotion, intensity and turn per line, mostly steady (only what is empty, or replacing all but what you set). Takes up to a minute or two; %s if no language model is set up.", "`fill`", "`all`", "`llm_unavailable`"), "direct"),
		}},
		{Name: "render_longform", Lead: i18n.T("render a story or audiobook in the background."), Args: []skillArg{
			must(i18n.T("The project; it must have no blocking problems (get_longform)."), "project_id"),
			arg(i18n.T("Chapter numbers (1-based) to render, for a book of those only; empty: everything."), "chapters"),
			arg(i18n.T("Audiobooks: %s previews that dialogue block only (get_longform's dialogue_blocks): a short render to listen to, no book.", "`{chapter, index}`"), "dialogue_block"),
		}, After: i18n.T("Returns a job_id (longform:<render id>) at once. Only one book or story renders at a time (busy). Follow it with get_job; when it succeeds its files are the M4B (if format m4b), the book's WAV and the chapters. Dialogue speakers nobody was cast as get voices of their own, saved in the cast and listed in the job's result as voices_given.")},
		{Name: "export_longform", Lead: i18n.T("save a finished render where the user wants it."), Args: []skillArg{
			must(i18n.T("The project; its last render is exported."), "project_id"),
			arg(i18n.T("Or one of its renders (render_longform's job without longform:)."), "render_id"),
			arg(i18n.T("m4b (rendered with format m4b), wav (default), mp3 or opus (with chapters; need ffmpeg), chapters (one WAV per chapter), cues (chapter times) or stems."), "format"),
			arg(i18n.T("WAV bit depth: 16, 24 or 32; 0: as rendered (32-bit float)."), "bits"),
			arg(i18n.T("An absolute folder (made if missing); empty: the agents' folder. The file's name; for chapters and stems the name of a folder made inside it."), "directory", "filename"),
		}, After: i18n.T("Returns files: the paths written. It is listed in Projects → Exports.")},
		{Name: "create_dub", Lead: i18n.T("dub a video or audio file into other languages: transcribe, translate, speak each line in its speaker's voice, mix."), Args: []skillArg{
			arg(i18n.T("Absolute path of the video or audio (mp4, mov, mkv, webm, mp3, wav, m4a…)."), "path"),
			arg(i18n.T("Or a video link (YouTube, Bilibili…): AudioSmith downloads it with yt-dlp, only when the user turned on Settings → Dubbing → Import from a link (else the error url.no_ytdlp: ask the user, or download the video and give its path)."), "url"),
			arg(i18n.T("BCP-47 codes to dub into, the main one first; empty: AudioSmith's Dubbing settings (by default Chinese, or English for a video in Chinese)."), "target_languages"),
			arg(i18n.T("The language spoken; empty: detected."), "source_language"),
			arg(i18n.T("How far to run now: transcript (stop to review the lines), translation, dub (default: everything) or none."), "through"),
			arg(i18n.T("One voice for every speaker (a saved voice's name or id); empty: each speaker's own voice, cloned from the source."), "voice"),
			arg(i18n.T("The speech model; empty: the dubbing default. How many people speak (0: detected). Keep music and effects (default true). A title (default: the file's name)."), "model", "speakers", "keep_background", "title"),
			arg(i18n.T("Multi-speaker dialogue: auto (the default) records the conversations of a Chinese or English track with the dialogue model, each line hearing the one before it (slower; the rest line by line), off line by line; the dialogue model: soulx (the only one for dubs yet)."), "dialogue", "dialogue_model"),
		}, After: i18n.T("Returns the dub (dub_id) and its job_id (dub:<dub_id>). Translation uses the translator of AudioSmith's settings (local or cloud, as the user chose): if none is set up, the error is dub.no_translator.")},
		{Name: "get_dub", Lead: i18n.T("a dub: stages, speakers and their voices, tracks (files), and its lines with their translations."), Args: []skillArg{
			must(i18n.T("The dub."), "dub_id"),
			arg(i18n.T("The target language whose translations to show; empty: the main one."), "language"),
			arg(i18n.T("false leaves the lines out."), "segments"),
		}},
		{Name: "edit_dub", Lead: i18n.T("change a dub's lines and voices (not while it runs)."), Args: []skillArg{
			must(i18n.T("The dub."), "dub_id"),
			arg(i18n.T("Line edits: objects with id and any of text (the source; its translations are made again), translation with language, speaker, voice."), "segments"),
			arg(i18n.T("Speaker label (S1, S2…) or * → voice (a saved voice's name or id; default: cloned from the source); speaker label → display name."), "cast", "names"),
			arg(i18n.T("Multi-speaker dialogue (auto or off) and its model (soulx); a line's no_dialogue: true makes it line by line."), "dialogue", "dialogue_model"),
		}, After: i18n.T("Then run_dub makes only the changed lines again: an edited conversation line alone, after the lines before it. get_dub lists the conversations (dialogue_stretches) and says how each line was made (made_by; flag: listen to it).")},
		{Name: "run_dub", Lead: i18n.T("run a dub's pipeline (again)."), Args: []skillArg{
			must(i18n.T("The dub."), "dub_id"),
			arg(i18n.T("transcript, translation, dub (default) or verify (listen again and flag lines that came out wrong)."), "through"),
			arg(i18n.T("Only these target languages; empty: all."), "languages"),
			arg(i18n.T("Translate every line again (edits too); transcribe the source again (drops the lines and their edits)."), "retranslate", "retranscribe"),
			arg(i18n.T("Only re-render these line ids."), "segments"),
		}, After: i18n.T("Returns the job (dub:<dub_id>); busy while a run goes on.")},
		{Name: "export_dub", Lead: i18n.T("save a dub."), Args: []skillArg{
			must(i18n.T("The dub."), "dub_id"),
			must(i18n.T("mp4 (the video with dubbed audio tracks), wav, m4a or mp3 (a dubbed track; mp3 needs ffmpeg), srt, vtt or ass (subtitles), stems or segments (ZIP), burn (subtitles drawn into the video; needs ffmpeg)."), "format"),
			arg(i18n.T("The language of a track or of subtitles (orig: the source's); empty: the main target."), "language"),
			arg(i18n.T("mp4: its audio tracks (languages and orig; default: the main target and orig), the one players pick, and soft subtitle tracks."), "tracks", "default_track", "subtitles"),
			arg(i18n.T("Subtitles with the translation and the original; WAV bit depth (default 24)."), "dual", "bits"),
			arg(i18n.T("An absolute folder (made if missing); empty: the agents' folder. The file's name; empty: from the title and language."), "directory", "filename"),
		}, After: i18n.T("Returns a job_id; when it succeeds its files hold the path written.")},
		{Name: "batch_dub", Lead: i18n.T("dub several files with the Batch queue: one dub each, at background priority, without review stops."), Args: []skillArg{
			arg(i18n.T("Absolute paths of videos or audio files, or a folder (its files, not its subfolders)."), "paths", "folder"),
			arg(i18n.T("Override the Batch page's settings for these files only (nothing is saved): target languages, source language, one voice, model, keep music, multi-speaker dialogue (auto or off)."), "target_languages", "source_language", "voice", "model", "keep_background", "dialogue"),
		}, After: i18n.T("Returns added (each with its dub_id and job_id) and errors (files it could not take).")},
		{Name: "list_batch", Lead: i18n.T("the Batch queue, its settings and its watch folder (read only: the user sets up watch folders in the app). No arguments.")},
		{Name: "list_projects", Lead: i18n.T("AudioSmith's project library, as on its Projects page, newest first."), Args: []skillArg{
			arg(i18n.T("story, audiobook, dub, workflow, voice, transcript, take, export or call; empty: all."), "category"),
			arg(i18n.T("Words in titles, scripts, names or paths."), "search"),
			arg(i18n.T("Paging: at most limit (50 by default, up to 500) after offset."), "limit", "offset"),
		}, After: i18n.T("Each item says which tool opens it (tool).")},
		{Name: "list_takes", Lead: i18n.T("the History: generated takes with their text, voice, length and WAV file, newest first."), Args: []skillArg{
			arg(i18n.T("Words in the text; only a saved voice's takes; only a model's; only starred ones."), "search", "voice_id", "model", "starred"),
			arg(i18n.T("One take by id."), "take_id"),
			arg(i18n.T("Paging: at most limit (20 by default, up to 200) after offset."), "limit", "offset"),
		}},
		{Name: "export_audio", Lead: i18n.T("export a take as the Export audio dialog does."), Args: []skillArg{
			must(i18n.T("The take (generate_speech's take_id, or list_takes)."), "take_id"),
			arg(i18n.T("wav (default), m4a, mp3, flac or opus; mp3, flac and opus need ffmpeg, m4a the engine."), "format"),
			arg(i18n.T("wav: 16, 24 or 32 (float); flac: 16 or 24. Lossy formats: bitrate_kbps (m4a 128/192/256, mp3 128/192/320, opus 64/96/128)."), "bits", "bitrate_kbps"),
			arg(i18n.T("44100 or 48000 (0: the take's; opus is always 48000); 1 or 2 channels (0: the take's)."), "sample_rate", "channels"),
			arg(i18n.T("Loudness target: podcast (−16 LUFS), speech (−18), broadcast (−23) or off; trim: true cuts leading and trailing silence."), "loudness", "trim"),
			arg(i18n.T("Title and artist tags (not wav)."), "title", "artist"),
			arg(i18n.T("Also save the text (.txt) or subtitles timed to the audio (.srt) beside it."), "script", "subtitles"),
			arg(i18n.T("An absolute folder (made if missing); empty: the agents' folder. The file's name; empty: from the text."), "directory", "filename"),
		}, After: i18n.T("Returns files, seconds, bytes and the loudness reached.")},
		{Name: "delete_item", Lead: i18n.T("delete a take, voice, transcript, story or audiobook project, dub, or isolation."), Args: []skillArg{
			must(i18n.T("take, voice, transcript, project (a story or audiobook), dub or isolation."), "kind"),
			must(i18n.T("Its id."), "id"),
			arg(i18n.T("true only after the user said yes to deleting it; without it nothing is deleted (confirm_required)."), "confirm"),
		}},
		{Name: "list_workflows", Lead: i18n.T("the saved workflows (WorkflowStudio): steps, inputs (scripts or files), export folders, last run. No arguments.")},
		{Name: "run_workflow", Lead: i18n.T("run a saved workflow. Its steps cannot be changed here: the user edits them on the Workflows page."), Args: []skillArg{
			must(i18n.T("The workflow (list_workflows)."), "workflow_id"),
			arg(i18n.T("One-off inputs of this run (the saved ones stay): scripts (each one an item) for workflows that start with scripts, or absolute paths of audio files for those that start with audio."), "scripts", "files"),
			arg(i18n.T("Make every result anew (default: keep the results whose inputs did not change)."), "again"),
		}, After: i18n.T("Returns a job_id (workflow:<id>); its result lists each item's steps and files, and files the final results.")},
		{Name: "get_job", Lead: i18n.T("follow a long job: state, stage, progress (0–1), error code and, when it ends, the absolute paths of the files it made."), Args: []skillArg{
			must(i18n.T("A job_id a tool returned."), "job_id"),
			arg(i18n.T("Wait up to this many seconds (≤ 60) for it to end before answering; it answers as soon as it ends."), "wait_seconds"),
		}},
		{Name: "cancel_job", Lead: i18n.T("stop a job; what was made so far is kept, and a run goes on later from there."), Args: []skillArg{
			must(i18n.T("The job_id."), "job_id"),
		}},
	}, kitSkillTools()...) // thread #135: integrations_skill_kit.go
}
