package main

// The agent skill's card on the agent pages (thread #34), under the
// setup prompt: what the skill is, Install skill… (Claude Code, Codex,
// Cursor: writes the user-level SKILL.md after a confirmation), Save
// skill… and Copy skill. integrations_skill*.go make the text.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
)

// skillWhere is the ⓘ of the card: where the agent reads skills.
func skillWhere(slug string) string {
	switch slug {
	case "claude-code":
		return i18n.T("Claude Code reads skills from %s (all projects) or a project's %s.", "~/.claude/skills", ".claude/skills")
	case "codex":
		return i18n.T("Codex reads skills from %s (all projects) or a repository's %s.", "~/.agents/skills", ".agents/skills")
	case "cursor":
		return i18n.T("Cursor reads skills from %s (all projects) or a project's %s; it also reads %s and %s.", "~/.cursor/skills", ".cursor/skills", "~/.claude/skills", "~/.agents/skills")
	}
	return i18n.T("Agents that read Agent Skills want a folder named %s with this SKILL.md in their skills folder, often %s. For other agents, paste the text after its frontmatter into their instructions.", skillName, "~/.agents/skills")
}

// agentSkillCard is the skill's card of an agent page.
func (a *app) agentSkillCard(c *ui.Context, in integration) {
	t := c.Theme()
	k := tok(t)
	ag := promptAgents[in.Slug]
	env := a.promptEnvOf()
	files := agentSkillFiles(in.Slug, env)
	name := ag.Name
	if ag.Client == "" {
		name = i18n.T("your agent")
	}
	dir, perr := skillTarget(in.Slug)
	canInstall := ag.Client != "" && perr == nil
	card(c.Key("integ-skill")).Children(func() {
		ui.Column(c).Gap(px(t, sp1)).Children(func() {
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
				sectionHeading(c, i18n.T("Agent skill"))
				infoTip(c.Key("integ-skill-where"), skillWhere(in.Slug))
			})
			caption(c, i18n.T("A skill (SKILL.md and a tool reference) that tells %s, in every new chat, what AudioSmith does and how to use it: its pages and tools, your voices, long jobs, recipes and manners. The setup prompt installs it; you can also do it here.", name)).Wrap()
		})
		ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
			if canInstall {
				l := i18n.T("Install skill")
				if secondaryIconButton(c.Key("integ-skill-install"), icons.DownloadMinimalistic, l).Label(l).Clicked() {
					a.skillAsk = true
				}
			}
			save := i18n.T("Save skill")
			if secondaryIconButton(c.Key("integ-skill-save"), icons.Export, save).Label(save).Clicked() {
				// A ZIP of the skill's folder: what claude.ai and Claude
				// Desktop upload, and what any agent's skills folder unzips.
				if data, err := agentSkillZip(in.Slug, env); err != nil {
					a.toastWarn(i18n.T("Could not save: %v", err))
				} else {
					a.saveBlock(integBlock{Code: string(data), File: skillName + "-skill.zip"})
				}
			}
			cp := i18n.T("Copy skill")
			if ghostIconButton(c.Key("integ-skill-copy"), icons.Copy, cp).Label(cp).Clicked() {
				c.Services().WriteClipboard(agentSkillText(in.Slug, env))
				a.toast(i18n.T("Copied"))
			}
		})
		if canInstall {
			switch skillState(dir, files) {
			case "same":
				ui.Row(c.Key("integ-skill-state")).Gap(px(t, 6)).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(k.Success).Shrink(0)
					truncText(c, i18n.T("Installed: %s", skillDir(in.Slug)+"/")).FontSize(px(t, fsCaption)).TextColor(k.Text2)
				})
			case "differs":
				caption(c.Key("integ-skill-state"), i18n.T("%s holds another version (your voices or settings changed since): install again to update it.", skillDir(in.Slug)+"/")).Wrap()
			}
		}
	})
	if !canInstall {
		return
	}
	state := skillState(dir, files)
	dialogFrame(c, "skill-dialog", &a.skillAsk, i18n.T("Install the skill for %s?", ag.Name), skillDir(in.Slug)+"/", px(t, 480), func() {
		ui.Text(c, i18n.T("AudioSmith writes the skill to %s (SKILL.md and %s). %s reads it in every new chat; it holds no API key.", skillDir(in.Slug)+"/", skillRefPath, ag.Name)).Wrap()
		switch state {
		case "differs":
			caption(c, i18n.T("Different files are there: each is kept beside it as a backup (SKILL.md.bak-…).")).Wrap()
		case "same":
			caption(c, i18n.T("This skill is already installed: nothing changes.")).Wrap()
		}
	}, func() {
		ui.Box(c).Grow(1)
		if ghostButton(c.Key("skill-cancel"), i18n.T("Cancel")).Clicked() {
			a.skillAsk = false
		}
		if primaryButton(c.Key("skill-install"), i18n.T("Install")).Clicked() {
			a.skillAsk = false
			if _, err := installSkill(dir, files); err != nil {
				a.toastWarn(i18n.T("Could not save: %v", err))
				return
			}
			a.toast(i18n.T("Skill installed: start a new chat in %s", ag.Name))
		}
	})
}
