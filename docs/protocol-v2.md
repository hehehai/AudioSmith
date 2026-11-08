# Engine protocol v2

How AudioSmith (Go) talks to `audiosmith-engine` (Swift). This spec is the contract between the two sides.

| Side | Code |
|---|---|
| Go wire types | [`internal/engine/proto`](../internal/engine/proto) |
| Go supervisor (processes, handshake, routing, restarts) and job executors | [`internal/engine`](../internal/engine) (`supervise.go`, `executors.go`) |
| Swift wire types (`Codable`, no dependencies) | [`engine/Sources/AudioSmithProtocol`](../engine/Sources/AudioSmithProtocol) |
| Swift engine (session, jobs, models) | [`engine/Sources/AudioSmithEngine`](../engine/Sources/AudioSmithEngine) (`Server.swift`, `Synthesizer.swift`) |
| Golden transcripts, decoded by both sides | [`engine/Fixtures/`](../engine/Fixtures) |

Protocol v1 (the MVP: flat `cmd`/`event` objects, one job, cancel without an id) is gone from both sides since M0; §9 explains how a stale v1 engine is recognised.

## 1. Goals

- **Many jobs, addressed by id.** Every long-running request is a job. Its id is the request id; progress, streamed audio, its answer and cancellation all refer to that id.
- **Fail loudly on mismatch.** A handshake checks the protocol version and the role before anything else runs.
- **Typed errors** the app can act on: download the model, accept a licence, retry, report a bug.
- **Streaming.** Audio chunks arrive while a take is still generating.
- **Stateless engine.** The engine holds models and runs computations. Queues, persistence, licences and the library live in Go.

## 2. Transport

| Item | Rule |
|---|---|
| Process | One Swift binary started as `audiosmith-engine --role gpu\|listen\|media`. Without `--role`, the role is `gpu`. The app starts `gpu` with the window, and `listen` and `media` with the first request for them (`engine.Config.OnDemand`). One-shot CLI flags (`--text …`) still work for smoke tests. |
| Channels | The app writes requests to the engine's **stdin**. The engine writes responses and notifications to its **stdout**. **stderr** is free-form text that the app appends to `engine-<role>.log` and keeps the last 40 lines of, for crash reports. |
| Framing | One JSON object per line, UTF-8, ended by `\n`. The JSON encoder must escape newlines inside strings, which every JSON encoder does by default. Max line: 4 MiB (`proto.MaxLine`). |
| stdout hygiene | stdout carries only protocol lines. The engine dups the real stdout for itself and points fd 1 at stderr, so stray prints from libraries can't corrupt the stream (the v1 engine already does this). |
| Paths | Files are passed as absolute paths. The app owns every directory it names. Inputs are read-only; outputs are written to a temporary name in the same directory, then renamed. |
| End | When stdin closes, the engine cancels its jobs and exits 0. `engine.shutdown` does the same after it answers. If it does not exit within 5 s, the app kills it. |
| Environment | The app may add variables (`engine.Config.Env`), e.g. proxies. `HF_TOKEN` is used for downloads while the engine still downloads (§4.7). `AUDIOSMITH_MAX_MODELS` caps resident models (default 6; models under 512 MB do not count; memory decides first). |

## 3. Messages

There are three shapes, all JSON-RPC 2.0 in form:

```text
request       {"id":"…","method":"…","params":{…}}          app → engine
notification  {"method":"…","params":{…}}                   engine → app (none app → engine in M0)
response      {"id":"…","result":{…}}                       engine → app
              {"id":"…","error":{"code":"…","message":"…","data":{…}}}
```

Differences from JSON-RPC 2.0:
- there is no `"jsonrpc"` member;
- **ids are strings**;
- **error codes are strings** (§7);
- the engine never sends requests.

`params` may be omitted when empty. `result` is always present on success; it is `{}` when there is nothing to return.

Ids are chosen by the app and are unique among the requests in flight to one process.
- **Job requests** use the scheduler's job id, a ULID, so logs, notifications and the database all correlate.
- **Control requests** use `c1`, `c2`, …

Unknown members must be ignored by both sides, so optional fields can be added within protocol 2. Missing optional members take their documented default.

## 4. Session and sequencing

1. **Handshake.** The app sends `engine.hello` right after spawning. Until it is answered, every other request fails with `not_initialized`.
   - If `params.protocol` is a version the engine does not speak, the engine answers `protocol_mismatch` with `data.supported`.
   - The app refuses an engine whose `result.protocol` ≠ 2 or whose `result.role` differs from the role it spawned. It shows "Engine version mismatch: rebuild audiosmith-engine" and does not restart it.
   - The handshake has a 30 s timeout. The first launch compiles Metal shaders.
2. **Two kinds of request.**
   - **Job requests** (`models.load`, `tts.synthesize`, `tts.encodeVoice`, `tts.dialogue`, `stt.transcribe`, `stt.align`, `vad.detect`, `enhance.run`, `media.decode`, `capture.start`; `proto.JobMethods`) run long, may send notifications, and can be cancelled.
   - **Control requests** (all others) answer promptly, even while jobs run, and cannot be cancelled.
3. **Concurrency.** The engine runs at most `hello.result.concurrency` jobs at once (`gpu` 1, `listen` 2, `media` 2). Another job request gets `busy`, which is retryable.
   - The Go scheduler is the only queue: it never sends more jobs than the engine can run.
   - The engine does not queue jobs.
   - A job's slot is free **before** its response (result or error) is written, so a job sent once the app has read the previous response is never answered `busy`. (A slot freed after the response would race with the scheduler starting the next job at once.)
4. **Ordering.**
   - All of a job's notifications are written **before** its response.
   - Nothing about a job is written after its response.
   - Responses to different ids may come in any order.
   - `tts.chunk` `seq` starts at 0 and has no gaps.
5. **Exactly one response.** Every request gets exactly one response, unless the process dies first. When it dies, the app fails all calls in flight as *interrupted* (`engine.ErrWorkerExited`).
6. **Cancellation.** `job.cancel {job}` answers at once with `{cancelled: true}` if the job was running, or `false` if it is unknown or already finished.
   - The job itself then ends with error `cancelled`.
   - The engine checks for cancellation between generation chunks, as v1 does.
   - A job ends only when its computation has: the engine must not take the next job while a cancelled one still runs on the GPU. Models that make a whole take in one call (Kokoro, Kitten) run in the job's own task, so cancelling them waits for the take (about 2 s for 25 s of audio on an M4). Streaming models stop at their next step; the engine waits 200 ms for it.
   - The app's supervisor gives a cancelled job 5 s to end, then kills and restarts the worker.
