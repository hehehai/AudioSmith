# Sample projects

The samples on Home's 示例 tab, the Projects page's 示例 cards and ⌘K
(0.5 F6). Each is a folder `<id>/` with a manifest `sample.json` and the
files it names. The app embeds this folder (`assets.Samples`); opening a
sample copies it into the user's library as a normal project
(app/samples.go, app/samples_copy.go), and opening it again asks whether
to open that copy or make a new one.

A card whose folder is missing keeps its built-in fallback: the story,
the audiobook chapter and the narration fill their page with a text-only
script; the dub and the meeting transcript say 即将推出.

## Folders

| id          | kind        | card                | needs                                   | budget  |
|-------------|-------------|---------------------|-----------------------------------------|---------|
| `story`     | `story`     | 两人对话的故事      | project or script; audio optional        | ≤ 3 MB  |
| `chapter`   | `audiobook` | 一章有声书          | project or script; audio optional        | ≤ 3 MB  |
| `narration` | `narration` | 一段产品旁白        | script; audio optional                   | ≤ 1 MB  |
| `dub`       | `dub`       | 一段配音短片        | project; media (the video, the dub)      | ≤ 6 MB  |
| `meeting`   | `transcript`| 一段会议转写        | project; media (its audio)               | ≤ 2 MB  |

The whole folder stays **≤ 15 MB** (the app binary carries it).
`TestBundledSamples` checks every manifest and the sizes.

## `sample.json`

```json
{
  "version": 1,
  "id": "story",
  "kind": "story",
  "title":    {"en": "A story for two voices", "zh-Hans": "两人对话的故事"},
  "subtitle": {"en": "A short scene, cast and performed", "zh-Hans": "一个短场景，分好角色并演绎"},
  "cover": {"color": "#8B5CF6", "icon": "story"},
  "files": [
    {"path": "project.json", "role": "project"},
    {"path": "audio/story.ogg", "role": "audio"}
  ]
}
```

- `id` is the folder's name and one of the five above; `kind` is
  `story`, `audiobook`, `narration`, `dub` or `transcript`.
- `title` and `subtitle` have `en` and `zh-Hans`.
- `cover.icon` is `story`, `audiobook`, `narration`, `dub` or
  `transcript`; `cover.color` (`#RRGGBB`, optional) tints the icon.
- `files[].path` is relative to the folder (no `..`, no absolute paths).
  Roles:
  - `project`: the project document, as the app saves it:
    - story, audiobook: a `services.LongformDoc` (the body of a saved
      Stories or Audiobook project);
    - dub: a `dub.Project` whose file names are relative to the dub's
      folder (as the app writes them); `source.path` too;
    - transcript: a `core.Transcript`; its `audio` names a `media` file
      (Ogg Vorbis is decoded to the WAV transcripts keep);
    - narration (optional): the take's recipe, `{"model": <repo>,
      "voice", "language", "seed", "speed"}`.
  - `script`: plain UTF-8 text: a story in the Stories markup
    (`[voice:Name] line`, narration untagged, `# Title` first), an
    audiobook manuscript (`# Chapter` headings), the narration's script.
    Used when there is no `project` (story, audiobook) or always
    (narration).
  - `audio`: the finished audio (story, audiobook, narration). The copy
    opens ready to play: it becomes the story's or the chapter's render
    (decoded to the WAV files a render writes, its loudness measured;
    `services.Longform.ImportRender`) or Text to speech's latest take;
    the file itself is copied to `<data>/samples/<copy>/` and listed in
    Assets with the project.
  - `media`: files the project document names (dub: the source video,
    its stems and renders; transcript: its audio). They are copied into
    the project's folder at the same relative path.
- Audio: Ogg Vorbis `.ogg`, mono (the app's players read Ogg, MP3 and
  WAV, not AAC), about 48 kb/s; a dub's media stay WAV (its mixer reads
  WAV). Video: H.264 or HEVC `.mp4`, ≤ 720p, ≤ 1.5 Mb/s, ≤ 30 s.
- Opening a sample counts its page's feature introduction as seen.
- Voices: a document names curated or built-in voices only (no voice of
  a person); the text has no filler words and is not colloquialised.

## The bundled content (0.5 F6b)

Made in an isolated AudioSmith on a Mac (thread #122), through its Local
API, with built-in voices only; each take checked with speech
recognition against its text.

| id          | content                                   | voices (model)                                                        | length |
|-------------|-------------------------------------------|-----------------------------------------------------------------------|--------|
| `story`     | 雨夜修表铺: narrator and two characters   | Eric narrates; 小满 Serena, 老周 Uncle_Fu (Standard voices; SoulX-Podcast speaks the dialogue) | 60 s |
| `chapter`   | 第一章　北站的末班车                      | Uncle_Fu narrates; 列车员 Vivian, 林舟 Dylan (Standard voices)         | 70 s   |
| `narration` | 知光台灯, a made-up product               | Serena (Standard voices)                                              | 29 s   |
| `dub`       | "Why is the sky blue?": our own slides, an English voice dubbed into Chinese by the dub pipeline | am_michael (Kokoro) → a clone of it (Qwen3-TTS Base) | 25 s |
| `meeting`   | a release meeting for two, transcribed with speakers | 林悦 Serena, 陈航 Eric (Standard voices); MOSS-Transcribe-Diarize | 63 s |

The story and the chapter name their voices as `speaker:<id>` of the
Standard voices model, so a copy never depends on the user's own voices.
The dub's media are 16-bit mono WAV, 24 kHz (16 kHz for the original
audio and the background); its line renders are kept so each line plays
in the editor, and their keys name the reference relative to the dub's
folder, so editing a line of the copy renders that line only.
