package main

import (
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// apiStatusLine says whether the Local API runs, with a link to its settings.
func (a *app) apiStatusLine(c *ui.Context) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c.Key("api-line")).Gap(px(t, sp2)).AlignItems(ui.Center).Padding(px(t, 6), px(t, 6), px(t, 6), px(t, sp3)).Radius(px(t, radiusControl)).Background(k.Subtle).Children(func() {
		switch {
		case !a.prefs.API.Enabled:
			statusDot(c, t.Warning)
			ui.Text(c, i18n.T("The Local API is off: these setups work once it runs.")).FontSize(px(t, fsCaption)).Grow(1).MinWidth(0).Wrap()
		case a.apiErr != "":
			statusDot(c, t.Danger)
			ui.Text(c, a.apiErr).FontSize(px(t, fsCaption)).TextColor(t.Danger).Grow(1).MinWidth(0).Wrap()
		default:
			statusDot(c, t.Success)
			truncText(c, i18n.T("Local API at %s", a.apiBaseURL())).FontSize(px(t, fsCaption)).Grow(1)
		}
	})
}

// integrationDetail is Settings → Integrations → one of them
// (/settings/integrations/{slug}): what it is, its setup prompt and skill
// (agents), the setup's blocks.
func (a *app) integrationDetail(c *ui.Context, slug string) {
	t := c.Theme()
	in, ok := integrationBySlug(slug)
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if ghostIconButton(c.Key("integ-back"), icons.AltArrowLeft, i18n.T("All integrations")).Label(i18n.T("All integrations")).Clicked() {
			a.navigate("/settings/integrations")
		}
		if ok {
			ui.Text(c, in.Name).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0)
		}
	})
	ui.Column(c).Gap(px(t, sp4)).Children(func() {
		if !ok {
			emptyState(c, icons.Magnifer, i18n.T("No matches"), "", nil)
			return
		}
		ui.Text(c, integAbout(in.Slug)).Wrap()
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.SquareArrowRightUp.Linear).Size(px(t, 14), px(t, 14)).TextColor(tok(t).Text2)
			ui.Link(c.Key("integ-site"), in.URL, in.URL).FontSize(px(t, fsCaption))
		})
		a.apiStatusLine(c)
		if a.apiHasKey && a.apiSrv != nil && a.apiSrv.Key() != "" {
			settingRow(c, "integ-key", i18n.T("Put my API key in the setups"), i18n.T("Off: setups read it from the AUDIOSMITH_API_KEY environment variable when a key is required. Anyone who sees a setup with the key can use AudioSmith."), func() {
				inkSwitch(c.Key("integ-key-switch"), &a.apiForm.keyIncl).Label(i18n.T("Put my API key in the setups"))
			})
		}
		blocks := func() {
			for i, blk := range in.Blocks(a.integEnvOf()) {
				a.codeBlock(c, in.Slug+"-"+itoa(i), blk)
			}
		}
		if _, ok := promptAgents[in.Slug]; ok { // thread #33: the agent setup prompt, the manual setup folded below it
			a.agentPromptCard(c, in)
			a.agentSkillCard(c, in) // thread #34
			collapsible(c.Key("integ-manual"), i18n.T("Manual setup"), &a.integManual, nil, blocks)
		} else {
			blocks()
		}
		if slices.Contains(in.Caps, "MCP server") {
			ui.Column(c.Key("integ-voices")).Children(func() {
				banner(c, icons.UserSpeakRounded, tok(t).Text2, i18n.T("Give each agent its own voice under Local API, on the list of integrations."), func() {
					if ghostButton(c.Key("integ-voices-go"), i18n.T("Open")).Clicked() {
						a.navigate("/settings/integrations")
					}
				})
			})
		}
	})
}