7. **Models.** Job methods load the model they need if it is not resident, reporting stage `loading`.
   - With `hello.offline: true` they never download: missing files fail with `model_not_downloaded`, and the Go hub owns downloads.
   - The app sends `offline: true` (since integration I5). The engine executors fill each job's local snapshot directory (`models.load` `path`, `tts.synthesize` `modelPath`, from `hub.Status` over the catalogue's pinned commit) as the job starts; a model that is not downloaded fails the job with `model_not_downloaded` before the engine sees it.
   - `models.load` with a `path` (and `tts.synthesize` with a `modelPath`) loads that snapshot directory.
   - Families mlx-audio-swift cannot load from a directory (Marvis, OmniVoice, Spark in the pin) fall back to resolving the repo id in `hello.cacheDir`, where the hub put the same snapshot (standard layout; for a pinned commit it also writes `refs/main`, so swift-huggingface finds it without the network). Auxiliary repos (Kokoro's G2P) are always resolved that way.
   - Without a path, the engine resolves the repo in `hello.cacheDir` (it sets `HF_HUB_CACHE` to it). A repo counts as present when `<cacheDir>/mlx-audio/<owner>_<name>/config.json` exists or `<cacheDir>/models--<owner>--<name>/snapshots/` has a non-empty snapshot.
   - Without a path, a job loads the repo's snapshot only when there is exactly one (non-empty, not `<commit>.incomplete`); with several it fails with `model_path_required`. The cache is shared with other apps (the ActNow family spec §7.1), which may keep another commit of the same repo there, so the engine never picks one.
   - Any model job may also carry `auxPaths` (`{repo id: snapshot directory}`, Go `proto.ParamAuxPaths`): the snapshots of the model's auxiliary repos at their pinned commits (Kokoro's G2P repos, Chatterbox's S3 tokenizer, Whisper's tokenizer), which the executors fill with the path (`hub.AuxSnapshots`): the engine job kinds (`engine.RegisterExecutors`) and the app's own engine calls (renders' `tts.synthesize`, transcripts, dub and workflow steps: `engine.Executor` / `KindExecutor` with `ExecutorOptions.AuxPaths`). mlx-audio-swift finds those repos by id in its folders `mlx-audio/<owner>_<name>` (`…_tokenizer_only` for Whisper's tokenizer); the engine links them to these snapshots before the job (`AuxLinks.swift`), so another app's commit of the repo is never used. A directory that is not there fails the job with `model_not_downloaded` (`data.model` is the auxiliary repo).
   - The engine keeps several models resident (`AUDIOSMITH_MAX_MODELS`, least recently used first out). The supervisor polls `engine.stats` while jobs run and after each, and unloads the least recently used idle models when all workers' models together exceed `engine.Config.MemoryBudget`. It also unloads, on every worker (the listen worker's dictation model included), models idle for longer than `engine.Config.IdleUnload` (setting `models.autoUnload`, 30 minutes by default since #109; 0 = never); `Supervisor.Hold(roles…)` keeps a worker's models while a call is live; `Supervisor.HoldModels(role, models…)` keeps only those models (a dictation pins its recogniser and, with clean-up on the engine's language model, that model; not the gpu worker's others).
   - Before every load the gpu worker makes room in its budget for the model's weights (`makeRoom`, size = the `*.safetensors` in its folder and the folders under it; every task passes it since code review 1102 M1: TTS, separation, diarization, aligner, VAD, denoiser, speech-to-text, LLM, dialogue).
   - **Memory pressure.** The gpu worker watches the system's memory pressure (`DispatchSource.makeMemoryPressureSource`). From `warning` up it unloads every model no job uses (at `warning` not those the app holds, `engine.config`) and clears MLX's buffer cache; at `critical` it also refuses to load a model (`oom`, `data:{reason:"memory_pressure", level, model}`, retryable) until the kernel's level (`kern.memorystatus_vm_pressure_level`, read at each load) falls. It sends `engine.memory` at each change; the supervisor then unloads the other workers' idle models too (holds apply) and polls `engine.stats`. `AUDIOSMITH_MEMORY_PRESSURE=normal|warning|critical` forces the level loads go by, and with `AUDIOSMITH_MEMORY_PRESSURE_SIGNALS=1` SIGUSR1 plays a `warning` event and SIGUSR2 a `normal` one (tests: simulating the system's pressure needs root).

## 5. Requests

Every field is listed in [`methods.go`](../internal/engine/proto/methods.go). The fixtures show every method on the wire.

### `engine.hello` (control)

```json
→ {"id":"c1","method":"engine.hello","params":{"protocol":2,"app":"0.2.0","role":"gpu","cacheDir":"/Users/me/.cache/huggingface/hub","offline":true,"logLevel":"info"}}
← {"id":"c1","result":{"protocol":2,"version":"0.2.0","role":"gpu","pid":4242,"concurrency":1,
   "methods":["engine.hello","engine.stats","engine.flush","engine.shutdown","models.load","models.unload","models.loaded","tts.synthesize","job.cancel"],
   "families":["kokoro","qwen3_tts","marvis","soprano"],
   "device":{"name":"Apple M2 Pro","memory":34359738368,"os":"macOS 15.1","mlx":"0.31.6"}}}
```

- `methods` lists the requests this build and role implement. The app hides features whose methods are missing.
- `families` are the model families it can load. They match `catalog.Entry.Family`. The M0 engine reports `kokoro`, `kitten`, `qwen3_tts`, `qwen3`, `marvis`, `soprano`, `omnivoice`, `chatterbox`, `pocket_tts`, `llama_tts`, `fish_speech`, `echo_tts`, `irodori_tts`, `moss_tts`, `moss_tts_nano`, `indextts`, `spark`, `breeze` (role `gpu`; other roles report none).
- Roles: every role implements `engine.hello`, `engine.stats`, `engine.flush`, `engine.shutdown` and `job.cancel`. On top of those (`proto.RoleOf`):
  - `gpu`: `models.*`, `tts.synthesize`, `tts.encodeVoice`, `tts.dialogue`, `stt.transcribe`, `stt.align`, `vad.detect`, `enhance.run` — everything that runs an MLX model;
  - `media`: `media.probe`, `media.decode` — AVFoundation, no MLX; concurrency 2;
  - `listen`: `capture.devices`, `capture.start`, `capture.stop` — the microphone; and, for dictation, `stt.transcribe` and `models.load`/`unload`/`loaded` with **one resident speech-to-text model of its own** (concurrency 2: a recording and a transcription), so dictation never waits behind a gpu render. The app runs those as the job kinds `proto.KindFor(proto.RoleListen, method)` (`"listen:stt.transcribe"`, `"listen:models.load"`) on `jobs.LaneListen`; `services.TranscribeRequest.Role = proto.RoleListen` does it for a transcript.
  A method sent to another role is answered `method_not_found`.
- **Why capture lives in `listen`.** A recording is long and interactive. In the `listen` process it survives the gpu worker being restarted (OOM, a stuck cancel) or busy with a long render, and M2's dictation, which uses the same microphone, runs in the same process. `media` does batch file work that may run two at a time; mixing a live device into it would make its concurrency mean nothing.

### `engine.stats` (control)

Returns:
- MLX memory (`active`, `cache`, `peak`, in bytes);
- the resident models, each with `idleSeconds` (for LRU eviction);
- the ids of running jobs.

The supervisor polls it while jobs run, and on demand for the ⌘E switcher.

### `engine.config` (control, gpu and listen)

```json
→ {"id":"c7","method":"engine.config","params":{"memoryBudget":20000000000,"held":["mlx-community/Qwen3-4B-Instruct-2507-4bit"]}}
← {"id":"c7","result":{"memoryBudget":20000000000,"held":["mlx-community/Qwen3-4B-Instruct-2507-4bit"]}}
```

The app's side of the worker's memory (code review 1103 F4/F5; Go `proto.EngineConfigParams`). `memoryBudget` is what `makeRoom` keeps to before a load (the memory limit setting, or the ActNow family's share; `0` = the engine's default, 60 % of the Mac's memory or `AUDIOSMITH_MEMORY_BUDGET`); `held` the models the app holds (`HoldModels`), `holdAll` every model of the worker (`Hold`, a live call). A memory-pressure `warning` leaves held models, `makeRoom` drops them only when the others are not enough; `critical` drops them too. A missing field keeps what the engine had; the result is what applies. The supervisor sends it after `engine.hello` (before the worker is ready), and whenever the budget or the holds change. Workers that list it in `hello.methods` get it.

### `engine.flush` (control)

Drops MLX caches (`MLX.GPU.clearCache()`) and returns `{}`.

### `engine.shutdown` (control)

Cancels running jobs, answers `{}`, then exits 0.

### `models.load` (job)

```json
→ {"id":"01JA…","method":"models.load","params":{"model":"mlx-community/Kokoro-82M-bf16","path":"/…/snapshots/0123abcd","task":"tts"}}
← {"method":"job.progress","params":{"job":"01JA…","stage":"loading","message":"Loading Kokoro-82M-bf16"}}
← {"id":"01JA…","result":{"model":"…","task":"tts","sampleRate":24000,"seconds":1.5,"bytes":390000000,"voices":["af_heart",…],"capabilities":["speed","stream"]}}
```

The result confirms or extends the catalogue entry: sample rate, the voices and languages the model reports, and capabilities named as in `catalog.Capability`.

### `models.unload` (control)

Takes `{model}` to unload one model, or `{}` to unload all. Returns `{unloaded:[…]}`. With `idleAtLeast` (seconds) only models no job uses and unused for at least that long are dropped: the supervisor's idle and budget unloads send the idle time their last `engine.stats` showed, so a model a job took since stays (`unloaded` then lacks it). After dropping a model the engine clears MLX's buffer cache.

### `models.loaded` (control)

Returns `{models:[…]}`.

### `tts.synthesize` (job)

```json
→ {"id":"01JA…","method":"tts.synthesize","params":{"model":"mlx-community/Kokoro-82M-bf16","modelPath":"/…/snapshots/a71e4d38…","text":"Hello from AudioSmith.","voice":"af_heart","speed":1.25,
   "output":{"path":"/…/clips/01JA….wav","format":"wav","bits":16},"stream":true}}
← job.progress {stage:"loading"} … {stage:"generating", fraction, audioSeconds} … {stage:"writing"}
← tts.chunk {seq:0 …} … {seq:n, final:true}          (only with "stream":true)
← {"id":"01JA…","result":{"output":"…","model":"…","sampleRate":24000,"channels":1,"frames":19200,"audioSeconds":0.8,"seconds":0.2,"rtf":4}}
```

Parameters left unset use the model's defaults:
- `speed`, `temperature`, `topP` and `seed` are pointers on the Go side;
- `maxTokens`, `steps`, `instruct` and `duration` count as unset when absent.

Qwen3-TTS (VoiceDesign, CustomVoice, Base) stops a take after `max(75, text tokens × 10)` codec frames (12.5 a second: 0.8 s a token) when `maxTokens` is unset; a `maxTokens` replaces that limit, raising or lowering it, up to 4,096 frames (327 s) (engine patch 0008, thread #93; upstream's × 6 cut slow voices off).

OmniVoice takes its own parameters through the same fields: `steps` (diffusion steps, default 32), `duration` (target seconds; the take comes out that long), `speed`, `seed`; `voice` is a design description ("female, young adult, british accent"), and `refAudio`/`refText` clone. It runs in the job's task and stops at the next diffusion step when cancelled.

What `voice`, `instruct` and the clone fields mean, by model (catalogue capabilities in brackets):

| Field | Meaning |
|---|---|
| `voice` | A built-in speaker id (Kokoro `af_heart`, CustomVoice `Vivian`). For design models [`design`, `voiceStyle`] — Qwen3-TTS VoiceDesign (free text, English or Chinese) and OmniVoice (attribute tags) — the voice description, as `voicedesign.Lowered.Voice` makes it. CustomVoice also takes `"Speaker, instruction"`. |
| `instruct` | A style or emotion instruction [`instruct`]. CustomVoice appends it to the speaker (`Vivian` + `用非常开心的语气说`); design models use it as the description when `voice` is empty. |
| OmniVoice `voice` | Its attribute tags, normalised by the engine as OmniVoice's own Python does: items split on `,`/`，`; a Chinese dialect makes every item Chinese and an English accent every item English, otherwise Chinese text makes them Chinese (`female, young adult` + Chinese text → `女，青年`). Unknown items and a second item of one category are dropped with a `log` warning instead of failing the take. |
| `language` | The catalogue's `Language.Value` for the model ("Chinese", "en"…). |
| `refAudio`, `refText` | Clone reference and its transcript [`clone`]. Any format AVFoundation reads. A model that only clones (catalogue `needsReference`; none is listed since thread #43 removed MOSS-TTS-Nano) fails without `refAudio` with `bad_request` ("MOSS-TTS-Nano-100M only clones voices: give it a reference clip (refAudio)"); the app does not send such a request (`core.NeedsReferenceError`, render jobs fail with `needs_reference`). |
| `voicePrompt` | A file `tts.encodeVoice` wrote for this model [`voicePrompt`]. It wins over `refAudio`. If it cannot be used (made for another model, an older format), the engine logs a warning and uses `refAudio`; without `refAudio` it fails with `bad_request`. The app therefore sends both. |
| `guidance` | Classifier-free guidance scale [`guidance`]: OmniVoice `guidance_scale`, Chatterbox (multilingual, removed in thread #43) `cfg_weight`. |
| `exaggeration` | Chatterbox's (multilingual) emotion exaggeration [`exaggeration`]. The app no longer sends it: the model was removed (thread #43) and Turbo ignores it. |
| `steps`, `duration` | Diffusion steps [`steps`], target seconds [`duration`]. |

**Text limits.** A model whose input is bounded per request (Kokoro: 510 phonemes) fails an over-long text with `input_too_long`, `data: {"limit", "actual", "unit": "phonemes"|"chars"|"tokens"}`. The app splits scripts into chunks below `catalog.Entry.MaxChars` (measured with `catalog.TextLength`), so this error means a chunk was still too long.

A model that cannot honour a parameter ignores it, unless that parameter is the point of the request: `refAudio` on a model that cannot clone fails with `unsupported`, `data.capability:"clone"`.

`result.seed` is the seed actually used, so the take can be reproduced: the engine seeds MLX's generator for every take, with a random seed when `seed` is unset.

`output.bits` is 16 or 24 (PCM) or 32 (IEEE float); the file is mono WAV. With `stream`, chunks hold up to 1 s of audio each, and a last empty chunk carries `final:true` (the engine only knows the take ended after the last audio). The chunks' samples add up to `result.frames`.

### `job.cancel` (control)

Takes `{job}` and returns `{cancelled:bool}`. See §4.6.

## 5.2 Methods added in M1

Types: [`media.go`](../internal/engine/proto/media.go) (Go), [`WireMedia.swift`](../engine/Sources/AudioSmithProtocol/WireMedia.swift). Fixtures: `stt.jsonl`, `analysis.jsonl`, `media.jsonl`, `capture.jsonl`, `voice.jsonl`.

Shared rules:
- **Inputs** (`audio`, `input`, `refAudio`) are absolute paths to anything AVFoundation reads: WAV, AIFF, CAF, MP3, M4A/AAC, ALAC, FLAC, and the audio of MOV/MP4/M4V. The engine converts to what the model needs. A missing file fails with `file_not_found` (`data.path`), an unreadable one with `unsupported_media` (`data.path`).
- **Outputs** are WAV (`output` as for `tts.synthesize`: `path`, `bits` 16/24/32f), written to a temporary name and renamed. Results describe them with `output`, `sampleRate`, `channels`, `frames`, `audioSeconds` (`proto.AudioOut`).
- **Times** are seconds from the start of the input.
- **Language codes are BCP-47** (`internal/lang`: `en`, `zh`, `yue`, `pt-BR`); the engine maps them to the model's names. (`tts.*` keep the catalogue's `Language.Value`.)
- **Model jobs** take `model` and `modelPath` like `tts.synthesize`; the executors fill `modelPath` and check licences, and the engine reports stage `loading` when it loads.
- **Stages** of `job.progress`: `transcribing`, `aligning`, `detecting`, `enhancing`, `decoding`, `encoding`, `recording` (besides `loading`, `generating`, `writing`).
- **In the app**, the job methods are job kinds (`engine.ExecutorKinds`) submitted on the lane named like their role: `jobs.Lane(proto.RoleOf(kind))`. `media.probe`, `capture.devices` and recordings bypass the queue: `engine.Probe`, `engine.Devices`, `engine.StartCapture`.

### `tts.encodeVoice` (job, gpu)

Encodes a clone reference once (speaker embedding, codec tokens… whatever the model conditions on) into a file, so later takes skip that step.

```json
→ {"id":"01JB…","method":"tts.encodeVoice","params":{"model":"mlx-community/Qwen3-TTS-12Hz-1.7B-Base-8bit","modelPath":"/…","refAudio":"/…/reference.wav","refText":"This is what my voice sounds like.","language":"English","promptPath":"/…/voices/01JB…/qwen3-tts-1.7b-base-8bit.voiceprompt"}}
← job.progress {stage:"loading"} … {stage:"encoding"}
← {"id":"01JB…","result":{"voicePrompt":"/…/qwen3-tts-1.7b-base-8bit.voiceprompt","model":"…","format":"qwen3_tts/1","bytes":182400,"audioSeconds":12.5,"seconds":0.6}}
```

- The file's content is the engine's business; `format` names the family and version. Its metadata records the model, so a prompt is never applied to another one (see `voicePrompt` above).
- **Which models (M1):** Qwen3-TTS Base (0.6B and 1.7B), format `qwen3_tts/1`: a safetensors file with the reference's codec codes, its transcript's token ids and the speaker embedding. They need `refText`; without it the request fails with `bad_request`. Others answer `unsupported`.
- The app keeps prompts next to the voice, one per model, and makes them again after a model update.
- A model that cannot (catalogue capability `voicePrompt` unset) answers `unsupported`, `data:{"capability":"voicePrompt"}`. Cloning then sends `refAudio` every time.

### `stt.transcribe` (job, gpu)

```json
→ {"id":"01JB…","method":"stt.transcribe","params":{"model":"mlx-community/whisper-large-v3-turbo-4bit","modelPath":"/…","audio":"/…/reference.wav","language":"auto","prompt":"AudioSmith, MLX"}}
← job.progress {stage:"transcribing", fraction, audioSeconds} …
← {"id":"01JB…","result":{"model":"…","text":"This is what my voice sounds like. …","language":"en",
   "segments":[{"start":0,"end":30,"text":"This is what my voice sounds like."},…],"audioSeconds":42.5,"seconds":3.1,"rtf":13.7}}
```

| Param | Meaning |
|---|---|
| `language` | BCP-47, or `""`/`"auto"` to detect. `result.language` is the one detected (or given). |
| `task` | `"transcribe"` (default) or `"translate"` (into English; Whisper only, else `unsupported` with capability `translate`). |
| `words` | Ask for word timings. Only models with catalogue `wordTimes` give them (`segment.words`: `text`, `start`, `end`, optional `prob`); others ignore it: run `stt.align` on the text. |
| `prompt` | Context or vocabulary (Whisper's initial prompt, Qwen3-ASR's context); ignored by models without one. |

Segments are in time order and cover `text`. Whisper gives about one per 30 s window, Qwen3-ASR one per utterance.

**Speakers (MOSS-Transcribe-Diarize, family `moss_transcribe_diarize`, catalogue capability `speakers`).** One segment per speaker turn, with an optional `speaker`: the model's anonymous label (`"S01"`, `"S02"`…, numbered in order of appearance within this call). The field is additive within protocol 2: other models never set it, and older apps ignore it.

```json
← {"id":"01JB…","result":{"model":"vanch007/mlx-MOSS-Transcribe-Diarize-8bit","text":"今天我们聊聊 AudioSmith。好啊，从哪儿说起？","language":"zh",
   "segments":[{"start":0.48,"end":3.9,"text":"今天我们聊聊 AudioSmith。","speaker":"S01"},{"start":4.2,"end":7.66,"text":"好啊，从哪儿说起？","speaker":"S02"}],…}}
```

- The model writes `[0.48][S01]text[3.90]`: the engine parses the timestamp and speaker tags into segments and strips them (and acoustic events such as `[laughter]`) from the text. It takes no language: `result.language` is detected from the text (Han/kana/Hangul share first, then Apple's `NLLanguageRecognizer`). `words` is ignored (no word timings: run `stt.align`).
- `prompt` becomes the model card's hotword hint (`热词提示：a, b`, terms split on commas, 、, ; and newlines).
- It loads only from the snapshot directory (`modelPath`, or the repo's snapshot in the cache), read-only; never by repo id, so mlx-audio-swift's "incomplete cache → delete" never runs on it.
- The engine decodes the whole input in one pass. The app sends at most 10 minutes per call: longer audio goes in 10-minute windows that overlap by 60 s, and the app links the windows' labels through the overlap (`services.TranscribeSpeakers`). The app also skips windows without speech (the model answers silence like a chat model: "I'm sorry, I can't assist…") and drops such answers.

### `stt.align` (job, gpu)

`{model, modelPath, audio, text, language?}` → `{model, words:[{text,start,end}], audioSeconds, seconds}`. Qwen3-ForcedAligner: words in order, one per character for Chinese and Japanese; punctuation is not a word. The app maps words back onto its lines (subtitles, script chunks).

### `vad.detect` (job, gpu)

`{model, modelPath, audio, threshold?, minSpeech?, minSilence?, pad?}` → `{model, regions:[{start,end,prob}], audioSeconds, seconds}`. Silero VAD; the defaults are threshold 0.5, minSpeech 0.25 s, minSilence 0.1 s, pad 0.03 s. Regions are in order and do not overlap. M1 uses it to pick the best 5–15 s reference window; M2's dictation runs its own VAD in `listen`.

### `enhance.run` (job, gpu)

`{model, modelPath, audio, output, strength?}` → `{output, sampleRate, channels, frames, audioSeconds, model, seconds}`. DeepFilterNet v3 (the `v3/` folder of `mlx-community/DeepFilterNet-mlx`). The output is mono at the model's rate (48 kHz); the app resamples if it needs another. `strength` (0–1, default 1) mixes the enhanced signal over the original, to keep some room tone. The input is read, enhanced in 60 s windows that overlap by 1 s and crossfade, and written as it goes (`OverlapStream` in AudioSmithStream; code review 1102 M5): memory stays at about a window whatever the length, with the same output as enhancing the whole track at once.

### `media.probe` (control, media)

```json
→ {"id":"c2","method":"media.probe","params":{"input":"/Users/me/Movies/interview.mov"}}
← {"id":"c2","result":{"format":"mov","duration":754.32,"hasAudio":true,"hasVideo":true,"audioTracks":1,"codec":"aac","sampleRate":48000,"channels":2,"bitRate":256000,
   "video":{"codec":"hevc","width":3840,"height":2160,"fps":29.97}}}
```

Audio fields describe the first audio track; `bits` is set for PCM only.

### `media.decode` (job, media)

`{input, output, sampleRate?, channels?, start?, duration?, track?}` → `{output, sampleRate, channels, frames, audioSeconds, source, seconds}`. Decodes a window (`start`, `duration`; 0 = to the end) of an audio track (`track`, default 0) to WAV at `sampleRate`/`channels` (0 keeps the source's; 1 mixes down, 2 duplicates mono). `source` is the input's `media.probe` result. Progress stage `decoding`.

### `media.encode` (job, media; M3)

```json
→ {"id":"01JB…","method":"media.encode","params":{"input":"/…/renders/01JB0R/book.wav","output":{"path":"/…/The Night Train.m4b","format":"m4b"},"codec":"aac","bitrate":64000,
   "chapters":[{"title":"Opening Credits","start":0,"end":12.5},{"title":"Chapter 1","start":12.5,"end":1834.2}],
   "metadata":{"title":"The Night Train","author":"M. Example","narrator":"Aria (AudioSmith)","year":"2026","genre":"Fiction","description":"A short novel."},
   "cover":"/…/covers/01JB0C.jpg"}}
← job.progress {stage:"encoding", fraction} … {stage:"writing"}
← {"id":"01JB…","result":{"output":"/…/The Night Train.m4b","format":"m4b","bytes":14820311,"duration":1834.2,"bitrate":64000}}
```

- **Input:** a WAV (RF64 allowed) or anything else AVFoundation reads.
- **Output:** `format` `m4b` or `m4a` (the same MP4 container); `mp3` answers `unsupported`, `data:{"capability":"mp3"}` (no encoder in this build).
- **Audio:** AAC-LC through AVAssetWriter, at `sampleRate` (0 = the input's, at most 48 kHz), mono or stereo as the input, `bitrate` in b/s (0 = 64 kb/s mono, 96 kb/s stereo).
- **Chapters** become a QuickTime chapter track (a text track the audio track refers to with `chap`), which Apple Books, QuickTime and AVFoundation read. Each runs to the next one's start; the last to the end.
- **Tags** (iTunes atoms): `©nam` and `©alb` = title, `©ART` and `aART` = author, `©wrt` = narrator, `©day`, `©gen`, `desc` and `©cmt` = description, `covr` = the cover (PNG or JPEG).
- The file is written to a temporary name beside `output.path` and renamed; `job.cancel` removes it.

### `capture.devices` (control, listen)

Returns `{devices:[{id, name, channels, sampleRate, default?, transport?}]}`, the system default first. `id` is the Core Audio device UID (stable across launches and reconnections); `transport` is `builtin`, `usb`, `bluetooth`, `aggregate`, `virtual` or absent. Voice processing's private aggregates (`CADefaultDeviceAggregate-*`, made while an app records with echo cancellation) are left out.

### `capture.start` (job, listen) → `capture.level` … → `capture.stop` (control)

```json
→ {"id":"01JB…","method":"capture.start","params":{"device":"AppleUSBAudioEngine:…","inputChannels":[2],"channels":1,"sampleRate":48000,"output":{"path":"/…/recordings/01JB….wav","bits":24},"maxSeconds":120}}
← {"method":"job.progress","params":{"job":"01JB…","stage":"recording"}}
← {"method":"capture.level","params":{"job":"01JB…","seconds":0.05,"rms":-52.3,"peak":-40.1}}   … about 20 per second
→ {"id":"c3","method":"capture.stop","params":{"job":"01JB…"}}
← {"id":"01JB…","result":{"output":"/…/01JB….wav","sampleRate":48000,"channels":1,"frames":9600,"audioSeconds":0.2,"device":"AppleUSBAudioEngine:…","peak":-0.0001,"clipped":3,"reason":"stopped"}}
← {"id":"c3","result":{"stopped":true}}
```

- **A recording is a job** whose response comes when it ends, so its meter (`capture.level`, which names the job) and its result follow the usual job rules: notifications first, nothing after the response.
- **Params.** `device` is a device `id` (`""` = the system default input). `inputChannels` are the device's channels numbered from 1, as in Audio MIDI Setup (default `[1]`, or `[1,2]` for stereo); with `channels` 1 several are mixed down. `sampleRate` is the file's (default: the device's). `maxSeconds` stops it by itself (0 = no limit). `aec: true` records with macOS voice processing (echo cancellation, with noise suppression and automatic gain: the voice may sound processed; other apps' audio is ducked as little as the system allows); its processed signal is one channel, so `inputChannels` do not apply (the result says `aecMono:true` when it gave a stereo file, or a file with picked channels, its one channel). Voice processing builds an aggregate of the system's default devices: for another input the engine chooses it again after turning voice processing on and reads back the device it records (or the aggregate's sub-devices). When voice processing does not start on the input, or would record another one, the recording runs **without** it rather than failing: every `capture.level` and the result carry `aecFallback:"failed"` or `"device"` (code review 1103 F2/F3).
- **Keeping the input running.** macOS, another app or a virtual audio driver may reconfigure the devices while recording (`AVAudioEngineConfigurationChange`), which stops the engine without an error. The engine checks a moment later (0.4 s, doubling with each recent restart, at most 5 s) and, when no buffers come, starts the same engine again in place — the device chosen again, the tap installed with the input's format now (a new rate is resampled to the file's), the same file. Buffers stopping for 1 s after sound came count the same. More than 6 restarts within 30 s count as `deviceChanges`.
- **No sound.** Without voice processing, no buffers or only digital silence (every sample below −120 dBFS) for 1.5 s after the start count as `noSound` (3 s on a Bluetooth input, which delivers zeros while it switches to its microphone profile). Either reason turns voice processing on by itself, once (when `aec` was off), except on a `virtual` or `aggregate` input (loopback, BlackHole: their silence is exact zeros, and voice processing would record another input) and when `inputChannels` are given (code review 1103 F1); those give up after 5.5 s (and a virtual or aggregate input only when **no buffers** come: its zeros are its signal, and a silent take is kept). When it turns voice processing on, the silence recorded so far is dropped, and every `capture.level` after it carries `aecAuto:true, aecReason`. With voice processing on, 4 s without sound, or the restarts running out again, give up: the job fails with `no_input` (`data:{device, name, reason, aec?}`, `aec:true` when voice processing was on; the file is removed), or, when sound came before, ends with `reason:"inputStopped"` keeping what was recorded. A recording stopped after 1.5 s or more of digital silence throughout (on a virtual or aggregate input: of no buffers) fails with `no_input` too, rather than leaving a silent file.
- **Start.** `job.progress {stage:"recording"}` is sent once the device runs. The first time, macOS asks for microphone access before that; refusal fails the job with `permission_denied`, `data:{"permission":"microphone"}`. An unknown or busy device fails with `device_unavailable`, `data:{"device"}`.
- **Meter.** `capture.level` about 20 times a second: `seconds` recorded so far, `rms` and `peak` in dBFS (at least −120) over the recorded channels, `clipped` when a sample reached full scale since the last one.
- **End.** `capture.stop {job}` finalises the file; the job answers with the result **before** the stop request is answered `{stopped:true}` (`false` if the job had already ended). It also ends by itself: `reason` is `stopped`, `maxSeconds`, `deviceLost` (unplugged; what was recorded is kept) or `inputStopped` (above). `job.cancel` discards the file and the job ends `cancelled`.
- **Result.** The `proto.AudioOut` fields, the `device` used, the loudest `peak` (dBFS) and `clipped`, the number of samples at full scale; `aec:true` when voice processing was on at the end, and `aecAuto:true, aecReason` when the engine turned it on; `aecFallback` and `aecMono` (above).
- **In the app**, `engine.StartCapture` returns once `recording` arrives; `Recording.Stop` sends `capture.stop` and returns the job's result; `Recording.Cancel` cancels.

## 5.3 Methods added in M4 (dubbing)

Types: [`dub.go`](../internal/engine/proto/dub.go) (Go; `internal/dub/engineapi.go` aliases them), [`WireDub.swift`](../engine/Sources/AudioSmithProtocol/WireDub.swift). Fixtures: `separate.jsonl`, `diarize.jsonl`, `mux.jsonl`, `thumbnail.jsonl`, `transcode.jsonl`. The §5.2 shared rules apply (absolute paths, `file_not_found` / `unsupported_media`, temporary name + rename, BCP-47, model jobs). New progress stages: `separating`, `diarizing`, `muxing`, `transcoding`.

### `separate.run` (job, gpu)

```json
→ {"id":"01JC…","method":"separate.run","params":{"model":"iky1e/demucs-mlx","modelPath":"/…","audio":"/…/dubs/01JC/audio.wav",
   "vocals":{"path":"/…/vocals.wav","bits":16},"background":{"path":"/…/background.wav","bits":16},"shifts":1,"overlap":0.25}}
← job.progress {stage:"loading"} … {stage:"separating", fraction, audioSeconds} … {stage:"writing"}
← {"id":"01JC…","result":{"vocals":"/…/vocals.wav","background":"/…/background.wav","sampleRate":44100,"channels":2,"frames":2646000,"audioSeconds":60,"model":"iky1e/demucs-mlx","seconds":8.6}}
```

- **Models** (catalogue task `separate`; the dub pipeline takes the first): HTDemucs (`htdemucs`, the only one in the catalogue: [demucs-mlx-swift](https://github.com/kylehowells/demucs-mlx-swift), MIT; `htdemucs_ft` / `htdemucs_6s` checkpoints in the same folder also load). The engine still loads Mel-RoFormer (`mel-roformer-kim-vocal-2`, [mel-roformer-mlx-swift](https://github.com/xocialize/mel-roformer-mlx-swift), MIT), which thread #46 removed from the catalogue: its last 2–3.5 s came out silent. The engine picks the family by the snapshot's files.
- **Stems:** `vocals` is the voices; `background` is everything else (HTDemucs: drums + bass + other; RoFormer: the mix minus vocals). A `path` of `""` skips that stem (it is then absent from the result). Stems are WAV at 44.1 kHz stereo, as many frames as the input; mono inputs are duplicated.
- **Long inputs** are separated in 60 s windows joined with a 2 s linear crossfade and streamed to the files, so memory stays flat. `shifts` (HTDemucs' random shifts, 0 = 1) trade time for quality; `overlap` is HTDemucs' segment overlap (0 = 0.25). RoFormer ignores both.
- **Speed on an M-series Air (24 GB):** HTDemucs ~10× real time, RoFormer ~1.5×. On a 25 s two-speaker clip with music: vocals vs. clean speech SI-SDR 18.3 dB (HTDemucs) / 16.1 dB (RoFormer); background vs. music 14.0 / 12.9 dB (mix: 2.6 dB).

### `diarize.run` (job, gpu)

```json
→ {"id":"01JC…","method":"diarize.run","params":{"model":"mlx-community/diar_streaming_sortformer_4spk-v2.1-fp16","modelPath":"/…","audio":"/…/vocals.wav"}}
← job.progress {stage:"loading"} … {stage:"diarizing", fraction:1, audioSeconds}
← {"id":"01JC…","result":{"turns":[{"start":0.52,"end":3.1,"speaker":"0"},{"start":3.4,"end":6.02,"speaker":"1"},{"start":5.9,"end":7.25,"speaker":"0"}],"speakers":2,"model":"…","audioSeconds":60,"seconds":1.9}}
```

- **Model:** Sortformer 4spk v2.1 (catalogue task `diarize`; NVIDIA Open Model License, commercial use allowed), from the pinned mlx-audio-swift. The audio is decoded to 16 kHz mono; threshold 0.5, turns under 0.25 s dropped, gaps under 0.5 s merged.
- **Turns** are in time order and may overlap (crosstalk). Labels are the model's (`"0"`…`"3"`), stable within one result; `speakers` counts the labels used.
- **Speaker counts:** the model finds how many speakers there are by itself. `speakers` or `minSpeakers` over 4 fails with `unsupported`, `data:{"capability":"speakers","max":4}`; otherwise the hints (and `maxSpeakers`) are accepted and ignored. More speakers need another model (FluidAudio, later).
- Run it on the separated vocals: music confuses it less. Mac check: 2 speakers, 99 % of frames agree with the script, 0.6 s for 25 s.

### `media.mux` (job, media)

```json
→ {"id":"01JC…","method":"media.mux","params":{"video":"/Users/me/Movies/interview.mov","keepAudio":true,
   "audio":[{"path":"/…/tracks/es.wav","language":"es","title":"Español","default":true,"bitrate":192000},{"path":"/…/tracks/fr.wav","language":"fr","title":"Français"}],
   "subtitles":[{"path":"/…/subs-es.srt","language":"es","title":"Español","default":true},{"path":"/…/subs-en.vtt","language":"en","title":"English"}],
   "output":{"path":"/Users/me/Movies/Interview.mp4","format":"mp4"}}}
← job.progress {stage:"muxing", fraction} …
← {"id":"01JC…","result":{"output":"/Users/me/Movies/Interview.mp4","bytes":182733112,"duration":754.32,"tracks":3}}
```

- **Video** is the first video track of `video`, copied as it is (no re-encode; its orientation kept). An input without one fails with `unsupported_media`. The file runs as long as the video.
- **Audio tracks,** in this order: with `keepAudio`, the source's own (copied if AAC, ALAC or AC-3, else encoded to AAC), then each of `audio` encoded to AAC-LC 48 kHz stereo at `bitrate` (0 = 192 kb/s), cut or padded with silence to the video's length. `tracks` counts them.
- **Subtitles** are soft text tracks (`tx3g`, which ffprobe calls `mov_text`) from SRT or WebVTT files (tags stripped, gaps written as empty samples).
- **Languages** (BCP-47 → ISO 639-2/T, e.g. `es` → `spa`) are written on every track; a track without one is `und`. Audio tracks form one alternate group and subtitles another; `default` picks the track players start with (one per group: the first with `default`; for audio, else the first dubbed track; subtitles stay off unless one is `default`). `forced` is accepted but not written yet.
- **Titles** are written into MOV files (`output.format` `"mov"`); AVFoundation does not write track names into MP4, so MP4 players show the language. Checked with ffprobe (languages, `default` dispositions) and AVFoundation (`languageCode`, `isEnabled`) on the Mac.
- `retime` (slowing parts of the video) is not implemented: `unsupported`, `data:{"capability":"retime"}`.
- Written to a temporary name and renamed; `job.cancel` removes it.

### `media.thumbnail` (job, media)

`{input, times:[s…], outputs:[path…], width?}` → `{frames:[{time, path, width, height}]}`. One image per time, JPEG (quality 0.8) or PNG by the output's extension, `width` wide (0 = the video's) in proportion, display orientation applied. The frame is the one nearest each time within ±0.5 s (fast: no exact-frame decode); times past the end give the last frame (`time` says which). `times` and `outputs` must be the same length (`bad_request`); no video track → `unsupported_media`.

### `media.transcode` (job, media)

`{input, output:{path, format:"mp4"}, maxHeight?, noAudio?}` → `{output, bytes, duration, width, height}`. An H.264 + AAC MP4 that fits an AVFoundation preset (`maxHeight` 0 = 720; under 540 → 640×480, under 720 → 960×540, under 1080 → 1280×720, else 1920×1080; aspect kept, never upscaled) for the web view's preview, through AVAssetExportSession (VideoToolbox; a 1080p HEVC clip of 25 s takes 0.3 s). `noAudio` drops the audio. AVFoundation reads H.264, HEVC, ProRes and (macOS 15 on M3+) AV1 in MP4/MOV; WebM, MKV and VP9 fail with `unsupported_media`. Progress stage `transcoding`.

### `media.timePitch` (not implemented)

Stays reserved: the app stretches with WSOLA in Go (`internal/dsp/stretch`). `TimePitchParams` (`input`, `output`, `rate`, `pitch?`) is kept in `proto/dub.go` should a listening test prefer AVAudioUnitTimePitch.

## 5.4 The local language model

Types: [`llm.go`](../internal/engine/proto/llm.go) (Go; `internal/llm`'s `GenerateParams`, `GenerateResult` and `TokenNotification` alias them), [`WireLLM.swift`](../engine/Sources/AudioSmithProtocol/WireLLM.swift). Fixture: `llm.jsonl`.

### `llm.generate` (job, gpu) → `llm.token` …

```json
→ {"id":"01JD…","method":"llm.generate","params":{"model":"mlx-community/Qwen3-4B-Instruct-2507-4bit","modelPath":"/…",
   "messages":[{"role":"system","content":"You translate subtitles from English to Spanish. Reply with the translation only."},{"role":"user","content":"Good morning, and welcome back to the show."}],
   "maxTokens":256,"temperature":0.3,"topP":0.8,"seed":7,"stop":["\n\n"],"stream":true}}
← job.progress {stage:"loading"} … {stage:"generating"}
← {"method":"llm.token","params":{"job":"01JD…","text":"Buenos días,"}} …
← {"id":"01JD…","result":{"text":"Buenos días, y bienvenidos de nuevo al programa.","finishReason":"stop","model":"…","promptTokens":41,"completionTokens":12,"seconds":0.31,"tokensPerSecond":58.2}}
```

- **Models** (catalogue task `llm`: Qwen3 4B Instruct 2507 since thread #46; the engine also loads the families `qwen3`, `gemma4`): anything mlx-swift-lm's MLXLLM loads, from the snapshot at `modelPath`, with its tokenizer (swift-transformers) and chat template. Hybrid thinking models get `enable_thinking: false`. `models.load` with `task:"llm"` loads one ahead.
- **Sampling.** `temperature` (default 0.7; 0 = greedy), `topP` (default 0.8), `seed` (reproducible sampling). `maxTokens` caps the reply (0 = 2048, within the context). `finishReason` is `stop` (end of turn or a `stop` string, which is not included) or `length`.
- **Streaming.** With `stream`, the reply arrives as `llm.token` notifications, in order, about 20 a second; together they are `result.text`. A piece that could start a stop string is held back until it cannot.
- **JSON.** `json` asks for one JSON value; `schema` (JSON Schema) shapes it. The engine constrains decoding to the schema with xgrammar (mlx-swift-lm's MLXGuidedGeneration matcher, with its jump-forward of forced text, in the engine's own token loop `LLMGuided.swift`) and sets `constrained`; a schema xgrammar cannot compile is logged and the reply is generated freely. `AUDIOSMITH_LLM_GRAMMAR=0` turns the grammar off; `AUDIOSMITH_LLM_TRACE=1` logs how each constrained reply ended (`2`: every token with its time, and the model's own choice where the grammar overrode it). The app validates either way (`internal/llm` repairs).
  - The reply starts after an opening ```` ```json ```` line added to the prompt (not in `text`), as the model writes JSON on its own. `AUDIOSMITH_LLM_PRIME=0` leaves it out.
  - Greedy by default (`temperature`, `topP`, `seed` ignored: the same reply every time); `sampleConstrained: true` samples it as a free reply (`temperature` > 0, `topP` default 0.95, `seed` for the same sample again). The app's repair turns set it (`internal/llm`: temperature at least 0.7, seed 1).
  - An object's keys come in a fixed order: its required properties in the order `required` lists them, then the others by name (`LLMGenerateParams.grammarSchema`). The app's prompts describe the keys in that order.
  - The model's special tokens are never text under the grammar, and its stop tokens (Qwen3: `<|im_end|>`, `<|endoftext|>`) are allowed only where the value may end: a constrained reply with `finishReason` `stop` is a whole value.
  - Nothing steers the reply to close as the budget runs out (only runs of whitespace are held back): a value longer than `maxTokens` ends with `length`, cut off. So does a reply that repeats itself (the same 8–64 tokens three times in a row, `ReplyLoop`), at once rather than at the budget.
- **Context.** The prompt (after the chat template) plus `maxTokens` (at least 256) may use at most the model's `max_position_embeddings`, capped at 16384 tokens (`AUDIOSMITH_LLM_CONTEXT`): the KV cache of a 4B model takes about 150 KB a token. More fails with `context_too_long`, `data:{"limit","actual","unit":"tokens"}`; the app splits the work (translation batches are about 2,500 characters).
- **Memory.** Before loading a model, the gpu worker unloads idle models, least recently used first, until the model's weights fit in the memory budget (`AUDIOSMITH_MEMORY_BUDGET`, else 60 % of the Mac's memory, as the supervisor assumes): a 2.3 GB language model evicts idle TTS models on a tight Mac without waiting for the supervisor.
- **In the app,** `llm.EngineBackend` (provider `local`, `local_llm.go`) calls the gpu worker directly; it is first in the automatic writing-help routing once its model is downloaded. A dub's translation stage then runs on the gpu lane, so it queues with renders. `llm.generate` is also a job kind (`engine.ExecutorKinds`): `llm.token` notifications are the job's chunks.

## 5.5 The invisible watermark

Types: [`watermark.go`](../internal/engine/proto/watermark.go) (Go), [`WireWatermark.swift`](../engine/Sources/AudioSmithProtocol/WireWatermark.swift). Fixture: `watermark.jsonl`. Both methods are jobs on the **media** worker.

The model is Meta's **AudioSeal** (facebookresearch/audioseal, MIT; weights `facebook/audioseal`, MIT), ported to MLX in [`AudioSeal.swift`](../engine/Sources/AudioSmithEngine/AudioSeal.swift): a SEANet encoder–decoder makes a 16 kHz watermark from a 16-bit message, and a SEANet detector scores every sample and decodes the message. The repo ships PyTorch `.pth` pickles only. The app converts them once, without Python and without unpickling code ([`internal/torchpt`](../internal/torchpt), [`internal/watermark`](../internal/watermark)), into `generator.safetensors` and `detector.safetensors` (weight norm folded) beside the model cache. `modelPath` is that folder.

### `watermark.embed` (job, media)

```json
→ {"id":"01JE…","method":"watermark.embed","params":{"model":"facebook/audioseal","modelPath":"/…/audiosmith-converted/facebook--audioseal/3c19eba…",
   "input":"/…/takes/01JE/take.wav","output":{"path":"/…/takes/01JE/take.wm.wav","bits":24},"message":42330,"strength":1}}
← job.progress {stage:"loading"} … {stage:"watermarking", fraction} … {stage:"writing"}
← {"id":"01JE…","result":{"output":"/…/take.wm.wav","sampleRate":24000,"channels":1,"frames":297600,"audioSeconds":12.4,"model":"facebook/audioseal","message":42330,"snr":31.2,"seconds":0.21}}
```

- **Input** is anything the engine reads. The output keeps the input's rate and channels. The watermark is computed on a 16 kHz mono mix (AVFoundation resampling), resampled to the input's rate (AVAudioConverter, best quality) and added to every channel. Samples are clipped to ±1. `output.path` may be the input: the file is read whole first, then written to a temporary name and renamed.
- **`message`** is 16 bits (0–65535); bit 15 is the model's first bit. Out of range: `bad_request`.
- **`strength`** scales the watermark (AudioSeal's alpha, default 1).
- **`snr`** is the audio's power over the watermark's, in dB. AudioSeal's watermark sits at about 25 dB under speech.
- Long inputs run in 10 s windows, up to 8 at a time as one batch; the watermark is crossfaded over 1 s overlaps.
- **Speed** (MacBook Air M4, Metal): about 1 s per minute of audio (0.2 s for a 10 s take). AUDIOSMITH_WATERMARK_DEVICE=cpu runs it on the CPU (23 s a minute).

### `watermark.detect` (job, media)

```json
→ {"id":"01JE…","method":"watermark.detect","params":{"model":"facebook/audioseal","modelPath":"/…","input":"/Users/me/Downloads/clip.m4a"}}
← job.progress {stage:"detecting", fraction} …
← {"id":"01JE…","result":{"probability":0.998,"detected":true,"message":42330,"bits":[0.99,0.02,…],"confidence":0.94,
   "frames":[{"start":0,"end":1,"probability":0.997},…],"audioSeconds":3,"model":"facebook/audioseal","seconds":0.05}}
```

- **`probability`** is the fraction of samples whose watermark probability is over `threshold` (default 0.5), as AudioSeal's `detect_watermark` computes it. `detected` = `probability ≥ 0.5`.
- **`message`** is decoded even when there is no watermark (it is then random). `bits` are the probabilities of each bit being 1, most significant first; `confidence` is their mean |2p − 1|.
- **`frames`** give the probability per second, to locate watermarked stretches.
- **Speed:** 0.4 s per minute of audio. The detector's 320× transposed convolution and its 1×1 head are folded into one matrix.

### AudioSmith's message

`internal/watermark`: the high byte is the marker `0xA5`; the low byte is the install id (1–255, chosen at random once; `settings.Privacy.WatermarkID`). A file is reported as made by AudioSmith when it is detected, the marker matches, and the bits are decoded with confidence ≥ 0.2.

### Robustness

Mac, `TestWatermarkEngine`; Kokoro, Qwen3-TTS CustomVoice and OmniVoice takes, 10–11 s at 24 kHz:

| Check | Result |
|---|---|
| Watermarked | p = 1.000, exact message |
| Clean takes | p = 0.001–0.043 |
| AAC 64 kb/s | still detected |
| 24→48 kHz resampling | still detected |
| Loudness normalisation to −16 LUFS | still detected |
| ±5 % time-stretch (WSOLA) | **not reliably detected** (p ≈ 0.25–0.3); AudioSeal is not trained for speed changes |

The port matches the PyTorch reference: on the same 16 kHz input and message, the two watermarks correlate at 1.00000 with a relative difference under 10⁻⁵, at the same SNR (24.9 dB). Each side's detector finds the other's mark, with the exact message.

## 5.6 Dialogue models

Types: [`dialogue.go`](../internal/engine/proto/dialogue.go) (Go), [`WireDialogue.swift`](../engine/Sources/AudioSmithProtocol/WireDialogue.swift). Fixture: `dialogue.jsonl`. Threads #80 (SoulX-Podcast), #82 (MOSS-TTSD), #166 (VibeVoice).

### `tts.dialogue` (job, gpu) → `tts.turn` …

A whole conversation in one job: each turn is spoken hearing the turns before it, in voices cloned from one reference per speaker.

```json
→ {"id":"01JF…","method":"tts.dialogue","params":{"model":"actnowai/SoulX-Podcast-1.7B-MLX-8bit","modelPath":"/…",
   "speakers":[{"id":"child","refAudio":"/…/child.wav","refText":"今天放学的时候，我在路上看见一只小鸟。"},{"id":"mom","refAudio":"/…/mom.wav","refText":"今天晚上我们早点吃饭，好不好？"}],
   "turns":[{"speaker":"child","text":"妈妈，我想领养一只小猫，可以吗？"},{"speaker":"mom","text":"领养是好事，你能一直照顾它吗？"}],
   "outputDir":"/…/renders/01JF0R/dialogue-0","seed":2}}
← job.progress {stage:"loading"} … {stage:"encoding"} … {stage:"generating", fraction, audioSeconds}
← {"method":"tts.turn","params":{"job":"01JF…","index":0,"speaker":"child","output":"/…/turn-000.wav","frames":63360,"audioSeconds":2.64,"sampleRate":24000}} …
← {"id":"01JF…","result":{"model":"…","sampleRate":24000,"channels":1,"turns":[{"index":0,"speaker":"child","output":"/…/turn-000.wav","frames":63360,"audioSeconds":2.64,"codes":[4114,1897,…],"seconds":3.7},…],
   "audioSeconds":6.56,"seconds":9.4,"rtf":0.7,"seed":2}}
```

| Param | Meaning |
|---|---|
| `speakers` | Up to the model's limit (SoulX-Podcast: 4, MOSS-TTSD: 5, VibeVoice: 4; more fails with `unsupported`, `data.capability:"speakers"`). Each has a unique `id`, a reference clip (any format AVFoundation reads; 1–30 s) and its **exact transcript** (`refText`, required; VibeVoice clones from the clip alone and ignores it). |
| `turns` | In order; `speaker` names a speaker id. A turn with `codes` (from an earlier result of the same model and speakers) is **context only**: heard, not spoken again, no file. |
| `outputDir` | Absolute; the app owns it. Per-turn files are `turn-<index, 3 digits>.wav`; a one-track model writes `dialogue.wav`. |
| `bits` | 16 (default), 24, or 32 (float). |
| `seed`, `temperature`, `topP`, `topK`, `repetitionPenalty`, `maxTokens` (per turn; one-track: in all), `steps` (flow matching) | Unset: the model's defaults. SoulX-Podcast samples exactly as upstream does with transformers 4.57.1: repetition penalty 1.25 on the current turn's tokens → temperature 0.6 → top-k 100 → top-p 0.9, repetition-aware sampling (window 25, ratio 0.2), 8 to 3,000 tokens a turn, logits limited to the speech tokens and their end token; 15 flow steps. MOSS-TTSD samples as the official `generate()`: audio temperature 1.1 → repetition penalty 1.1 (codes already in the sequence, prompt included) → top-k 50 → top-p 0.9; the text channel 1.1 / 50 / 0.9 without penalty; at most twice the text's estimated length plus 10 s, never over 2,000 frames (160 s). |

Two kinds of model:
- **Per-turn** (SoulX-Podcast, family `soulx_podcast`): each generated turn is its own file, announced by `tts.turn` as soon as it is written (before the response, as every notification). `result.turns` lists the generated turns with `output`, `frames`, `audioSeconds`, the turn's `codes` (speech tokens, 0–6560) and `seconds` (its generation time). **Redoing one turn**: send the same speakers, the turns before it with their `codes`, and the turn itself without; only it is generated, with the same context.
- **One-track** (MOSS-TTSD, family `moss_ttsd`): the whole dialogue in one file, `dialogue.wav`: `result.mixed` (`output`, `frames`, `audioSeconds`, and `codes`/`codebooks`: the track's codec frames, 16 codes each, flattened; the first frame comes just before the audio, frame k + 1 lies under samples [k × 1920, (k + 1) × 1920), 12.5 frames a second), and every spoken turn with `index`, `speaker` and `text` (as the model read it, normalized); no `tts.turn`. The app cuts the track at the turns (`stt.align`, as character tracks are cut). **Redoing one turn**: the same speakers, the turns before it with their slice of `mixed.codes` as `codes` (the first one from frame 0), the turn itself without; context turns must all come before the turns to speak, and only those are in the new track.

Errors: a model that is not a dialogue model fails with `unsupported`, `data.capability:"dialogue"`; a missing `refText`, an unknown speaker, an empty turn or every turn with codes with `bad_request`; a missing reference with `file_not_found`.

`models.load` with `task:"dialogue"` loads a dialogue model ahead; its result's capabilities are `dialogue`, `clone`, `seed`, `sampling` and (SoulX-Podcast) `steps`.

**SoulX-Podcast in the engine** ([`SoulX.swift`](../engine/Sources/AudioSmithEngine/SoulX.swift), [`Dialogue.swift`](../engine/Sources/AudioSmithEngine/Dialogue.swift), [`AudioSmithDialogue`](../engine/Sources/AudioSmithDialogue)): the Qwen3-1.7B language model (8-bit, mlx-swift-lm) writes a turn's speech tokens after the reference blocks and the earlier turns, keeping its KV cache across turns and rebuilding the context as upstream does (more than 10 blocks or 6,192 tokens: the references, the texts of two earlier turns, the last two turns with their speech); CosyVoice2's flow matching (fresh noise per turn) and HiFT (mlx-audio-swift's S3Gen types, patch 0006) make the turn's waveform from its tokens and the speaker's reference tokens, mel and CAM++ x-vector. The S3 tokenizer is the auxiliary repo `mlx-community/S3TokenizerV2`. Checked against the official PyTorch code stage by stage (`audiosmith-engine --soulx-check`, scripts/soulx/parity.sh). M4 (24 GB): 0.7× real time, 39 speech tokens a second, 4.3 GB MLX peak.

**MOSS-TTSD in the engine** ([`MossTTSD.swift`](../engine/Sources/AudioSmithEngine/MossTTSD.swift), [`MossCodec.swift`](../engine/Sources/AudioSmithEngine/MossCodec.swift), [`MossDialogue.swift`](../engine/Sources/AudioSmithEngine/MossDialogue.swift), `AudioSmithDialogue`'s `MossTTSD*`): the TTSD Space's voice-clone-and-continuation mode. Each speaker's clip (loudness-normalized as the official processor does) is encoded alone into the user message's `Reference(s)`, all clips back to back once more as the start of the assistant's audio; the text is every transcript as `[Sn]` then the turns, through the Space's `normalize_text`. A Qwen3-8B backbone (8-bit, own code on MLX) writes rows of a text token and 16 codebooks with the delay pattern; MOSS-Audio-Tokenizer (8-bit, own code) decodes the new frames in 8-second streamed chunks with the prompt's frames as left context, trimmed as the official processor trims. Checked against the official PyTorch code (`audiosmith-engine --moss-check codec|tiny|lm`, scripts/mossttsd/parity.sh): prompt rows identical, the generation loop identical on a tiny random model, logits KL 6·10⁻¹¹ with the same weights, codec codes identical. M4 (24 GB): 0.8× real time, 11 frames a second, 12.2 GB MLX peak.

**VibeVoice in the engine** ([`VibeVoice.swift`](../engine/Sources/AudioSmithEngine/VibeVoice.swift), `AudioSmithDialogue`'s `VibeVoiceScript`; thread #166): VibeVoice 1.5B (family `vibevoice`, `microsoft/VibeVoice-1.5B` as published) on upstream mlx-audio-swift's port (engine patch 0011). One-track, like MOSS-TTSD, but without `codes`: the turns become one `Speaker N: text` line each (N by first appearance; Chinese punctuation made English, as the model card advises), each speaker's clip (in that order) is encoded by the model's acoustic tokenizer, and the model speaks the script in one go (7.5 frames a second, at most twice the text's estimated length plus 10 s, never over 3,000 frames). Turns with `codes` fail with `unsupported` (`data.capability:"continuation"`); the app leaves a line that fails its checks to the project's model. `steps` sets the diffusion steps (default 20). The repo has no tokenizer: the engine loads from a folder of links to the snapshot and the Qwen2.5 tokenizer files of the auxiliary repo `Qwen/Qwen2.5-1.5B`.

## 6. Notifications (engine → app)

| Method | Params | Notes |
|---|---|---|
| `job.progress` | `job`, `stage`, `fraction?`, `audioSeconds?`, `message?` | `stage` is an open set: `loading`, `generating`, `writing`, … Omit `fraction` when it is unknown; the UI then shows an indeterminate bar. `message` is English, for logs and the status line. At most about 10 per second per job. |
| `tts.chunk` | `job`, `seq`, `sampleRate`, `channels`, `encoding`, `data`, `final?` | `encoding` is `"pcm16le"` (the only one in M0). `data` is base64 (Go `[]byte`, Swift `Data`). Send chunks of up to about 1 s of audio. The last chunk has `final:true` and may be empty. |
| `log` | `level`, `message`, `job?` | Structured log line for the Logs panel. `level` is `debug`, `info`, `warning` or `error`. It may name any job. |
| `capture.level` | `job`, `seconds`, `rms`, `peak`, `clipped?`, `aecAuto?`, `aecReason?` | The input meter of a `capture.start` job, about 20 per second (§5.2). |
| `llm.token` | `job`, `text` | The next piece of an `llm.generate` reply with `stream` (§5.4). |
| `tts.turn` | `job`, `index`, `speaker`, `output`, `frames`, `audioSeconds`, `sampleRate` | A `tts.dialogue` turn's file is written (§5.6). |
| `engine.memory` | `level`, `unloaded` | The gpu worker: the system's memory pressure changed to `normal`, `warning` or `critical`; `unloaded` are the idle models it dropped for it (§4). Names no job. |

At 24 kHz mono, PCM16 in base64 is about 64 KB/s. If profiling ever shows that to be costly, a binary side channel (an fd 3 pipe) can be added in a later minor change.

## 7. Errors

| Code | When | `data` | App reaction |
|---|---|---|---|
| `bad_request` | Params malformed or missing | — | bug: report |
| `method_not_found` | Unknown method, or not in this role | — | hide the feature |
| `not_initialized` | Request before `engine.hello` | — | bug |
| `protocol_mismatch` | `hello.protocol` not spoken | `{"supported":[2]}` | "rebuild the engine" |
| `unsupported` | The model lacks the capability | `{"capability":"clone"}` | explain, disable the control |
| `model_not_downloaded` | Offline and files missing | `{"model","missing":[…]}` | offer the download (hub) |
| `model_load_failed` | Files present but unloadable | — | show the message; offer Verify/Repair |
| `licence_required` | Reserved: the app gates licences before sending | `{"licence"}` | licence dialog |
| `oom` | Out of memory; or a load refused under critical memory pressure | `{"active":…}`; `{"reason":"memory_pressure","level","model"}` | **retryable** after unloading or a restart; the pressure one says the Mac is short of memory |
| `busy` | Over `concurrency` | — | **retryable** (scheduler bug if seen) |
| `cancelled` | The job was cancelled | — | none |
| `internal` | Anything else | — | show the message, keep the stderr tail |
| `input_too_long` (M1) | The text is over the model's per-request limit | `{"limit","actual","unit"}` | split it (the app's chunker should have) |
| `file_not_found` (M1) | An input path does not exist | `{"path"}` | say which file is gone |
| `unsupported_media` (M1) | An input exists but cannot be decoded | `{"path"}` | "convert it to WAV, M4A or MP3" |
| `permission_denied` (M1) | macOS refused a permission | `{"permission":"microphone"}` | point to System Settings → Privacy & Security |
| `device_unavailable` (M1) | The capture device is unknown, gone or busy | `{"device"}` | refresh the device list |
| `context_too_long` | `llm.generate`: prompt + reply over the context the engine allows | `{"limit","actual","unit":"tokens"}` | split the work |
| `model_path_required` | A job without `path` / `modelPath`, and several snapshots of the model in the cache | `{"model","snapshots":[…]}` | bug: the executors always send the pinned snapshot |
| `no_input` | `capture.start`: the input brought no sound, even with voice processing (§5.2) | `{"device","name","reason":"noSound"\|"deviceChanges"}` | say what to try: echo cancellation, another input, quitting other recording apps, the microphone permission |

Messages are the error's own words: the engine takes a Swift error's `errorDescription` (LocalizedError), else its `description` (CustomStringConvertible), else `String(describing:)` (`errorMessage` in Wire.swift), never Cocoa's generic "The operation couldn’t be completed. (… error 0.)". A model's invalid-input errors (`AudioGenerationError.invalidInput`, `MossTTSNanoError.invalidInput`) are `bad_request`.

The app treats an unknown code as `internal` (so an older app reads the M1 codes as `internal`). `proto.Codes` lists them all; `proto.Retryable(code)` the retryable ones, and the job scheduler uses it. `jobErrorText` (enginelink.go) translates them for the UI.

## 8. Reserved methods (later milestones)

These names are taken; `proto.Reserved` lists them. An engine that does not implement one answers `method_not_found`. Each one gets params, results and fixtures in the milestone that adds it.

| Family | Methods | Role | Milestone |
|---|---|---|---|
| TTS | `tts.batch` (many segments, one warm model) | gpu | M3 |
| STT | streaming `stt.stream.open/push/close` → `stt.partial` / `stt.final` | listen | later (feasible: Qwen3-ASR and Nemotron have streaming sessions in the pin; partials would come from VAD-cut windows of a `capture.start` job, without `capture.pcm` round-trips) |
| Analysis | `lid.detect` | gpu | later |
| LLM | `translate.run` | gpu | later |
| Media | `media.timePitch`, `media.ocr`, `doc.extractText` | media | later |
| Capture | `capture.pcm` (raw input for streaming ASR) | listen | M2 |

M1 implemented `tts.encodeVoice`, `stt.transcribe`, `stt.align`, `vad.detect`, `enhance.run`, `media.probe`, `media.decode` and `capture.devices/start/stop/level` (§5.2); M3 `media.encode`; M4 `separate.run`, `diarize.run`, `media.mux`, `media.thumbnail` and `media.transcode` (§5.3); `llm.generate` (§5.4); `watermark.embed` and `watermark.detect` (§5.5); `tts.dialogue` (§5.6).

## 9. Versions and compatibility

- **Within protocol 2**, changes are additive only: new methods (announced in `hello.methods`), new optional fields and new notification methods. A peer ignores what it does not know.
- Any other change is **protocol 3**: new `proto.Version`, new fixtures, and the engine answers `protocol_mismatch` to the other version. App and engine ship together in one bundle, so a mismatch only happens with `AUDIOSMITH_ENGINE` overrides or stale dev builds.
- **Telling a v1 engine apart.** A v1 engine writes `{"event":"ready","protocolVersion":1,…}` first, without being asked. `proto.LegacyReady` recognises that line; the supervisor then reports "engine speaks protocol 1: rebuild it" instead of waiting for the hello timeout. `engine/Fixtures/legacy_v1.jsonl` pins this line.

### v1 → v2 mapping

| v1 | v2 |
|---|---|
| `ready` (unsolicited) | `engine.hello` request/response |
| `ping` / `pong` | `engine.stats` |
| `load` → `loaded` | `models.load` (job) → result |
| `synthesize` → `status` / `progress` / `done` | `tts.synthesize` → `job.progress` / `tts.chunk` → result |
| `cancel` (whatever runs) | `job.cancel {job}` |
| `cancelled` event | error `cancelled` on the job's id |
| `unload` → `unloaded` | `models.unload` → result |
| `error {message}` (id optional) | error response with a code, always on an id |
| `shutdown` | `engine.shutdown` |

## 10. Fixtures

[`engine/Fixtures/*.jsonl`](../engine/Fixtures) are golden transcripts. Each line is one of:

```json
{"to":"engine","msg":{…one wire message…}}
{"to":"app","msg":{…}}
```

`legacy_v1.jsonl` instead uses `{"to":"app","line":{…}}` for a raw v1 line.

`go test ./internal/engine/proto` (`TestFixtures`) runs these checks:
- every `msg` is a valid shape for its direction;
- params and results decode into the Go types **with unknown fields rejected**, and re-encode to equal JSON, so the Go types carry every field the fixture has;
- every request is answered exactly once;
- a job's notifications come before its response, and nothing comes after it;
- chunk `seq` has no gaps, `fraction` is within 0–1, and error codes are known;
- unknown methods are answered `method_not_found`.

**Swift side:** [`FixtureTests.swift`](../engine/Tests/AudioSmithEngineTests/FixtureTests.swift) decodes every `msg` with the Swift `Codable` types, re-encodes it and compares it with the fixture, so the Swift types carry every field. Run it with `scripts/test-engine.sh` (xcodebuild, on the Mac).

**Supervisor:** `TestSupervisorReplaysFixtures` (`go test ./internal/engine`) runs a scripted engine ([`enginetest`](../internal/engine/enginetest)) that replays `tts_stream`, `tts_clone`, `models`, `cancel`, `stt`, `analysis` and `voice`, and checks that `Supervisor.Call` returns the fixture's results, errors and notifications in order; the cancel transcript is driven by cancelling the call's context.

**Real engine:** `AUDIOSMITH_E2E=1 go test -run TestRealEngineConformance ./internal/engine` speaks raw protocol 2 with the built engine: handshake errors, every error code it can produce without special models, `busy`, control requests during a job, cancel by id, gapless chunks, shutdown and stdin EOF.

`TestFixturesCoverMethods` fails when a method has no fixture.

**Fake engine for app tests:** besides replaying, [`enginetest`](../internal/engine/enginetest) serves every method in the role it is started with, reading and writing real WAV files, so the app's code can be tested end to end on Linux (`TestM1Executors`, `TestCapture`). [`fake_media.go`](../internal/engine/enginetest/fake_media.go) says what each method does with its inputs (e.g. a transcript comes from `<audio>.txt`). View tests use the lighter fakes in `enginefake_test.go`, where the test finishes jobs itself.

Every engine change that touches the wire adds or updates a fixture in the same commit.

Changing a fixture is changing the contract: both test suites must pass.
