// Package assets holds the files the app embeds in its binary. They live
// here, at the module root, because go:embed cannot reach outside the
// embedding package's directory and the app (package main) is in app/.
// The scripts that make them write here: make-voice-previews.sh
// (voice-previews), make-curated-voices.sh (gallery-voices); the tray
// icons come from the ActNow brand kit, the starter voices are exported
// from AudioSmith (starter-voices/README.md).
package assets

import "embed"

// Tray icons, 36×36 px templates: AudioSmith's anvil and spark, and the
// same with a larger dot while dictation listens.
var (
	//go:embed tray/tray.png
	TrayIcon []byte
	//go:embed tray/tray-active.png
	TrayActiveIcon []byte
)

// GalleryVoices are the curated gallery voices' clips, at
// gallery-voices/<name>.<ref|preview>.ogg.
//
//go:embed gallery-voices
var GalleryVoices embed.FS

// VoicePreviews are the built-in speakers' previews, at
// voice-previews/<family>-<speaker>.ogg.
//
//go:embed voice-previews
var VoicePreviews embed.FS

// StarterVoices are the designed voices imported into a new library
// once, at starter-voices/*.asvoice.
//
//go:embed starter-voices
var StarterVoices embed.FS

// Samples are the sample projects (0.5 F6), at samples/<id>/sample.json
// and the files it names (samples/README.md).
//
//go:embed samples
var Samples embed.FS
