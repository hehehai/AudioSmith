# Voice previews

Short clips of the models' built-in speakers, embedded in the app
(assets/assets.go; app/voice_preview.go plays them) so the voice pickers can play a voice (▶) before its
model is downloaded (thread #60).

- One line per speaker in its main language (~3 s), e.g. 「你好，很高兴认识你，这是我的声音。」
  or "Hi, nice to meet you. This is what I sound like." (Ono_Anna, Qwen3-TTS's
  Japanese speaker: 「こんにちは、はじめまして。これが私の声です。」, thread #172)
- Rendered with AudioSmith's engine and the recommended sampling
  (Qwen3-TTS temperature 0.7, top-p 0.8, seed 7), trimmed, normalised to
  −18 LUFS (−1.5 dBTP), Ogg Vorbis mono.
- Named `<catalogue family>-<speaker id>.ogg`: `qwen3_tts-Vivian.ogg`,
  `kokoro-af_heart.ogg`.
- Remake them with `scripts/make-voice-previews.sh` (renders on the Mac,
  `TestVoicePreviewsE2E`, then encodes here with ffmpeg).
- Licences: the models' output (Qwen3-TTS Apache-2.0, Kokoro Apache-2.0):
  see NOTICE.
