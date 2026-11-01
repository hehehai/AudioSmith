package main

import (
	"testing"

	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/core"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/services"
)

// TestNewUserLongform: a new user's Stories and Audiobook start on the
// presets' models — Chinese audiobook: Qwen3-TTS 0.6B Base narrated by
// a saved voice (CustomVoice + Serena without one); English: Kokoro +
// af_heart — until the user picks a model or writes.
func TestNewUserLongform(t *testing.T) {
	t.Cleanup(func() { i18n.SetLocale("en") })
	a, _, tt := newTestApp(t)
	tt.Frame()
	if e := entryFor(a.lfBook.doc.Model); e.ID != "kokoro-82m-bf16" || a.lfBook.doc.Voice != services.SpeakerRef+"af_heart" || !a.lfBook.auto {
		t.Fatalf("English audiobook: %s %q", e.ID, a.lfBook.doc.Voice)
	}
	i18n.SetLocale("zh-Hans")
	a.autoLfModels()
	if e := entryFor(a.lfBook.doc.Model); e.ID != "qwen3-tts-0.6b-customvoice-8bit" || a.lfBook.doc.Voice != services.SpeakerRef+"Serena" {
		t.Fatalf("Chinese audiobook without voices: %s %q", e.ID, a.lfBook.doc.Voice)
	}

	b, _, tt2 := newTestApp(t)
	st := addVoice(t, b, core.Voice{Name: "温晴", Kind: core.VoiceCloned, Language: "zh"}, 8)
	tt2.Frame()
	i18n.SetLocale("zh-Hans")
	b.autoLfModels()
	if e := entryFor(b.lfBook.doc.Model); e.ID != "qwen3-tts-0.6b-base-8bit" || b.lfBook.doc.Voice != st.ID {
		t.Errorf("Chinese audiobook with a saved voice: %s %q", e.ID, b.lfBook.doc.Voice)
	}
	// The story: 1.7B Base with a saved voice.
	if e := entryFor(b.lfStories.doc.Model); e.ID != "qwen3-tts-1.7b-base-8bit" {
		t.Errorf("Chinese story with a voice: %s", e.ID)
	}
	// Writing ends the automatic choice.
	b.lfBook.doc.Script = "# 第一章\n\n很久以前。"
	b.lfBook.doc.Model = catalog.Builtin().Recommended(catalog.WorkflowAudiobook, "zh")[1].Repo
	addVoice(t, b, core.Voice{Name: "我的", Kind: core.VoiceCloned, Language: "zh"}, 8)
	tt2.Frame()
	if e := entryFor(b.lfBook.doc.Model); e.ID != "qwen3-tts-1.7b-base-8bit" {
		t.Errorf("a written page changed model: %s", e.ID)
	}
}
