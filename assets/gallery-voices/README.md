# Curated voices' bundled clips

Discover voices' designed voices (internal/gallery/data/curated.json) ship
two clips each, Ogg Vorbis, mono 24 kHz:

- `<id>.ref.ogg`: the voice's reference (10–15 s), the best of several
  takes on Qwen3-TTS 1.7B VoiceDesign that says its text (Qwen3-ASR);
  "Use voice" saves it as the voice's reference, with the text in
  curated.json.
- `<id>.preview.ogg`: the voice's preview line, cloned from that reference
  on Qwen3-TTS 1.7B Base, as the Studio would make it.

The seven Japanese voices of thread #172 (curated.json's `designModel`:
`irodori-tts-v4.1-small-mf-8bit`) were made on Irodori-TTS v4.1 instead:
the reference designed from the voice's Japanese caption saying one of its
own texts (app/e2e_curated_ja_test.go), the best of four seeds by what
Qwen3-ASR heard (kana error rate), and the preview cloned from it on the
same model, the recommended Japanese model of the Studio.

Made by `scripts/make-curated-voices.sh` (threads #94, #172). Generated
speech of no real person or character; Qwen3-TTS is Apache-2.0, Irodori-TTS
MIT with its author's terms (see NOTICE).
