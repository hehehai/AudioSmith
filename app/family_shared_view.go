package main

// The views of the shared settings (family_shared.go; family spec §9.3,
// §9.4, §15.2 step 5): the scope line under the language, model folder,
// download source and proxy rows — "Applies to all ActNow apps · Use a
// different setting for this app", or "Only for this app · Follow other
// ActNow apps", or once after the migration "Dicta uses a different
// proxy. [Use theirs]" — and the Models page's "Move models…" banner with
// the move's progress. All of it only with another ActNow app's release
// build installed (sharedUI): otherwise the rows are as they always were.

import (
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/jobs"
	famset "github.com/hehehai/actnowkit/settings"
)

// sharedScope is the line under a shared setting's row (key: the
// family's key).
func (a *app) sharedScope(c *ui.Context, key string) {
	if !a.sharedUI() {
		return
	}
	settingFoot(c, "scope-foot-"+key, func() { a.sharedScopeLine(c, key) })
}

// sharedScopeLine is sharedScope's line: whom the setting applies to,
// and the link that changes it.
func (a *app) sharedScopeLine(c *ui.Context, key string) {
	t := c.Theme()
	ui.Row(c.Key("scope-" + key)).Wrap().GapX(px(t, sp2)).GapY(px(t, 2)).AlignItems(ui.Center).Children(func() {
		if a.prefs.Family.Follows(key) {
			caption(c, i18n.T("Applies to all ActNow apps"))
			if scopeLink(c.Key("scope-own"), i18n.T("Use a different setting for this app")).Clicked() {
				a.useOwnSetting(key)
			}
			return
		}
		if fv, ok := a.famShared.Conflict(a.prefs, key); ok && a.prefs.Family.OtherValue[key] && key != famset.KeyModelsRoot {
			// Once after the migration (forgotten when the dialog closes).
			if !slices.Contains(a.otherShown, key) {
				a.otherShown = append(a.otherShown, key)
			}
			theirs := sharedValueText(key, fv)
			caption(c, a.otherValueText(key)).Tooltip(theirs)
			if secondaryButton(c.Key("scope-theirs"), i18n.T("Use theirs")).Tooltip(theirs).Clicked() {
				a.followFamily(key)
			}
			return
		}
		caption(c, i18n.T("Only for this app"))
		if scopeLink(c.Key("scope-follow"), i18n.T("Follow other ActNow apps")).Clicked() {
			a.followFamily(key)
		}
	})
}

// otherValueText is "Dicta uses a different proxy.", or "Other ActNow
// apps use a different proxy." when the app is not known (spec §7.10).
func (a *app) otherValueText(key string) string {
	if by := a.famShared.By[key]; by != "" {
		return i18n.T("%s uses a different %s.", by, sharedNoun(key))
	}
	return i18n.T("Other ActNow apps use a different %s.", sharedNoun(key))
}

// scopeLink is a small text action beside a caption.
func scopeLink(c *ui.Context, label string) ui.Element {
	t := c.Theme()
	k := tok(t)
	b := ghostBase(c).Padding(px(t, 2), px(t, 6)).Label(label)
	b.Children(func() {
		ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.AccentInk).SingleLine()
	})
	return b
}

// moveModelsBanner heads the Models page while the family keeps models
// in another folder than AudioSmith (spec §15.2 step 5), and while
// AudioSmith moves its models there.
func (a *app) moveModelsBanner(c *ui.Context) {
	if j := a.modelsMoveJob; j != nil {
		a.modelsMoveProgress(c, *j)
		return
	}
	if !a.sharedUI() {
		return
	}
	key := famset.KeyModelsRoot
	fv, ok := a.famShared.Conflict(a.prefs, key)
	if !ok || !a.prefs.Family.OtherValue[key] {
		return
	}
	path := sharedValueText(key, fv)
	text := i18n.T("Other ActNow apps keep models in %s. Move AudioSmith's models there to share them?", path)
	if by := a.famShared.By[key]; by != "" {
		text = i18n.T("%s keeps models in %s. Move AudioSmith's models there to share them?", by, path)
	}
	notice(c, "move-models", noticeInfo, text, "", func() {
		if secondaryButton(c.Key("move-models"), i18n.T("Move models…")).Disabled(a.modelsMoveLoading).Clicked() {
			a.planModelsMove()
		}
		if ghostButton(c.Key("keep-separate"), i18n.T("Keep separate")).Clicked() {
			a.keepSeparate()
		}
	}, nil)
	a.modelsMoveDialog(c)
}

// modelsMoveDialog confirms Move models… with what it moves.
func (a *app) modelsMoveDialog(c *ui.Context) {
	if !a.modelsMoveAsk {
		return
	}
	p := a.modelsMovePlan
	msg := i18n.N(p.Models, "%d model (%s) moves there; what the folder has already is not copied again. AudioSmith then keeps its models there, with the other ActNow apps. You can cancel: what has moved stays there.",
		"%d models (%s) move there; what the folder has already is not copied again. AudioSmith then keeps its models there, with the other ActNow apps. You can cancel: what has moved stays there.", p.Models, mb(p.Bytes))
	switch ui.AlertDialog(c.Key("move-ask"), &a.modelsMoveAsk, i18n.T("Move AudioSmith's models to %s?", p.To), msg, i18n.T("Cancel"), i18n.T("Move")) {
	case 1:
		a.startModelsMove(p)
	}
}

// modelsMoveProgress is the banner while the models move: how far, what it
// waits for, Cancel.
func (a *app) modelsMoveProgress(c *ui.Context, j jobs.Job) {
	t := c.Theme()
	p := j.Progress
	to := sharedValueText(famset.KeyModelsRoot, a.famShared.Values[famset.KeyModelsRoot])
	text := i18n.T("Moving AudioSmith's models to %s…", to)
	switch {
	case p.Message != "" && p.Message != "?":
		text = i18n.T("Waiting for %s to finish with a model…", p.Message)
	case p.Message == "?":
		text = i18n.T("Waiting for another app to finish with a model…")
	case p.TotalBytes > 0:
		text += fmt.Sprintf(" · %s / %s", mb(p.Bytes), mb(p.TotalBytes))
	}
	banner(c.Key("move-progress"), icons.Refresh, tok(t).Text2, text, func() {
		if p.Fraction >= 0 {
			ui.Progress(c, p.Fraction).Width(px(t, 120)).Label(i18n.T("Move models"))
		}
		if secondaryButton(c.Key("move-cancel"), i18n.T("Cancel")).Clicked() {
			a.cancelModelsMove()
		}
	}).Shrink(0)
}
