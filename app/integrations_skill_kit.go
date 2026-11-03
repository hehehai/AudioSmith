package main

// The agent skill's entries for the toolkit's tools (thread #135: the
// Voice isolator, Assets and samples, voice management, design
// candidates, convert, the story draft, models and the small tools);
// split from integrations_skill_tools.go.

import "github.com/hehehai/AudioSmith/internal/i18n"

// kitSkillTools are the toolkit's tools as the skill explains them.
func kitSkillTools() []skillTool {
	arg := func(text string, names ...string) skillArg { return skillArg{Names: names, Text: text} }
	must := func(text, name string) skillArg { return skillArg{Names: []string{name}, Text: text, Required: true} }
	output := arg(i18n.T("inline returns the audio in the result, file a path in the agents' folder, both both; empty: AudioSmith's setting."), "output")
	return []skillTool{
		// Voices.
		{Name: "design_voice_candidates", Lead: i18n.T("Design's candidates: up to three short takes of a described voice, each with its own seed, to hear before saving one. Nothing is saved."), Args: []skillArg{
			must(i18n.T("The voice in plain words (gender, age, pitch, accent, style), as for design_voice."), "description"),
			arg(i18n.T("The line each candidate says; empty: a short greeting in the voice's language."), "text"),
			arg(i18n.T("Its language (BCP-47); a model that designs voices (empty: the first); how many candidates, 1–3 (default 3)."), "language", "model", "count"),
			output,
		}, After: i18n.T("Returns candidates (seed, take_id, file). Let the user pick one, then design_voice with the same description, a name and the chosen seed (and model) saves that voice.")},
		{Name: "check_reference", Lead: i18n.T("grade a recording as a clone reference before clone_voice, as Quick clone's checker does. Nothing is saved."), Args: []skillArg{
			must(i18n.T("Absolute path of the recording (wav, mp3, m4a, mp4…)."), "path"),
			arg(i18n.T("What it says, if known: a mismatch with what is heard is reported."), "text"),
		}, After: i18n.T("Returns grade (good, fine or needs_work), seconds, speech_seconds and hints (code, text: what to fix). clone_voice cleans background noise by itself; with needs_work ask the user for a better recording.")},
		{Name: "preview_voice", Lead: i18n.T("hear a voice without generating anything: a saved voice's sample, a Discover voice's preview or a built-in speaker's clip."), Args: []skillArg{
			arg(i18n.T("Give one: a saved voice (id or name); a Discover voice's id (search_voices); or a model id with one of its speakers (list_models → voices)."), "voice_id", "gallery_id", "model", "speaker"),
			output,
		}, After: i18n.T("Returns the clip (WAV or Ogg) inline and/or as a file, with what it says when known.")},
		{Name: "update_voice", Lead: i18n.T("rename a saved voice or change its style, language, tags or reference text; what you leave out stays."), Args: []skillArg{
			must(i18n.T("The saved voice (id or name)."), "voice_id"),
			arg(i18n.T("A new name; a delivery hint (calm, whisper…); the speaker's language (BCP-47)."), "name", "style", "language"),
			arg(i18n.T("What the reference recording says (cloned voices only)."), "ref_text"),
			arg(i18n.T("Tags, replacing the old ones (an empty list clears them)."), "tags"),
		}},
		{Name: "duplicate_voice", Lead: i18n.T("save a copy of a saved voice (its reference, sample and portrait; not its lock or consent)."), Args: []skillArg{
			must(i18n.T("The saved voice (id or name)."), "voice_id"),
			arg(i18n.T("The copy's name; empty: \"<name> (copy)\"."), "name"),
		}},
		{Name: "export_voice", Lead: i18n.T("write a saved voice as an .asvoice file, to share it or move it to another Mac."), Args: []skillArg{
			must(i18n.T("The saved voice (id or name)."), "voice_id"),
			arg(i18n.T("true also packs the reference recording, so that the file clones the voice elsewhere (only with the speaker's consent); default: the preview only."), "include_reference"),
			arg(i18n.T("An SPDX licence id for the file (CC-BY-4.0…); empty: personal use."), "licence"),
			arg(i18n.T("An absolute folder (made if missing); empty: the agents' folder. The file's name; empty: the voice's name."), "directory", "filename"),
		}, After: i18n.T("Returns files: the path written. A voice without a sample or reference answers not_ready (its takes are never packed: they are private).")},
		{Name: "import_voice", Lead: i18n.T("add a voice from an .asvoice (or VoiceStudio .ovsvoice) file to My voices."), Args: []skillArg{
			must(i18n.T("Absolute path of the voice file."), "path"),
		}, After: i18n.T("Returns the voice and warnings; its reference is checked and cleaned in the background, as in the app.")},
		{Name: "convert_voice", Lead: i18n.T("Developers → Convert: say a recording's words again in another voice (transcribed, then generated)."), Args: []skillArg{
			must(i18n.T("Absolute path of the recording (audio or video)."), "path"),
			must(i18n.T("The voice to say it in: a saved voice's name or id, or a built-in speaker."), "voice"),
			arg(i18n.T("A speech model that speaks the voice; empty: the default for it. The recording's language (BCP-47); empty: detected."), "model", "language"),
			arg(i18n.T("true aims at the recording's length: a target duration on models with one (OmniVoice), else one more take at a matching speed."), "match_length"),
			output,
			arg(i18n.T("The WAV's name in file mode (no folders)."), "filename"),
		}, After: i18n.T("Returns the text heard, the new take (take_id, file, audio_seconds) and its source's length.")},
		// Stories.
		{Name: "write_story", Lead: i18n.T("Stories' Help me write: the writing help's language model drafts a short script for two or three voices from an idea."), Args: []skillArg{
			must(i18n.T("The idea in a sentence or two, in the language the story should be in."), "description"),
			arg(i18n.T("true also makes the Stories project of the draft, cast with voices; a speech model for it (empty: the default)."), "create", "model"),
		}, After: i18n.T("Returns script ([voice:Name] lines, narration untagged), title and, with create, the project (render it with render_longform). Takes up to a minute; llm_unavailable if no language model is set up: then write the script yourself and use create_story.")},
		// Library.
		{Name: "list_assets", Lead: i18n.T("Assets: every file AudioSmith made or took in, newest first: speech takes, story and audiobook renders, exports, dubs, isolated stems, imported recordings, each with its path and the tool that opens its source."), Args: []skillArg{
			arg(i18n.T("speech, story, audiobook, dub, stems or import; empty: all."), "kind"),
			arg(i18n.T("Words in the title, the text or the path."), "search"),
			arg(i18n.T("Paging: at most limit (50 by default, up to 500) after offset."), "limit", "offset"),
		}},
		{Name: "list_samples", Lead: i18n.T("Home's sample projects (a story for two voices, an audiobook chapter, a narration, a dubbed clip, a meeting transcript) and whether this build has them. No arguments.")},
		{Name: "open_sample", Lead: i18n.T("copy a sample project into the library (a new, editable copy each time) to try a feature."), Args: []skillArg{
			must(i18n.T("The sample's id from list_samples (story, chapter, narration, dub, meeting)."), "sample_id"),
		}, After: i18n.T("Returns ref (<kind>:<id>), tool (what reads it: get_longform, get_dub, export_transcript, list_assets) and files.")},
		// Voice isolator and tools.
		{Name: "isolate_voice", Lead: i18n.T("Voice isolator: separate the voices of an audio or video file from its music and effects, and denoise them."), Args: []skillArg{
			must(i18n.T("Absolute path of the file (wav, mp3, m4a, mp4, mov…)."), "path"),
		}, After: i18n.T("Returns isolation_id and job_id at once; follow the job with get_job, then get_isolation for the stems. Needs the separation model (model_not_downloaded otherwise).")},
		{Name: "get_isolation", Lead: i18n.T("the isolations: all of them, or one with its state and stems (vocals, background, clean: absolute WAV paths)."), Args: []skillArg{
			arg(i18n.T("One isolation; empty: every isolation, newest first."), "isolation_id"),
			arg(i18n.T("With isolation_id: also return this stem's audio: vocals, background or clean."), "stem"),
			arg(i18n.T("For stem: inline, file or both; empty: AudioSmith's setting."), "output"),
		}},
		{Name: "probe_media", Lead: i18n.T("what an audio or video file holds: duration, streams, codecs, sample rate, channels."), Args: []skillArg{
			must(i18n.T("Absolute path of the file."), "path"),
		}},
		{Name: "detect_watermark", Lead: i18n.T("check an audio file for AudioSmith's invisible watermark (whether an AudioSmith made it, and this one)."), Args: []skillArg{
			must(i18n.T("Absolute path of the audio file."), "path"),
		}, After: i18n.T("Needs the watermark model (model_not_downloaded otherwise).")},
		{Name: "direct_performance", Lead: i18n.T("Direction: a performance note in your words becomes emotions, an instruction for models that take one, and rate, pitch and energy hints."), Args: []skillArg{
			must(i18n.T("The note (\"whispered, getting angry\", \"a tired smile\")."), "note"),
		}, After: i18n.T("Use its instruction as generate_speech's instructions, its rate as speed.")},
		{Name: "check_line_fit", Lead: i18n.T("Rate fit: will a line fit its time when spoken (ok, tight or overflow), and a shorter version if asked."), Args: []skillArg{
			must(i18n.T("The line."), "text"),
			must(i18n.T("The time it must fit, in seconds."), "slot"),
			arg(i18n.T("Its language (BCP-47); the speaking rate (1 = normal)."), "language", "speed"),
			arg(i18n.T("The original line it translates and its language, for a better rewrite."), "source", "source_language"),
			arg(i18n.T("true asks the writing help for a shorter version when it does not fit."), "rewrite"),
		}},
		// Models and settings.
		{Name: "download_model", Lead: i18n.T("download a model of list_models (0.1–25 GB)."), Args: []skillArg{
			must(i18n.T("The model's id."), "model"),
			arg(i18n.T("true only after the user agreed to the download; without it nothing is downloaded (confirm_required)."), "confirm"),
		}, After: i18n.T("Returns a job_id: follow it with get_job. Licences are not accepted here: if list_models says licence_to_accept, the user accepts it in AudioSmith → Models.")},
		{Name: "get_settings", Lead: i18n.T("AudioSmith's defaults, read only: speech and transcription models, dubbing targets and whether link import is on, the output mode, the agents' folder, your own voice, the watermark. No arguments.")},
	}
}
