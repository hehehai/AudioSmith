# Changelog

## Unreleased (0.5.0-beta.1)

### A new start page and a new sidebar
- AudioSmith opens on **Home**: ask 「今天想做点什么？」, then type, paste a link or drop a file and press Enter (Shift-Enter for a new line). Text opens in Text to speech with it as the script, a video or a link starts a dub of it, a recording is transcribed, a text file or an ebook becomes an audiobook's manuscript. A row of tools and your recent work (speech, dubs, transcripts, stories, books) are under it. With the writing help's model loaded, 「智能分派」 lets it choose between Text to speech, Stories and Audiobook.
- The sidebar has places — Home, Voices, Projects, Assets — then the tools: Text to speech, New voice, Stories, Audiobook, Dub, Speech to text, Voice isolator, Dictation, and ··· More (Workflows, Batch dubbing, Calls). Developers and Settings are at its foot.
- **Voices** is one page with two tabs, Discover and Mine; **New voice** offers Quick clone and Design a voice. **Assets** is the former History. **Developers** gathers the agents' setups (MCP, Claude Code, Codex, SDKs), the Local API and the tools (A/B voices, Convert, Rate fit, Direction, File probe, Watermark).
- The search sits at the top of the sidebar, over Home: 「Search ⌘F」 searches everything (pages, actions, voices, projects) in the command palette; ⌘K opens the palette too. A bell beside Settings at the sidebar's foot counts what is going on and lists it with the notifications. It replaces the cards at the sidebar's foot.
- Models, Engine, Logs and Integrations are in Settings only. ⌘1…⌘8 are now Home, Text to speech, Voices, Projects, Dub, Speech to text, Stories, Audiobook; ⌘F puts the keyboard in the sidebar's search.
- Old links (audiosmith://gallery, /history, /tools, /engine…) open where those pages are now; the Local API and MCP are unchanged.

### Text to speech: the script on the left, Settings | History on the right
- The script fills the left of the page, without a box; Insert, Import script… and Pronunciation… are above it. While it is empty, chips under it — Tell a story, Product narration, Podcast intro, News bulletin, Guided meditation, Audiobook excerpt — fill it with an example in the voice's language (Chinese, English or Japanese), else the interface's. A fresh start has an empty script.
- At its foot, the character count and estimated length, and Generate (⌘↩). The latest take sits just above, with 「Another voice」.
- On the right, two tabs. **Settings**: the voice and the model as rows that open their pickers (the voice's previews are still there), 「✦ Recommend」, which lists voices for the script's language and use first (and the model for a language the current one has no voice in), the speed, the language and "Play when ready" as rows, and Advanced at the foot. **History**: this page's takes, newest first — play, the first words, the voice, the length, when — with ⋯ Export…, Reuse settings, Regenerate with another voice, Save as project (Audiobook or Story, for long scripts) and Delete. Assets still lists every take.
- 「Regenerate with another voice」 loads the take and opens the voice picker, recommended voices first: the voice you pick makes it again.
- The panel's toggle is in the page's header. In a narrow window the panel opens over the script from it, or from the model and voice named under the count.

### Dub, Speech to text and the new Voice isolator: start on the page
- **Dub** opens on a composer: drop a video or an audio file, or paste a link, pick the language to dub into and press 「配音」. The dub starts at once — the source's details, the download and the transcript run in the background — and shows on top of the list under it, with its progress. The list has a search, filters (in progress, done, failed; a language) and each dub's cover, languages, length and status; ⋯ opens, exports or deletes it. A click opens the dub's editor, as before; ‹ in its header, or 视频配音 before its title, goes back to the list. Creating an empty project first is gone.
- **Speech to text** works the same way: drop or upload a file (or record), choose the language, speakers and Fast or Accurate, and send it; the transcript shows in the list and opens on its own page. A transcript has two new hand-offs: 「配音」 makes a dub of the same file, 「在文字转语音里朗读」 opens its text as the script.
- New: **Voice isolator**. Drop a song, a video or a noisy recording and get the voices alone, the music and effects, and the voices denoised, on this Mac, with the models dubbing uses (HTDemucs, DeepFilterNet). It runs as a task in the bell; each result plays and saves its three tracks, and goes on to Speech to text or Dub. If its models are missing the page says which, with their size, and downloads them. Results are kept in the data folder and appear in Home's Recent.

### Every page has its own header
- There is no bar across the top of the window any more. Each page's top row is its own: its title, ‹ › to go back and forward through the pages you saw (also ⌘[ ⌘] and a mouse's back and forward buttons), how many things it lists (「3 项」), and on the right the page's own tools — search, filters, views, actions and at most one main button. Projects: search, category, list or cards, New. Assets: search, All/Starred, the model, and Open folder. Voices: search, the filters and 「+ New voice」, with Discover and Mine just under the header. Text to speech: the panel's toggle.
- A dub, a transcript or an isolation shows its name with its list's before it (视频配音 / Interview); ‹ goes back. The extra back rows and breadcrumbs are gone.
- Home shows only ‹ ›: its question is the title. Settings has ‹ › beside its name to go back through the sections you opened.
- In a narrow window a page's search is a magnifier that opens it, and its less used tools go into ⋯.

### Voices: browse, open, use — and make your own step by step
- **Discover** shows the use cases as chips (Narration, Conversation, Characters, Ads) after Language, Gender and Age; while nothing filters, 「Recommended」 gives six strong voices for your language, then 「All」 lists every voice as a table: name and description, language, gender and age, use, ▶, 「Use」 and ⋯ (save to My voices, Stories, Audiobook, Design, favorite).
- ▶ plays a voice's preview right in its row (one at a time, the row shows it). A click on a row opens the voice's **details** at the right (over the list in a narrow window): a large preview, 「Try a line」 said by the model that speaks it, and 「Use in Text to speech」, 「Use as a story character」, 「Narrate an audiobook」, 「Save to My voices」.
- **Mine** is a table too: name, kind, language, created, how often used; filter All / Cloned / Designed / Saved (from Discover). ⋯ renames, duplicates, exports a .asvoice or deletes (with Undo). A saved voice's details hold what the profile editor had — the reference and its quality, settings, lock, ownership, where it is used, export — under the same preview, hand-offs and 「Try a line」.
- **New voice** offers two large cards with what each needs (Quick clone: 10 seconds or more of clear speech, 30 is best), and the voices you made lately.
- **Quick clone** is one guided page: Add a recording → Check (the part kept, its quality and cleaning, the transcript) → Name and save → Done, with 「Listen」 and 「Use in Text to speech」.
- **Design a voice** starts with the description; the presets are chips under it and fill it. 「Generate candidates」 makes three takes of it with different seeds: listen, pick one, name it and save; then 「Use in Text to speech」. Details and the model fold away below.
- The Studio's voice picker ends with 「Browse the voice library ›」.

## 0.4.0-beta.8 — 2026-10-26

### Idle models leave memory
- Models you have not used for 30 minutes are now unloaded, dictation's speech model included, so AudioSmith gives the memory back to your Mac. They load again by themselves when next used: the first dictation after a long pause may take about a second longer, and the pill says “Loading model…” while it waits. Nothing you record is lost.
- Settings → Models & network → Memory: “Unload idle models” offers after 5, 15, 30 or 60 minutes, or Never. If you had never changed it, it moves from Never to 30 minutes once; a choice you made is kept.
- A model is never unloaded while a job uses it, during a dictation or during a live call; the 30 minutes count from its last use.

### Fixes
- Setup: "Download models later" did nothing (issue #1): the wizard put the recommended pack back on the next frame, so the wizard went on with it. The choice now sticks.
- Dub: a line that is not translated yet opens in the editor on its text. It used to open empty, so the text seemed to vanish when clicked (issue #2). Done without a change no longer saves the source as the translation.

### For contributors
- The app's Go code (package main) moved from the repository root to `app/` (mygo.json `"main": "./app"`); the files it embeds moved to the `assets` package (`assets/`, starter voices now in `assets/starter-voices`). Run it with `go run ./app`, test it with `go test ./app`; `go tool mygo build`, `mygo dev` and `mygo vet .` work as before.

## 0.4.0-beta.7 — 2026-10-24

### Dub timeline: zoom and pan
- Zoom the waveform timeline with a trackpad pinch, ⌘ or ⌥ with the scroll wheel, or ⌘= and ⌘−, around the pointer; ⌘0 or a double click on the zoom readout shows it all. It zooms from the whole source to 100 pixels a second.
- Pan with a sideways two-finger swipe (with the system's inertia), ⇧ with the wheel, a middle-button drag, or the new overview strip under the zoom controls, which shows the part in view over the whole source; drag it, or click beside it to jump there.
- While playing zoomed in, the view turns the page as the playhead reaches its edge, unless you panned in the last few seconds.
- Dense lines no longer crowd the timeline with numbers: a number shows only where it fits and leaves room after the last one.

### Dub: see the video before it downloads
- Paste a link and the Dub page shows what it is within seconds, before anything downloads: the cover, the site (YouTube, Bilibili, Vimeo, TikTok and the others yt-dlp knows), the title, channel, length, picture size, estimated download size, date, views, the description (Expand shows all of it) and the caption languages. It is kept in the dub, so it shows again without the network.
- While the video downloads, a thin bar under the cover gives the size and time left; once it is downloaded, the centre shows the video itself, with play, while transcribing and the steps after it report in the footer.
- A link that cannot be read (a private or removed video, one that needs a sign-in, a site that is not supported) says so before any download, with the site's reason, and Try again. Cancel stops the reading as it stops a download.
- A dropped video or audio file shows its poster, length, picture and size the same way.
- The cover is fetched by the app itself, through the same proxy as the download, from public addresses only, at most 5 MB, and kept only if it is a JPEG, PNG or WebP picture.

### Faster with long projects
- Dub: the editor builds only the lines in view; a 2-hour video's 2,000 lines no longer slow every frame (190 ms → under 1 ms a frame).
- Audiobook and Stories: a long manuscript (a novel) is checked and its draft saved once you pause typing, away from the window, not at every key; typing in 500,000 characters is as quick as in a short text.
- Studio and Audiobook editors: wrapping long text remembers what it measured, so opening, resizing and typing in a long manuscript no longer stall (rows still never pass the editor's edge).
- The invisible watermark is added while the audio streams through, so watermarking an hours-long dub no longer holds the whole recording in memory several times.
- The engine keeps up to 6 models loaded (small ones don't count) and lets the memory budget decide, so a story render no longer reloads a model at every line.
- Finished tasks older than 30 days are removed at startup; voice previews no longer copy their audio every frame; the render cache is tidied at most once a minute.

### Security
- Voice names are cleaned wherever they come in (imported .asvoice and .ovsvoice files, the Local API, MCP, the app, Discover voices): line breaks and control characters become spaces, and a name keeps at most 80 characters. Names saved earlier are cleaned the next time they are saved.
- The agents' skill and setup prompt list voice names as data (quoted, cut short), not as text an agent could take for instructions.
- MCP voice cloning and design work only from this Mac (or with the API key), like the Local API's; over the LAN, a voice's audio is its sample, never the reference recording.
- Built with Go 1.27.3 (security fixes in Go's standard library).

### Logs
- An operations log in the app's log (source “ops”, one key=value line each): creating, renaming, deleting, importing and exporting voices, projects, dubs, transcripts, workflows and takes, with who asked (the app, the Local API, LAN, MCP); settings changes by name; each job's start, end, failure (with its error) and cancel (with who cancelled and how long it ran); downloads; Local API and MCP calls that change something, with their status; errors shown as toasts; the app's start and each step of its quit. Never script text, transcripts, prompts or secrets.
- Logs and diagnostic bundles mask Local API keys, Twilio tokens, keys read from the Keychain and bearer tokens, and show the home folder as “~”; the engines' own log files are masked too.
- Errors that were dropped silently in background work (cancelling jobs, export records, notifications, files the app writes or removes…) are logged.

### Fixes
- Studio and Audiobook script editors: the last character of a line was cut in half at the right edge on the Mac (a Chinese comma or full stop measured half as wide on its own as within its line). Lines now wrap where they are drawn.
- Importing a video from a link shows its real progress (percent, size, time left) on the page, in the footer and on the sidebar card, with the video's title; Stop on the card and Cancel in the footer really stop it (yt-dlp's helper process kept downloading before), and activity cards no longer show an empty bar.
- A copy built on the Mac says, in Settings → About → Software updates, why it cannot update itself, with a link to the download page.
- My voices: Delete… (and the other items) in a voice card's ⋯ menu did nothing — no dialog, no log line — unless the voice's editor was open: the menu went away with the pointer before the item chosen reached it.
- Deleting a voice says where it is used (stories, books, dubs, workflows, the Studio, batch dubbing, calls, agents) and can be undone from its toast for about 10 seconds; its files wait in the library's trash until then. Places that named it show “Voice deleted” to choose another one, a story or book that uses it stops with a clear message, and the Studio says its voice went — never another voice in silence.
- Quitting never hangs: edits not saved yet (drafts, the dub editor's changes) are written first, and jobs that do not stop when cancelled — such as a stuck download — are left after a few seconds (they continue or wait for Retry at the next start). An engine that stops reading its requests is restarted instead of blocking.
- A job after an interrupted one (the engine stopped twice in a stage) no longer waits forever: it fails, saying why, the dub or book is no longer shown as busy, and Resume continues it. Chains stuck this way in an earlier version recover at the start.
- The language model's turn on the GPU lasts until its request has really stopped, so the next job is not refused as busy.
- Models moved by a move that did not finish (cancelled, a model in use by another app, the app quit) no longer show as not downloaded; the move continues after a restart.
- Deleting a dub waits for its running stage to stop; two runs of the same dub started at once make one.
- Stories and Audiobook: a rename on the Projects page no longer marks unsaved changes as saved; saving over a project changed elsewhere (by an agent through the Local API) asks whether to save a copy or replace it, and the Local API refuses an edit made on an outdated read.
- Moving the data folder measures it and cleans up a failed copy in the background, with the dialog saying so, instead of freezing the window.
- Smaller fixes: a crash in background work is logged instead of closing the app; a draft that could not be written is tried again; unfinished temporary files are cleaned in every folder; downloads left running by a crash are stopped at the next start.

## 0.4.0-beta.6 — 2026-10-23

### The window in the ActNow family's layout
- The sidebar starts with a search field (⌘F): it searches projects, voices, takes, transcripts and exports, and the Projects page shows what it finds ("3 found", Clear search). ⌘K opens the command palette from the button in it.
- Studio, Projects and History come first; Create (Stories, Audiobook, Dub, Transcribe), Voices and Automate are sections that fold. The ⌘1–⌘9 keys are as before.
- No status bar: what goes on shows as cards at the foot of the sidebar — a stopped engine (Restart), dictation and calls under way (Stop, Hang up), each story, book, dub and workflow being made, takes, exports, transcriptions and downloads, several of a kind as "2/5", a finished one for 3 seconds. The pointer over them opens the stack; View all opens the new Tasks page, with every job that runs or waits.
- Under the cards: Settings, the logs panel (⇧⌘L) and Tools, and the notifications' bell (a dot while some are unread).
- Models and Integrations are Settings sections now (their links open them); Engine and Logs left the sidebar (the palette, the Go menu, Settings → Advanced → Diagnostics). ⌘E's quick switch is a small dialog with the engine's state and the model in use.
- Settings: 17 sections become 11, each a column of groups with a line under every setting: General (with Appearance), Studio (with Pronunciation), Dubbing, Dictation (with Permissions), Writing help, Integrations (apps and agents, then the Local API), Audio tools, Models & network (the models, then downloads, storage, the Hugging Face token, memory and licences), Privacy, Advanced (storage, usage, diagnostics and the self-check) and About. Old links open the part they were.
- Page titles are smaller; Projects and History have ‹ › (⌘[ ⌘]) and how many; Projects switches list and cards in its header (⌥1 ⌥2). Toasts show at the foot of the page, except on the Studio, Dub, Stories and Audiobook, whose foot has a bar of its own: there they show in the header.

### Updates show in the window
- MyGo's update window is gone: a new version shows as "Update available" at the foot of the sidebar (a dot on the icon rail's download button), and nothing pops up. Its panel shows the version's notes in AudioSmith's language from actnow.dev (in English where a version has no Chinese notes, marked so), Download and Install with the download's progress, and Restart to Finish the Update. A failed download says AudioSmith is unchanged and offers Try again.
- Settings → About → Software updates: Check for Updates…, the daily check, installing without asking (a version found by the daily check installs and waits for the restart) and beta updates as before, and Show Changelog…, every version's notes in AudioSmith's language. The choices made in the old update window carry over.
- Releases send their notes in English and Chinese (CHANGELOG.md and CHANGELOG.zh-Hans.md), so actnow.dev's changelog page and the app show each in its language.

### Discover voices: about 35 chosen voices instead of 1,184
- Discover voices now lists a small set chosen by listening tests' measurements (thread #91's audit): Chinese and English voices first, a few in Japanese, Korean, Spanish, French, German, Italian, Portuguese and Russian. The 1,126 generated combinations and the weaker hand-made voices are gone from the list.
- Each designed voice has a written character (age, voice, situation) that the model really gets, a seed that was tried, and a reference that ships with the app: Use voice saves it ready to speak, with no reference to wait for. Preview plays a clip made from that reference, as the Studio would say it; nothing is rendered to listen.
- The gallery also offers the built-in speakers worth starting from (Serena and Dylan for Chinese, Kokoro's best English and European voices): Use voice saves the speaker and opens the Studio.
- Filters: use (narration and audiobooks, conversation and podcasts, characters and kids, ads and announcing), language, gender and age, and search. Pitch, accent or dialect, style and the nine categories are gone.
- Voices saved from the earlier gallery keep working (they carry their own recipe), and dub casts or agents that name an earlier gallery voice or design preset still get it: those ids are kept, just not listed.

### Design: eight starting points
- Narrator, Conversational, Newsreader, Presenter, Storyteller, Elder, Villain and the new Soft (which replaces Whisper and Calm Guide). Upbeat, Kid and Northeast Buddy are gone (a Northeastern voice is in Discover voices). None asks for slow speech any more.

### Fewer weak built-in speakers
- Kokoro's af_nicole (a whisper) and bm_lewis (rough) are no longer offered, and Qwen3-TTS's Ryan is not offered in Chinese (he gave up on Chinese numbers in tests). A project or a setting that uses one keeps it, and the API still takes them by name.

### Settings breathe, and say it shorter
- Every Settings page follows one spacing: the groups' padding, room above and below each row with a line between rows, a group's note as a block of its own, and room between titles, lines and controls. Writing help's model picker no longer cuts its value, and the status line that repeated the rows is gone.
- 28 texts are shorter, in English and Chinese.

### Designed voices no longer say leftover reference text
- Slow designed voices (Storyteller, Elder, Meditation guide, and a few gallery voices) were cut off half-way through their reference: Qwen3-TTS stopped at a length limit that slow speech reached (engine patch 0008 raises it from 0.48 s to 0.8 s per text token, and AudioSmith's own limit can now raise it as well as lower it). The reference was still saved with its whole text, so clones said the missing half before every line.
- A reference made from a voice must now say its text: when the recognizer hears none of the three takes say it (or they were cut off), they are made once more; a kept take that says something else gets what it says as its text, or none is kept and the voice says why. Its text is never one the audio doesn't say.
- References made before are heard once, the first time a take, story or book clones them: one that doesn't say its text is made again (a note says so) or gets the text it says.
- Design prompts no longer ask for slow speech ("speaks slowly", "语速偏慢") from a preset or a picked pace: it made references slow and clones sound read. A description you write keeps it.
- Voices designed for Japanese, Korean, German, French, Spanish, Italian, Portuguese or Russian say their reference in that language, not English.
- Gallery previews use the tuned sampling the Studio uses (temperature 0.7, top-p 0.8 for Qwen3-TTS) instead of the engine's defaults; previews are made again once.
- The Hindi gallery voice is hidden: no model here designs or clones Hindi.

### Dictation's sounds to choose
- Settings → Dictation → Sounds: the sound of listening starting, of it stopping and of an error are each chosen from the ActNow family's sound library (Tally, Wrap and Shrug by default), macOS's system sounds or none. A compact picker searches them (names and tags), filters by type, plays each with ▶ (again to stop) and sets the volume in steps; Try plays the choice.
- The sounds are React Bits Sound Lab's and come with the builds from actnow.dev and GitHub releases only: they are not in this repository or under its licence (NOTICE). A build from source plays the system sounds AudioSmith used before (Tink, Pop, Basso).

### Models move to the ActNow folder, out of Time Machine
- Models are kept in ~/Library/Application Support/ActNow/models, shared with the other ActNow apps, instead of the Hugging Face cache (~/.cache/huggingface), which cleaning tools treat as a cache. Models already there are brought over at the first start without downloading them again (hard links: the cache stays as it is, for the tools that use it).
- Time Machine leaves models out (they can be downloaded again); Settings → Advanced → "Back up models with Time Machine" turns it back on for all ActNow apps.
- A model file cut short (a disk error, a copy that stopped) is no longer taken for a downloaded model; downloading again repairs it. A download of another version left half-way no longer makes a downloaded model look incomplete. Checking a model's files waits for other ActNow apps working on it and tells them when a file was bad.
- If the ActNow folder's model claims are deleted (a cleaning tool), AudioSmith claims its models again when it comes to the front, and keeps models another app may still use.

### Audiobooks keep their rendered parts
- The parts of an audiobook already rendered are kept in AudioSmith's data folder instead of ~/Library/Caches, which cleaning tools empty: Resume after a stop or a failure no longer renders hours of a book again. Those of earlier renders move over at the first start.
- Temporary files of recordings and imports cut short are cleaned at start.

### Keys stay out of logs
- Google Translate (Basic)'s key is sent in a header, not in the address, so a network error no longer writes it into the log. Logs and the diagnostic bundle leave out keys, tokens and proxy passwords.

### Dub editor: ⌘Z and ⇧⌘Z undo and redo again
- In the Dub page's line editor ⌘Z did nothing on the Mac (the Edit menu's Undo took the key before the editor saw it), and with menu keys that have ⇧ fixed (MyGo patch 0009) ⇧⌘Z would have done nothing either. Edit → Undo and Redo, and their keys, now undo and redo the editor's changes whether a line, the toolbar or the timeline was clicked last; while a line's text is being edited they undo its typing, as before.
- Menu items with ⇧ in their keys show and take exactly those keys (⇧⌘Z is Redo, not also ⌘Z).

### The local writing model answers in full
- The built-in writing model (Qwen3 4B) could cut its answers short or garble them when they had to follow a fixed form. The AI director left the last line of a story undirected; the emotion director could lose half a scene (`"surprisedn”: 2note”:` in place of an emotion); Cinematic translation's review copied the translation instead of pointing out problems, so nothing was improved. These came from how the engine kept answers to the form. They are fixed: in tests every line of every story is directed and the review finds real issues.
- The same request now gets the same answer every time; before, the order of an answer's parts changed each time AudioSmith started.
- An answer that starts repeating itself is stopped at once and asked again, instead of running for minutes (a glossary note did).
- A retry after an unusable answer is no longer a copy of the same answer: it is sampled.

## 0.4.0-beta.5

AudioSmith joins the ActNow family: it works together with the other ActNow apps on this Mac (Dicta, Auralink, Pik, VoxtNew). The apps share one copy of each model, some settings, and, if you agree, API keys, and they don't get in each other's way. AudioSmith names another app only when a released version of it is installed; with no other ActNow app, nothing on screen changes.

### Downloads and updates move to actnow.dev
- AudioSmith now has its home on our website: [actnow.dev/audiosmith](https://actnow.dev/audiosmith). Download it from [actnow.dev/download/audiosmith](https://actnow.dev/download/audiosmith) (free; the download needs a free account), and read every version's notes at [actnow.dev/audiosmith/changelog](https://actnow.dev/audiosmith/changelog). The source code stays on GitHub under the AGPL-3.0, and you can still build AudioSmith yourself.
- Updates come from actnow.dev: the daily check reads the version file there, and with Receive beta updates on also the betas' one, and offers the newer version. Copies of 0.4.0-beta.4 and earlier update to this version from GitHub, one last time.
- Settings → Diagnostics and about links to the website, the download page and the release notes.

### Models shared with other ActNow apps
- On first run, if another app (say Dicta) has already downloaded some of the models AudioSmith needs, the setup says so ("3 models are already on this Mac · Installed by Dicta · no download needed") and uses them. Only the rest is downloaded. Choose models… lets you pick them yourself on the Models page.
- The Models page shows which models AudioSmith shares with other apps ("Shared with Dicta"), which ones another app has loaded right now ("Also loaded in Dicta"), and which ones another app installed ("Installed · from Dicta", with Use).
- Deleting a model that another app also uses asks what to do. Remove from AudioSmith keeps the files on this Mac for that app. Delete for all apps deletes them; the other app then has to download the model again. Clean up and "Erase everything" keep what other apps use, and say which app uses it. A model another app deleted for all apps shows "Deleted in Dicta · Download again".
- Settings → Storage shows how much space AudioSmith shares with other apps. It also offers to clean up models that only uninstalled apps used.
- If you accepted a model's licence in another app, AudioSmith still shows the licence, says when you accepted it, and asks you to continue.
- Kokoro's, Chatterbox's and Whisper's helper files now always load from the copy AudioSmith downloaded, at that version. Before, the engine could download them again on first use, or pick up another app's version.
- Downloads coordinate with the other apps: a file another app is downloading is not fetched twice ("Downloading in Dicta…"), and a model is not deleted while another app downloads or uses it.
- The engine no longer guesses which version of a model to load when several are on disk.

### Settings shared with other ActNow apps
- The interface language, proxy, download source and model folder are shared with the other ActNow apps. With another app installed, each of these settings says whether it applies to all ActNow apps, and you can keep a different one for AudioSmith only.
- When another app keeps its models in a different folder, the Models page offers to move AudioSmith's models there. The move can be cancelled, and models the folder already has are not copied again.

### API keys shared with other ActNow apps
- API keys and the Hugging Face token can be shared with the other ActNow apps. With another app installed, a checkbox under the key ("Let other ActNow apps on this Mac use this key") chooses whether to share it.
- AudioSmith offers a key saved in another app ("Use the DeepSeek key from Auralink"). It reads that key only after you click Use this key.
- Removing a key that another app also uses asks whether to stop using it here or to delete it for all apps. If another app deletes a shared key, AudioSmith asks you for a key again. "Erase everything" never deletes another app's key.
- The Hugging Face token is looked up in this order: `$HF_TOKEN`, the shared token, AudioSmith's own, the huggingface-cli token file.

### Shortcuts and memory
- If another ActNow app uses the same keys as the dictation shortcut, Settings → Dictation says so. The app that took the keys last shows the notice once, with Change shortcut, Open Auralink settings and Ignore. While you record a new shortcut, "Also used by Auralink (Transcribe)" warns you, but you can still save it.
- The ActNow apps share one memory budget. Before AudioSmith loads a model, it unloads its own idle models first. If that is not enough, it asks the other apps to unload models that have been idle for a minute. It does the same when they ask, and never unloads a model a job is using.
- Dictation's paste no longer overwrites the clipboard while another ActNow app is pasting.

### Dub: conversations recorded together (multi-speaker dialogue)
- A new switch in the Dub page's Cast step, 「多人对话配音」 (Multi-speaker dialogue): the people of a conversation are recorded together by the dialogue model "Dialogue · Light" (SoulX-Podcast), each line hearing the one before it, so answers sound like answers. Each line is then fitted to its own time as before (Smart fit and the other timings) and placed where it was said, so the video's pauses and overlaps stay. Monologues, one person speaking alone and lines over 4 s apart stay line by line.
- On by default for new dubs into Chinese or English with two speakers or more; dubs made before keep their audio (off). Each speaker is cloned from about 12 s of their own lines, with what they say there, even from another language (an English or Japanese video dubbed into Chinese); a saved or designed voice from its own reference.
- A line the dialogue model does not say clearly is made again (after the lines before it, another seed), then line by line; such a line is checked too, and marked "Listen" when it is not clear either. Editing a line records it alone again after the lines before it; changing a voice records the conversation again. The line list shows each conversation's lines with a coloured bar and a 「对话 N」 chip; each line's ⋯ menu can record the whole conversation again or keep a line out of it.
- Greyed with the reason on Macs under 16 GB, with the 「逐句」 voice match, and for other languages (a Japanese track stays line by line; a dub into Chinese and Japanese uses it for Chinese only). Not downloaded yet: the switch offers the download (2.4 GB), and dubs are made line by line until it is there. The Generate step's model picker lists it too. Slower than line by line.
- The translation of such a track aims a little shorter: the dialogue model speaks about 1.01–1.12× the estimate (learnt from the lines it makes).
- Local API and MCP: `create_dub`, `edit_dub` and `batch_dub` take `dialogue` (`auto` or `off`) and `dialogue_model` (`soulx`), `edit_dub` a line's `no_dialogue`; `get_dub` returns `dialogue`, `dialogue_blocked`, `dialogue_stretches` and each line's `dialogue`, `made_by` (`dialogue` or `single`), `fallback_reason` and `flag`; `run_dub` with `segments` naming a conversation line records it again after the lines before it.

### Audiobook: dialogue passages (多人对话段)
- A scene in a book can now be spoken by a dialogue model, each line hearing the lines before it, so the characters answer each other. Mark it in the manuscript as a dialogue passage: `[对话]` on a line of its own (`[dialogue]` in English; `【对话】` works too), then one `名字：台词` (`Name: line`) line per turn, then `[/对话]`. A line without a name is narration, read by the narrator; a line in parentheses is a stage direction, not read; `名字（低声）：` gives a direction that is not read either; `[pause 1s]` on its own line lengthens the gap; `[voice:NAME]` lines work as well. The rest of the book stays on its model.
- Insert → "Dialogue passage" writes an empty passage with the latest names of the manuscript (the first one selected, to type over), or makes the selected lines one, keeping every word: quotes become the turns of whoever the speech verb names (or, between two speakers, the other one), the words around them narration lines, and lines it cannot attribute are marked "？" for you to fix. Undo in the toast.
- In the editor a passage has a faint tint and a dot per line, each speaker's name its own colour, stage directions grey, and a summary after `[对话]` ("3 人 · 5 句 · 多人对话 · 轻量"); names of the cast are offered as you start a line; the context menu has "Preview this dialogue" and "Turn back into paragraphs".
- The inspector has a "Dialogue passages" section while the book has some: each passage with a preview button, the dialogue model, and what happens without it. Passage speakers are part of the cast (marked "dialogue"); a speaker nobody is cast as gets a voice of their own, unlike the others', when the book is generated (or at "Assign voices").
- Without the dialogue model (not downloaded, or a Mac with less than 16 GB) the passages are read line by line in their voices, with the same tight conversational gaps; so are passages of one speaker and passages in languages other than Chinese and English. The dialogue model's lines are brought to the level of the chapter's narration. Chapter markers, the read-along and M4B export work as before; books without passages render exactly as before.
- The Local API and MCP: `update_longform` takes the manuscript as `script` and `dialogue`/`dialogue_model` for audiobooks; `get_longform` returns `script` (up to 100 KB) and `dialogue_blocks` (chapter, index, speakers, lines, uncast speakers, `mode` dialogue or per_line and why); `render_longform` takes `dialogue_block: {chapter, index}` to preview one passage and lists the voices it gave as `voices_given`.

### Dub: foreign-language videos dubbed into Chinese speak Chinese
- Dubbing a Japanese or Korean video into Chinese with the default clone model (Qwen3-TTS Base) made many lines in the source language or garbled: given the speaker's clip and its transcript, the model went on in the clip's language (a Japanese test: 9 of 14 lines). Lines in another language than their speaker's clip are now cloned from the clip alone, from its voice print, and come out in the line's language as much like the speaker: in tests from Japanese, Korean, French and English into Chinese and from Chinese into English, 0.4–7 % character errors instead of up to 42 %, and as or more like the speaker. Dubs in the video's own language are made as before.
- Those lines are also heard again after they are made: a line that is not in its language, or does not say its text, is made once more and the better take kept; one that still fails is marked "Listen" in the line list, with what was heard, and the step says so, instead of being kept silently (the Local API's `flag`: `heard` or `language`). Conversation lines made line by line are checked for the wrong language too.
- Qwen3-TTS Base, given a clip without its transcript, now clones that voice; it used to speak in no particular voice.

### Model pickers fit the window; dialogue models in the Stories picker
- The model picker's list no longer runs past the bottom of the window with "Show all models" open (the Stories inspector, small windows): it opens below its button, or above it when there is little room below, at most as tall as the room there, and its list scrolls to the last model under the search field. Voice pickers follow the same rule.
- The Stories model picker has a section "Whole dialogue (multi-speaker models)" with the two dialogue models, now named "Dialogue · Light" (SoulX-Podcast 1.7B) and "Dialogue · One take" (MOSS-TTSD v1.0), with the memory they need and their download size. Choosing one turns whole dialogue on with it (the Performance switch follows); the story's model then reads the narration and the lines the dialogue model does not say clearly, as the button says ("Narration and fallback: …"). Macs with too little memory, and stories in other languages, see them greyed out with the reason.

### Better voice references: checked, cleaned, and longer for designed voices
- Clone and dialogue models copy a reference's noise, loudness, intonation and pace. AudioSmith now checks every reference where it comes in (Clone, an imported voice, Dub's speakers, Stories' whole dialogue) and says in one line how good it is ("Reference quality: good / fine / needs work") with at most two tips: too short, very noisy, distorted, a transcript that does not match, a flat delivery, or (for dialogue) a slow, read-aloud pace. Nothing is blocked.
- References are cleaned automatically: background noise is removed only when there is some (DeepFilterNet; clean recordings are left alone), the silence before and after is trimmed, long pauses are shortened, and the loudness is evened out to −18 LUFS. The original is kept: Undo on Clone, "Use the original" in the voice's editor.
- Recordings of 15–30 seconds keep their best 10–14 seconds. Clone has tips for recording your own voice: a quiet small room, talk rather than read, record 15–30 seconds.
- Designed voices get a reference of their own for clone models, portable copies and Stories' whole dialogue: about 12 seconds of lively everyday speech in a casual tone, the best of three takes. Their greeting sample stays for previews. New designed voices get it when saved; older ones when first used with a clone model or in whole dialogue (or "Make reference" in the voice's menu).
- Portable copies of built-in voices now read everyday narration instead of a sentence about the recording, asked to sound casual where the model takes an instruction, and keep the best of three takes. They read in the speaker's own language (Vivian, Serena, Uncle_Fu, Dylan and Eric Chinese; Ryan and Aiden English), whatever the app's language.
- Dub denoises a speaker's reference only when it is noisy, and tidies it the same way.

### Stories: emotions that stay coherent across a scene
- The AI director now reads the whole story and plans its emotions: a usual mood for each character, and for each line one of 13 emotions (steady, happy, excited, tender, pleading, hesitant, worried, hurt, sad, angry, scared, surprised, apologetic) with a strength from 0 to 3, the turns of the scene marked. Most lines stay steady; a character's feeling changes one step at a time, except at a turn.
- Each line is sent a short instruction made from its emotion ("略带委屈", "非常难过，带哭腔"), the same wording for every line of a character, instead of the persona and the scene with every line. In a test story, a father's voice moved over 8 semitones instead of 19.
- Each line shows its emotion and strength; you can change them, mark a turn, or write your own wording. Directing again keeps what you changed. Each character has a usual mood you can set.
- The pauses between lines follow the emotions: excited lines come quicker, hesitant or sad ones after a longer pause (Advanced → "Pauses follow the emotion").
- A character's lines are said in one go, stretch by stretch, so the voice carries on from line to line instead of starting afresh; AudioSmith cuts them apart again with the forced aligner and checks each line. A stretch that cannot be cut cleanly, and narration, are made line by line. Advanced → "Each character's part in one go" turns this off.
- Stories directed before this version sound exactly as before until you direct them again.
- The Local API and MCP: `update_longform` takes `emotions` (line id → emotion) and intensities 0–3; `direct` runs the new director.

### yt-dlp and ffmpeg download from a mirror too
- yt-dlp (importing from links) and ffmpeg now download from GitHub or ActNow's mirror, mirror.actnow.dev, whichever is faster; a download that stalls or crawls moves to the other. GitHub's downloads reach many users in China at a few KB/s.
- Both are still checked against their pinned SHA-256 before use: a file that does not match is fetched again from the other source, so the mirror cannot change what you get.
- Settings → Network → Download from now covers models and tools: Automatic (the fastest of the official sites and mirrors), Official sites only (huggingface.co, github.com), Mirrors only (hf-mirror.com, mirror.actnow.dev), or Custom (Hugging Face URLs; tools then come from either). Test the connection also reaches mirror.actnow.dev.

### Stories: whole dialogue (整场对话)
- A new switch under Performance, "Whole dialogue": the characters' lines are spoken by a dialogue model (SoulX-Podcast 1.7B, Apache-2.0) a scene at a time, so each line hears the lines before it and answers them. Narration stays on the story's model.
- Each character is cloned from their voice: a cloned voice's own clip, a designed voice's reference, or a short sample the voice reads first.
- Every line is checked: one the model does not say clearly is made again with the same context, then on the story's model.
- Up to four characters per scene. Needs 16 GB of memory (the switch says so on smaller Macs) and a 2.4 GB download; Chinese and English stories. On an M4 it runs at about 0.7× real time.
- A second dialogue model, MOSS-TTSD v1.0 (OpenMOSS, Apache-2.0), on Macs with 24 GB of memory or more: pick it under "Dialogue model". It speaks each scene as one recording, with its own pauses between speakers, up to five characters per scene, and AudioSmith cuts the recording into the lines (with the forced aligner) and checks each one; a line it does not say clearly is made again after the lines before it, then on the story's model. An 11 GB download; on an M4 about 0.8× real time, using about 12 GB of memory while it speaks.
- The Local API and MCP: `update_longform` takes `dialogue_model` (`soulx` or `moss`); `get_longform` returns it.

### Chinese numbers, phone numbers and units
- Chinese text now reads phone numbers digit by digit (138-0013-8000, 010-12345678, 拨打10086), ranges (3-5天 → 三到五天, 1-2个 → 一到两个, 10%-20%), scores (3:2 → 三比二), dates with dots (2024.03.05) and units (5km → 五公里, 3.5kg, 100m², 60km/h, 220V, 128GB). Words with digits (MP3, iPhone15, COVID-19) and other languages are unchanged.

### Dub: API keys for the online translators
- The online translators (DeepL, Google, Microsoft) have an API key field in the Translate step's Advanced options. Before, the key had to be added to the Keychain by hand.

### Logs
- Click a line in the logs (the page or the panel) to copy it. Shift-click, ⌘-click or drag to select several lines, as in Finder; ⌘A selects every line shown, Esc deselects, ⌘C copies. While lines are selected the header says how many, with Copy and Deselect. Right-click a line to copy it, the selected lines or every line shown.
- The newest line is at the top. Scrolled down to older lines, the list keeps its place as new lines come in, and "N new lines" at its top goes back up. Copied lines are in the log's order, the oldest first.

### New icon
- AudioSmith has its own icon: a white anvil with a lime spark on a green-to-teal gradient, in the same style as the other ActNow apps (macOS 26 adds its Liquid Glass look). The menu bar icon is the anvil too; while dictation listens, a dot takes the spark's place.

### Fixes
- Models → Clean up could delete models another ActNow app uses, such as Dicta's language model (0.4.0-beta.1 to beta.4). It now keeps every model another installed ActNow app uses, and says which app.
- The Dub page's button read "Generate dub%!(EXTRA int=1)" with one target language. It reads "Generate dub" again, and the Translate button got the same fix.
- `HF_ENDPOINT` now overrides the download source chosen in Settings. Before, the setting won.

## 0.4.0-beta.4

### Updates and downloads behind a proxy
- Updating failed behind a proxy ("unexpected EOF" part way through the download): the updater did not use the system proxy. It now goes through the proxy of Settings → Models (automatic: the environment, then the macOS system proxy), like model downloads, and a download whose connection drops resumes where it stopped.
- Importing a video from a link also uses that proxy: yt-dlp, started from the app, did not get it.

## 0.4.0-beta.3

### Dubbing
- Importing a video from a link works again for YouTube videos offered only as separate video and audio streams (most of them now): AudioSmith gives yt-dlp the ffmpeg it uses (or one found on this Mac) to join them. Downloads stop at 1080p. Without any ffmpeg the error says to set it up in Settings → Audio tools, instead of "Requested format is not available".
- When a step fails, the dub shows why that step failed, not "A job it depends on failed".

### Diagnostics
- Failed jobs (a dub step, a render, a transcription…) are written to the log, so Settings → Logs shows why.

### Licence
- The app ships THIRD-PARTY-NOTICES.txt with the full licence texts of the 45 components it includes; Settings → Diagnostics and about → Third-party licences opens it.

## 0.4.0-beta.2

### Voices
- Hear a voice before you pick it: every voice list (Studio, Stories, Audiobook, Dub) has a ▶ on each voice, ■ to stop. Built-in speakers play a short sample that ships with the app, so they play before their model is downloaded; your own voices play the first seconds of their sample. One preview at a time; closing the list stops it. Keyboard: P plays the highlighted voice.
- Stories, Audiobook and Dub use the same voice picker as the Studio.
- Kokoro's Japanese voices are hidden, like its Chinese ones: our engine does not speak them intelligibly yet.

### Licence
- AudioSmith is now licensed under the GNU Affero General Public License, version 3 (AGPL-3.0); earlier releases, up to and including 0.4.0-beta.1, remain under the Apache License 2.0. Settings → Diagnostics and about shows the licence and links to the source code; the Local API's `/`, `/health` and capabilities document carry `source_url` and `license`. The app ships LICENSE and NOTICE.
- Contributions need the Contributor License Agreement (CLA.md): a bot asks you to sign it on your first pull request, by a comment. See CONTRIBUTING.md.

## 0.4.0-beta.1

The first signed release is a beta (a GitHub prerelease): copies that receive beta updates get the next beta, and 0.4.0 once it is out.

### Updates
- AudioSmith updates itself: once a day it reads the version file of its latest release on GitHub and, when a new version is out, shows its notes (this file's section of the version) and offers to install it, skip it or remind you later. Updates are signed; AudioSmith checks the signature before it installs one, and usually downloads only what changed. AudioSmith → Check for Updates…, the command palette and Settings → Diagnostics and about check at once; that page also turns the daily check and installing without asking on or off. Copies built on your own Mac don't update themselves.
- Releases are signed with a Developer ID and notarized by Apple, so they open without Gatekeeper's warning (docs/release.md).
- Beta updates: Settings → Diagnostics and about → Receive beta updates. Betas get new features sooner but may be less stable; turned off, AudioSmith only updates to releases. It is on in a beta copy (such as this one) and off in a release until you choose. Turning it off never goes back to an older version: a beta stays until a release newer than it is out. About shows a Beta badge next to a beta's version.
- MyGo 0.5.5: MyGo's own texts (text fields' menus, date pickers, dialogs) follow the app's language, and Chinese text is drawn with the Chinese forms of its characters.

### Transcription with speakers
- New model "Transcription with speakers" (区分说话人转写; MOSS-Transcribe-Diarize 0.9B, 8-bit, 1.3 GB, Apache-2.0): it transcribes and tells who says what in one pass. Download it from the Models page, Transcribe or the Dub page; nothing downloads by itself.
- Transcribe: a Speakers option in the toolbar. Transcripts list their speakers (S1, S2…) with their lines; click one to name it. Copy and TXT export put one paragraph per turn after the speaker's name, SRT cues start with it, VTT marks voices, JSON has each segment's speaker. Dictation and plain transcription stay on Qwen3-ASR (MOSS is about 4× slower: a 10-minute recording takes about 4 minutes on a MacBook Air M4).
- Dubbing: with the model downloaded, a source with more than one speaker is transcribed by it and every line gets its speaker from it (in our tests 1.00 of the lines right on three of four 2–4 speaker dialogues and 0.93 on a 13-minute one with five people, where Qwen3-ASR + Sortformer managed 0.50–0.69 and never found more than 2 speakers; no limit of 4 speakers); the aligner still times the words, and the glossary's terms are its vocabulary. Without it, the dub works as before (Qwen3-ASR + Sortformer) and suggests the download, then Transcribe again.
- Long recordings go in 10-minute windows that overlap by a minute, linked by who speaks in the overlap; stretches without speech are skipped, and the model's chat answers to silence ("I'm sorry, I can't assist…") are dropped.
- Local API and MCP: `speakers=true`, `model=gpt-4o-transcribe-diarize` or `response_format=diarized_json`; segments carry `speaker`. MCP `transcribe` takes `speakers: true`.

### Fewer transcription and writing models; Qwen3-ASR transcribes everything
- Speech recognition is Qwen3-ASR 0.6B ("Chinese & English transcription") everywhere: dictation, Transcribe's Fast and Accurate modes, dubbing and its read-back check, workflows, Convert, clone references and calls. It made 0.5–4 % errors on Chinese where Whisper made 8–33 %, punctuates, and is 2–4× faster (thread #44's measurements). Whisper large-v3 turbo ("Multilingual transcription", tagged Fallback) is used on its own for the languages Qwen3-ASR does not know (it knows 30) and for translating speech into English. Fast and Accurate now differ only in the word timings.
- Long files: the vocabulary is only sent with recordings up to 30 s (Qwen3-ASR copied it into the end of longer transcripts); the " The." (or "嗯。") Qwen3-ASR sometimes invents after the last sentence of a long file is dropped; files longer than about 4½ minutes are timed word by word in windows of at most 4 minutes, cut between sentences (the aligner's times broke down after about 4½ minutes).
- Removed: Parakeet TDT 0.6B v3 (no Chinese) → Qwen3-ASR, or Whisper for a language Qwen3-ASR does not know; Mel-RoFormer (its output ended in 2–3.5 s of silence, and it was 5–9× slower) → HTDemucs; Gemma 4 E4B, Gemma 4 E2B and Qwen3 1.7B (slower or broken JSON, weaker translations and clean-up) → Qwen3 4B Instruct 2507, now the only language model, named in Settings → Writing help instead of a choice. Settings that named a removed model go back to the default once, with a notice; a model you chose that is still there (Whisper) stays. API and MCP: a transcription that names Parakeet is answered by its replacement with `replaced_by` and a warning (`X-AudioSmith-Replaced-By` / `X-AudioSmith-Warning`); `GET /api/models/{id}` describes the removed models, and they are not listed. Their files stay until Models → Clean up, which now also lists them (and shows on their tab).
- Onboarding: Balanced no longer downloads Whisper; Max adds it instead of Mel-RoFormer.

### Models named for what they do
- Every model has a plain name that says what it is for — Standard voices, High-fidelity clone, Fast clone, Voice design, Fast English voices, English clone, Multilingual all-rounder; Chinese & English transcription, Writing assistant · Standard… (标准音色、高保真克隆、快速克隆…) — a one-line description and a few tags (Chinese, Cloning, Built-in voices, Low memory, Non-commercial…). Pickers, the Models page, ⌘E, Settings, the bar beside Generate, History, onboarding, notifications and messages use them.
- The family, version, size, quantisation, repository and licence are still there: in a model card's Details on the Models page, in the pickers' tooltip and ⓘ, and in History's details (Technical name). Ids, repos and saved projects don't change.
- The model picker and the Models page have a search field that finds a model by its name, its technical name, id, repo or a tag.
- Local API and MCP: model lists add `display_name` (English), `ui_name` (as the app shows it), `description` and `tags`; a friendly name picks a model too.

### Portable voices, and simpler Design, Clone and Export
- Make a portable copy of a model's built-in speaker or of a designed voice (My voices → ⋯, the Studio's voice list, or Make portable where a voice can't be used): its model says a ten-second line, evened out, and the take is saved as a cloned voice "<name> (approximate)" that every model that clones can speak with. It sounds close to the original, not the same, and says so.
- When a voice can't be used with a model, the message says why and what to do (make a portable copy, or pick a model that designs).
- Design shows only what the model can't express; what it will hear is behind ⓘ, and the seed moved into Details. Clone: style and language are under Advanced. Export audio: format, loudness, file name and extras in view; bit depth or bitrate, sample rate, channels and trimming under Advanced.

### Simpler Stories, Audiobook and Dub
- Stories and Audiobook: the inspector keeps Setup, Cast, Performance (Stories) or Chapters and Cover & details (Audiobook), and one Advanced section for the rest — language, merging and evening out voices, refinement and guidance, gaps, trimming, seed, a stem per character, a story's M4B with its cover and details, an audiobook's own pronunciations — with N changes and Restore recommended (your pacing stays).
- The Project section moved to a Project ▾ menu in the header: Save (under the title — the title is the project's name), Save a copy, New, recent projects, All projects…
- A story shows only what its model hears: personas, the scene, the AI director and line directions disappear on models that say lines as written, and a line's speed on models without one.
- Dub: each step keeps its main choices; the rest folds into Advanced at the foot of the step (speakers and review, more languages and the translator's options, voice match, the voice model's parameters, a WAV's bit depth). The buttons the editor's ⋯ menu already had left the panels; Transcribe again… and Translate everything again joined that menu.

### A simpler Studio
- The inspector shows what most takes need: the model, one Voice list (the model's speakers, your saved voices, a clip to clone, Manage voices…) and the speed. Everything else — style, reference transcript, length, refinement, guidance, sampling, seed, reading numbers, evening out the volume — is under Advanced, filled with the model's recommended settings; its header says "Recommended settings" or how many you changed, with Restore recommended inside. Advanced stays open or closed as you left it.
- The Voice controls and Audio quality buttons are gone, and so are the WAV precision (takes are kept at 24-bit; choose the file's bit depth in Export audio), Preload / Unload (in the ⌘E engine switcher, now with Unload), the autoplay switch (Settings → Studio) and the steps slider (three tiers remain).
- A clip chosen to clone is transcribed for you when it is a WAV.
- The bar beside Generate says which model and voice will speak, and how many settings you changed; a click shows the inspector.

### Recommended models and settings
- Every speech model was measured on a MacBook Air (intelligibility with two recognisers, naturalness, voice stability, speed, memory). Each page now recommends two or three models for its job and your language, with a one-line "best for" and a speed and quality hint; the others are one click away under "Show all models", and the ones that can't do the job (no cloning for a dub) are greyed out with the reason.
- Tuned defaults: Qwen3-TTS uses temperature 0.7 and top-p 0.8 unless you (or an API request) set them — the models' own 0.9 / 1.0 sounded worse and less stable; OmniVoice uses guidance 1.5 with 32 steps (16 for dubbing). The Local API and MCP tools follow the same defaults. Rendered chunks from before are made again once with the new settings.
- Removed: six speech models that misread, paused or covered too little in the tests — KittenTTS mini, MOSS-TTS-Nano, Chatterbox (multilingual), Marvis, Soprano and Pocket TTS. Seven remain: Qwen3-TTS 0.6B CustomVoice, 0.6B Base, 1.7B Base and 1.7B VoiceDesign, Kokoro, Chatterbox Turbo and OmniVoice (under Show all models). Drafts, projects, dubs, workflows, settings, history and API or MCP calls that name a removed model use its replacement — Kokoro, Qwen3-TTS 0.6B Base for MOSS-TTS-Nano, Chatterbox Turbo for Chatterbox and for cloning with Marvis, Soprano or Pocket TTS — and the app says so once; the API answers with `replaced_by` and a warning. A removed model's own speakers saved in My voices can't be used any more.
- Models → "Removed models still take X GB": Clean up deletes their files (after listing them); nothing is deleted on its own.
- Chatterbox's expressiveness control is gone with the multilingual model (Turbo does not take it).
- New dubs fit lines with "Smart fit" instead of "Lip sync": lip sync sped most Chinese lines up audibly (1.2–1.4×). Dubs you already made keep their timing.
- Kokoro's six Chinese voices are hidden (unintelligible in the tests); its English voices are the best English default.

### Stories: dialogue that flows
- Turn-aware pacing: the silence between lines follows the conversation — shorter when a character goes on or answers a question, a little longer after a statement, shortest when a line is cut off (… —), longer around narration. Three presets (Dialogue, Reading, Relaxed) in the new Performance section; new stories use Dialogue, stories made before keep their fixed gap until you pick a preset. A line's own "pause after" still wins.
- Consecutive lines of one character are said in one go, so the voice flows from one sentence into the next, then split back into lines at their pauses (player, stems and the sidecar see lines as before; a turn that cannot be split is made line by line). The pause inside a turn is kept close to the preset's.
- Each character's lines are brought toward one loudness (measured 4–7 LU apart on Qwen3-TTS before, about 1 after).
- A scene for the story, a persona for each character and a direction (with an intensity) for each line. Qwen3-TTS CustomVoice and VoiceDesign hear them as an instruction with every line; other models say lines as written, and the page says so ("Line by line"). Never spoken.
- AI director: the writing-help language model (Qwen3 4B on this Mac by default) reads the story and fills in the scene, personas and directions; edit any of them, Undo, or direct again.
- Imported scripts keep stage directions out of the spoken text: `妈妈（温柔地）：…`, `MOM (softly): …`, `[Mia] (pleading) …` become the line's direction.
- Agents: `update_longform` sets a story's scene, personas, directions, intensities and pacing, and `direct: fill | all` runs the AI director; `get_longform` shows them with the model's continuity level.
- The catalogue marks Qwen3-TTS CustomVoice and VoiceDesign as taking instructions, as the engine reports them; dubs and stories ask one predicate.

### Export audio
- History's takes have an Export button (and Export… in ⋯); the Studio's latest take, a take's details and Projects open the same Export audio dialog, which replaces the Save as… menu.
- Choose the format (WAV; M4A through the engine; MP3, FLAC and Opus with ffmpeg, greyed out with a pointer to Settings → Audio tools without it), the bit depth or bitrate, the sample rate and the channels; bring the file to −16, −18 or −23 LUFS with a true-peak ceiling; trim the silence at both ends; name the file and its title and artist tags; save the script and subtitles beside it. The dialog shows the size and length before you export, and remembers your choices for each kind of take (Studio, voice preview, converted recording, Local API).

### Writing help no longer fails during a render
- The language model on this Mac now waits for its turn when a render, dub or batch is using the engine, instead of failing with "the engine is busy": writing help, the AI director, dub translation, transcript clean-up, the direction coach, phone calls and API/MCP requests go next, ahead of queued batch work (a running job is never interrupted). These short requests don't appear in the jobs list. Their time limits count from when the model starts.
- Dictation never waits for a render: while one runs, it types the polished text at once without the model's clean-up. The Tools direction coach shows 「等待当前任务完成…」 (Waiting for the current job to finish…) and, if the job is still running after 30 s, reads the note by keywords.

## 0.3.0 — 2026-10-14

A new look for every page: flat, quiet and black-white-grey, in the spirit of Typeless. Nothing was removed; some actions moved into menus. Your library, voices and settings carry over unchanged.

### The new look
- One dark (ink) button per page for its main action — Generate, Create audiobook, Transcribe, Run workflow, Export — and quieter buttons for everything else.
- Colour only where it means something: a teal accent for state (selection, progress, the played part of a waveform, switches) and red-orange for recording and deleting.
- New icons everywhere (Solar), including the sidebar, the icon rail, the status bar and the dictation pill.
- Flat cards with a hairline or a light grey fill, round play buttons, grey filter chips and tags.

### Calmer pages
- Secondary actions are in "⋯" menus and drop-downs (Save ▾, Insert ▾, Add ▾, Projects ▾); help text is behind ⓘ tooltips; results show as small toasts in the page header, where they no longer cover buttons.
- Inspectors and lists open beside the page when there is room, or as drawers over it in a small window, with the same panel button on every page.
- Nothing overflows at the smallest window (820×520) or with 125 % text: toolbars drop their labels, search fields and filters move to two lines, long names end in "…".
- Notes on a page (warnings, finished steps) are one-line notices you can dismiss, instead of coloured boxes.

### Settings
- Settings opens as a dialog over the page you are on (⌘,), with its sections on the left; the page keeps its state behind it.
- The accent colour choice is gone (the accent is teal); theme, text size, icon rail and reduce motion stay.

### Pages
- Studio: the script fills the page; Voice controls and Audio quality open as popovers; the inspector can be hidden.
- Stories and Audiobook: flat line rows, an inspector with sections that fold, a ready card like the Studio's with one Save menu (whole book, one WAV per chapter, chapter times, stems). The read-along shows Chinese text without spaces between characters.
- Dub: a quiet step bar, the step's settings as a side panel, one main button at the foot for the current step.
- Voices, Gallery, Design, History, Projects, Transcribe, Batch, Workflows, Calls, Tools, Integrations, Models, Engine: the same header, search and card patterns; Workflows' zoom buttons stay visible next to the step panel.
- The first-run wizard has step breadcrumbs and a single Continue button.

## 0.2.0 — 2026-10-14

The first build meant to be installed and used: everything below runs on the Mac, offline once its models are downloaded. It follows the open-source edition of VoiceStudio feature by feature (no cloud, no account).

### Studio
- Script editor with markup: pauses, emphasis, `[voice:…]` switches, `[slow]`/`[fast]`, non-verbal tags (`[laughter]` on OmniVoice); imports TXT, Markdown, EPUB and DOCX.
- Models: Kokoro, Qwen3-TTS (CustomVoice, Base, VoiceDesign), OmniVoice, Chatterbox, Marvis, Soprano, Kitten, Pocket TTS, MOSS-TTS Nano; controls follow the model (speed, sampling, seed, target duration, steps, guidance).
- Long scripts in chunks with a streaming preview and a chunk cache (a re-render only redoes what changed).
- Mastering ("Even out volume", −18 LUFS), pronunciation dictionary, audio check (clipping, clicks, long silences).
- Save as WAV 16/24/32-bit (MP3, FLAC and Opus with an ffmpeg on the Mac), drag out, Show in Finder; History with search and stars.
- An inaudible AudioSeal watermark on generated speech (Settings → Privacy; Tools → Watermark checks a file).

### Voices
- Clone from a recording or a file: best part picked, automatic transcript, noise cleaning, consent statement.
- Design a voice from a description (Qwen3-TTS VoiceDesign, OmniVoice tags) with a seed for its identity.
- Gallery of ~1,100 archetypes with previews; portraits, tags, favourites; `.asvoice` export/import and VoiceStudio `.ovsvoice` import.

### Stories, Audiobook, Projects
- Stories: multi-voice scripts, Auto-cast of screenplay-style text, a cast per character, line auditions.
- Audiobook: chapters from the manuscript, ACX or podcast loudness per chapter, read-along with word timings, M4B with chapters and cover, stems per character, cue sheets; resumes after a quit and re-renders only edited paragraphs.
- Projects: everything made and exported, searchable.

### Dubbing and batch
- Dub a video or audio file: separation of speech and background, speaker diarization (up to 4), transcription with word timings, translation (local language model, Ollama/LM Studio, cloud LLMs, DeepL, Google, Microsoft), voice-cloned lines fitted to the timing, review checkpoints, timeline editor, A/B player, video preview.
- Exports: MP4/MOV with the original and dubbed audio tracks and soft subtitles, WAV, M4A, SRT/VTT/ASS (dual), stems, lines.
- Batch queue and a watch folder; links via yt-dlp (opt-in).

### Transcribe and dictation
- Transcripts with word times and speakers (Whisper large-v3 turbo, Qwen3-ASR, Parakeet; Qwen3 forced aligner); exports SRT, VTT, TXT, DOCX, JSON; clean-up with a language model.
- System-wide dictation (⌘⇧Space, toggle or hold) that types into the app in front, with a pill, the menu bar icon and close-to-tray.

### Local language model
- Qwen3 4B Instruct runs in the engine for offline translation and writing help (Gemma 4 E2B as the light choice); automatic routing local → Ollama → cloud only when chosen.

### Local API, MCP, integrations, tools
- OpenAI-compatible `/v1` on 127.0.0.1:3901: speech (wav, pcm, aac; mp3/opus/flac with ffmpeg; SSE), transcriptions, translations, models, voices; native `/api` with job events and WebSocket streaming; OpenAPI reference at `/docs`; optional key, LAN sharing with a PIN.
- MCP over HTTP (`/mcp`) and stdio (`AudioSmith --mcp-stdio`): generate_speech, clone_voice, design_voice, transcribe, list_voices and more.
- Setup pages for Claude Code, Cursor, Codex CLI, n8n and the OpenAI SDKs; tools: A/B voices, Convert, Rate fit, Direction, File probe, Watermark.

### Workflows
- A canvas of steps (scripts, clips, speak, transcribe, translate, convert voice, normalize, condition, export) run as resumable jobs, with six templates.

### AI phone calls (optional)
- An agent that places and answers calls over your own Twilio account (through a tunnel such as cloudflared), with a phone-quality voice, take-over, transcripts and summaries. Inert until configured.

### App
- First-run wizard: a check of the Mac, a model pack chosen by memory (Fast, Balanced, Quality, Max), privacy, dictation.
- Sidebar in sections with an icon rail, ⌘1–9, command palette (⌘K), shortcut sheet (?), status bar with engine, jobs and downloads, notifications, logs.
- Settings: appearance, models and downloads (hf-mirror.com, proxy), engine memory budget, storage (sizes, clear caches, move the data folder, erase), usage, diagnostics (self-check, bug report bundle), privacy.
- One persistent job queue: generation, renders, dubs, transcripts, workflows and downloads continue after a restart.
- Upgrades the library of earlier builds (the MVP's `history.json`, databases from migration 1 on; a backup `audiosmith.db.bak-<n>` is kept before each migration).
- UI in English and Simplified Chinese.

### Fixed in the release check
- M4B export no longer hangs on books longer than a few seconds per chapter (the encoder waited for the chapter track forever); a stalled encoder now fails the job after a minute instead.
- Qwen3-ASR and the forced aligner no longer keep a second full copy of themselves in the model folder (about 1.7 GB); copies left by earlier builds are replaced by links, and full copies of models that now load in place (Kokoro, Qwen3-TTS, Whisper) are removed.
- Kokoro no longer contacts huggingface.co for its English phonemizer at every load.

### Known limits
- **Ad-hoc signed, not notarized**: macOS blocks the first launch. Remove the quarantine (`xattr -dr com.apple.quarantine /Applications/AudioSmith.app`) or allow it in System Settings → Privacy & Security → Open Anyway. No automatic updates.
- **macOS 15 or later on Apple silicon only** (M1 or newer). 8 GB Macs: Fast pack only; 16 GB recommended for cloning and dubbing, 24 GB for the Quality pack.
- **MP3, Opus and FLAC export, WebM/MKV import and burnt-in subtitles need an ffmpeg on the Mac** (e.g. `brew install ffmpeg`, then Settings → Audio tools). WAV, M4A/AAC, M4B, MP4 and MOV work without it.
- **Diarization finds at most 4 speakers.**
- **Dictation is not streaming**: the text appears after you stop (about 0.3 s for a short sentence once the model is loaded).
- **Speech streams per sentence**, not per audio frame (the API's first audio comes after the first sentence is synthesized).
- **UI languages: English and Simplified Chinese.**
- **AI calls need your own Twilio account and a public tunnel**; call audio is not watermarked.
- **Model licences**: OmniVoice is CC-BY-NC (non-commercial; the app asks before downloading it). Marvis' card says Apache-2.0 but it was trained on non-commercial data.
- PDF import is not available yet; the watermark does not survive time-stretching beyond about ±5 %.
- Workflows are not exposed through the Local API yet; the workflow editor has no undo.

## 0.1.0

The MVP: Kokoro and Qwen3-TTS takes from a single window, history in `history.json`.
