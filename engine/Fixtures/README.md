# Protocol v2 fixtures

Golden transcripts of the engine protocol: the contract between the Go app and the Swift engine. See [docs/protocol-v2.md](../../docs/protocol-v2.md) §10 for the format and what the tests check.

| File | Shows |
|---|---|
| `handshake.jsonl` | `engine.hello` |
| `tts_stream.jsonl` | `tts.synthesize` with progress and streamed `tts.chunk`s |
| `tts_clone.jsonl` | cloning with sampling parameters and a seed |
| `cancel.jsonl` | `job.cancel` of a running job, and of a finished one |
| `models.jsonl` | `models.load` / `loaded` / `unload`, `engine.stats`, `flush`, `engine.memory` (memory pressure), `engine.config` (the app's budget and holds), `shutdown` |
| `errors.jsonl` | the M0 error codes, a load refused under critical memory pressure (`oom`, `memory_pressure`), `input_too_long`, and a model that only clones (MOSS-TTS-Nano) without a reference (`bad_request`) |
| `voice.jsonl` | `tts.encodeVoice`, a take from a voice prompt, VoiceDesign, CustomVoice + instruct, Chatterbox parameters |
| `stt.jsonl` | `stt.transcribe` (auto language, word timings, translate refused, missing file, speaker labels from MOSS-Transcribe-Diarize), `stt.align` |
| `analysis.jsonl` | `vad.detect`, `enhance.run`, `unsupported_media` |
| `media.jsonl` | a `media` worker: hello, `media.probe` (video, WAV, unreadable), `media.decode` of a window, `media.encode` of an M4B (and MP3 refused), `file_not_found`, a gpu method refused |
| `capture.jsonl` | a `listen` worker: its own speech-to-text model (`models.load`, `stt.transcribe`), `capture.devices`, a recording with its meter stopped by `capture.stop`, refusals (device, permission), cancel, `maxSeconds`, voice processing turned on by the engine (`aecAuto`), unavailable on the input (`aecFallback`) and giving one channel (`aecMono`), `no_input` |
| `separate.jsonl` | `separate.run`: both stems, one stem only, `file_not_found` |
| `diarize.jsonl` | `diarize.run` (overlapping turns), more than 4 speakers refused (`unsupported`, `max`) |
| `mux.jsonl` | `media.mux`: source audio + 2 dubbed tracks + 2 subtitle tracks; an input without video (`unsupported_media`) |
| `thumbnail.jsonl` | `media.thumbnail` (JPEG and PNG); no video track |
| `transcode.jsonl` | `media.transcode` to a 720p preview; WebM refused |
| `llm.jsonl` | `llm.generate` streamed (`llm.token`), JSON with a schema (constrained; greedy, and sampled with `sampleConstrained`), `context_too_long`, `models.load` of an LLM |
| `dialogue.jsonl` | `tts.dialogue` on a per-turn model (`tts.turn` per turn, the turns' codes), redoing one turn with the earlier turns as context (`codes`), a one-track model's result (`mixed` + the turns' texts), a model without dialogue and five speakers refused (`unsupported`), `models.load` with task `dialogue` |
| `watermark.jsonl` | a media worker with the watermark: `watermark.embed`, `watermark.detect`, a message over 16 bits (`bad_request`), a missing file |
| `logs.jsonl` | structured `log` notifications |
| `legacy_v1.jsonl` | the first line of a protocol-1 engine (detected, refused) |

Go: `go test ./internal/engine/proto` (and `TestFixturesCoverMethods`: every method has a fixture). Swift: `FixtureTests.swift` (`scripts/test-engine.sh`).
