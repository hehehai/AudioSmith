package main

// 「帮我写」 on the Stories composer (0.5 F6): while the writing help's
// model on this Mac is there, a toggle turns the one-line description
// into a short script for two or three voices — one prompt, in the
// Stories markup ([voice:Name] lines, narration untagged) — then the
// story is made of it, cast and opened. The draft is in the language of
// the description, written, without filler words (the project's rule for
// scripts: no 嗯/啊/um, no colloquialising).

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/oplog"
	"github.com/hehehai/AudioSmith/internal/services"
)

// storyWriteSkill is the writing help's skill that drafts a story.
const storyWriteSkill llm.Skill = "story_write"

// storyWriteTimeout bounds a draft (a load of the model included).
const storyWriteTimeout = 3 * time.Minute

// storyWritePrompt asks for the draft.
const storyWritePrompt = `You write short scripts for a voice studio. From the user's description, write one scene for two or three characters, 8 to 14 lines in all.
Answer with the script only, in this format, one item per line:
# Title
[voice:Name] What this character says.
A line without a tag is narration, read by the narrator.
Rules:
- Write in the language of the description.
- Written language: no filler words (um, uh, er, 嗯, 啊, 呃, 那个, 就是说), no slang, no colloquial padding, no repetition.
- No stage directions, sound effects, emojis or Markdown other than the title line.
- Short names, the same spelling every time.`

// storyWriteReady reports whether 「帮我写」 shows: the writing help's
// model on this Mac is downloaded.
func (a *app) storyWriteReady() bool { return a.llmRouter != nil && a.localLLMReady() }

// storyWriteToggle is the composer's 「帮我写」 chip.
func (a *app) storyWriteToggle(c *ui.Context, p *lfPage) {
	t := c.Theme()
	k := tok(t)
	on := p.helpWrite
	b := ghostBase(c.Key("st-help-write")).Gap(px(t, 4)).AlignItems(ui.Center).Padding(px(t, 3), px(t, sp2)).Radius(px(t, 12)).Border(1, k.Line).
		Label(i18n.T("Help me write")).Tooltip(i18n.T("The writing help turns your description into a short script for two or three voices.")).Checked(on).Disabled(p.writing)
	if on {
		b.Background(k.Selected).Border(1, k.Text3)
	}
	b.Children(func() {
		ui.Icon(c, icons.MagicStick3.Linear).Size(px(t, 13), px(t, 13)).TextColor(k.Text2)
		ui.Text(c, i18n.T("Help me write")).FontSize(px(t, fsCaption)).SingleLine()
	})
	if b.Clicked() {
		p.helpWrite = !p.helpWrite
	}
}

// storyWriteProgress is the composer's line while the draft is written.
func (a *app) storyWriteProgress(c *ui.Context, p *lfPage) {
	t := c.Theme()
	ui.Row(c.Key("st-writing")).Gap(px(t, sp2)).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
		ui.Spinner(c)
		truncText(c, i18n.T("Writing a draft…")).FontSize(px(t, fsCaption)).Grow(1)
		if ghostButton(c.Key("st-writing-stop"), i18n.T("Cancel")).Clicked() {
			a.storyWriteCancel(p)
		}
	})
}

// storyWriteCancel stops the draft being written; the description stays.
func (a *app) storyWriteCancel(p *lfPage) {
	if p.writeStop != nil {
		p.writeStop()
	}
	p.writing, p.writeStop = false, nil
}

// storyWrite drafts a script from the description, then makes the story
// of it and opens it.
func (a *app) storyWrite(p *lfPage, desc string) {
	if p.writing || strings.TrimSpace(desc) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storyWriteTimeout)
	p.writing, p.writeStop = true, cancel
	router := a.llmRouter
	oplog.Info("story.write", "chars", len([]rune(desc)))
	a.async(func() {
		resp, err := router.Chat(ctx, storyWriteSkill, llm.Request{
			Messages:    []llm.Message{{Role: llm.System, Content: storyWritePrompt}, {Role: llm.User, Content: desc}},
			Temperature: llm.F64(0.7), MaxTokens: 1200,
		})
		stopped := ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded)
		cancel()
		draft := ""
		if err == nil {
			draft = cleanStoryDraft(resp.Text)
			if draft == "" {
				err = errors.New(i18n.T("the draft had no lines for voices"))
			}
		}
		a.post(func() {
			if !p.writing || stopped { // cancelled: nothing made
				oplog.Info("story.write", "cancelled", true)
				return
			}
			p.writing, p.writeStop = false, nil
			if err != nil {
				oplog.Err("story.write", err)
				a.toastWarn(i18n.T("The writing help could not write a draft: %v", err))
				return
			}
			a.storyFromDraft(p, draft)
		})
	})
}

// storyFromDraft makes a story of a draft: its lines cast, then opened.
func (a *app) storyFromDraft(p *lfPage, draft string) {
	if a.lfCreate(p, func() {
		voices := a.lfVoices(entryFor(p.doc.Model))
		if title, _ := storyFromMarkup(&p.doc, draft, voices); title != "" {
			p.doc.Title = title
		}
		p.open["review"], p.open["cast"] = false, true
	}) {
		p.entryText = ""
	}
}

var (
	voiceTag   = regexp.MustCompile(`^\s*\[voice:\s*([^\]]+?)\s*\]\s*`)
	listPrefix = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)])\s+`)
)

// cleanStoryDraft keeps a model's draft to the script: no code fences or
// list marks; "" when no line is a character's.
func cleanStoryDraft(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "</think>"); i >= 0 { // a reasoning model's thoughts
		s = strings.TrimSpace(s[i+len("</think>"):])
	}
	var out []string
	voiced := false
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "```") {
			continue
		}
		l = listPrefix.ReplaceAllString(l, "")
		if voiceTag.MatchString(l) {
			voiced = true
		}
		out = append(out, l)
	}
	if !voiced {
		return ""
	}
	return strings.Join(out, "\n")
}

// storyFromMarkup appends a script in the Stories markup to d: "# Title"
// first (returned), "[voice:Name] line" a character's line (cast with
// voices: new characters get voices nobody plays yet), any other line
// narration. lines is how many lines were added.
func storyFromMarkup(d *services.LongformDoc, script string, voices []lfVoice) (title string, lines int) {
	var b strings.Builder
	for i, l := range strings.Split(strings.TrimSpace(script), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if i == 0 && strings.HasPrefix(l, "#") {
			title = strings.TrimSpace(strings.TrimLeft(l, "#"))
			continue
		}
		if m := voiceTag.FindStringSubmatch(l); m != nil {
			l = strings.TrimSpace(l[len(m[0]):])
			if name := strings.ToLower(m[1]); name != "default" && name != "narrator" && name != "旁白" {
				l = "[" + m[1] + "] " + l // autocast's tagged line
			}
		}
		b.WriteString(l + "\n\n")
	}
	lines, _ = autoCastText(d, b.String(), voices)
	return title, lines
}