// codeBlock shows a setup block: its title and hint, the code, Copy and
// Save….
func (a *app) codeBlock(c *ui.Context, key string, blk integBlock) {
	t := c.Theme()
	ui.Column(c.Key("block-" + key)).Gap(px(t, 6)).Children(func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, blk.Title).FontWeight(600).Grow(1).MinWidth(0)
			if blk.Hint != "" {
				infoTip(c.Key("hint"), blk.Hint)
			}
		})
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Start).Padding(px(t, sp3), px(t, 6), px(t, sp3), px(t, sp3)).Radius(px(t, radiusControl)).Background(tok(t).Subtle).Children(func() {
			ui.Text(c, blk.Code).Font(monoFont).FontSize(px(t, fsCaption)).Wrap().Selectable().Grow(1).MinWidth(0)
			if blk.File != "" {
				if iconButton(c.Key("save"), icons.Export, i18n.T("Save…")).Clicked() {
					a.saveBlock(blk)
				}
			}
			if iconButton(c.Key("copy"), icons.Copy, i18n.T("Copy")).Clicked() {
				c.Services().WriteClipboard(blk.Code)
				a.toast(i18n.T("Copied"))
			}
		})
	})
}

// agentPromptCard is the agent setup prompt of a page (thread #33): what
// it does, the text, Copy prompt and Save as .txt….
func (a *app) agentPromptCard(c *ui.Context, in integration) {
	t := c.Theme()
	k := tok(t)
	p := a.promptEnvOf()
	text := agentPrompt(in.Slug, p)
	name := promptAgents[in.Slug].Name
	if promptAgents[in.Slug].Client == "" {
		name = i18n.T("your agent")
	}
	card(c.Key("integ-prompt")).Children(func() {
		ui.Column(c).Gap(px(t, sp1)).Children(func() {
			sectionHeading(c, i18n.T("Agent setup prompt"))
			caption(c, i18n.T("Copy this text and paste it into %s: it checks that AudioSmith runs, adds AudioSmith's MCP server to its configuration, installs AudioSmith's skill, tests it and tells you what it changed. Nothing to set up by hand.", name)).Wrap()
		})
		note := func(ic icons.Icon, col ui.Color, s string) {
			ui.Row(c.Key("integ-prompt-key")).Gap(px(t, 6)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, ic.Linear).Size(px(t, 14), px(t, 14)).TextColor(col).Shrink(0).Margin(px(t, 1), 0, 0, 0)
				ui.Text(c, s).FontSize(px(t, fsCaption)).TextColor(col).Wrap().Grow(1).MinWidth(0)
			})
		}
		switch p.keyMode() {
		case keyIncluded:
			note(icons.DangerTriangle, k.Warning, i18n.T("The prompt contains your API key: paste it only into an agent you trust."))
		case keyKeychain:
			note(icons.ShieldCheck, k.Text2, i18n.T("AudioSmith requires its key: the agent reads it from the Keychain (macOS may ask you to allow it) and keeps it out of shared files."))
		}
		// The actions above the text, so that they show without scrolling
		// in a small window.
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			copyL := i18n.T("Copy prompt")
			if primaryIconButton(c.Key("integ-prompt-copy"), icons.Copy, copyL).Label(copyL).Clicked() {
				c.Services().WriteClipboard(text)
				a.toast(i18n.T("Copied: paste it into %s", name))
			}
			save := i18n.T("Save as .txt…")
			if secondaryIconButton(c.Key("integ-prompt-save"), icons.Export, save).Label(save).Clicked() {
				a.saveBlock(integBlock{Code: text, File: agentPromptFile(in.Slug)})
			}
		})
		_, h := c.Size()
		ui.Scroll(c.Key("integ-prompt-text")).MaxHeight(max(px(t, 120), min(px(t, 240), h*0.35))).Radius(px(t, radiusControl)).Background(k.Subtle).Children(func() {
			ui.Text(c, text).Font(monoFont).FontSize(px(t, fsCaption)).Wrap().Selectable().Padding(px(t, sp3))
		})
	})
}

// integSaveDir is where tests save blocks (no save panel).
var integSaveDir string

// saveBlock writes a block to a file.
func (a *app) saveBlock(blk integBlock) {
	if a.win == nil {
		if integSaveDir != "" {
			if err := saveSnippet(filepath.Join(integSaveDir, blk.File), blk.Code); err != nil {
				a.toastWarn(i18n.T("Could not save: %v", err))
				return
			}
			a.toast(i18n.T("Saved"))
		}
		return
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: i18n.T("Save…"), DefaultPath: blk.File})
		if err != nil || path == "" {
			return
		}
		err = saveSnippet(path, blk.Code)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save: %v", err))
				return
			}
			a.toast(i18n.T("Saved"))
		})
	}()
}
