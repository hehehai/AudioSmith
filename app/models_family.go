package main

// ActNow family apps that share the model folder (the family spec §7.6,
// §7.10; internal/hub family.go and delete.go, internal/family): the
// delete dialog of a model they use, what Clean up and Settings →
// Storage → Erase everything keep for them, and the words for a model
// another app is downloading. Other apps are named only when a release
// build of one is installed (family_app.go familyNames).

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/actnowkit/models"
)

// keptReason says in the log why Clean up kept l.
func keptReason(l hub.Leftover) string {
	if l.Busy {
		return "its lock is held by " + cmp.Or(l.BusyBy, "another process")
	}
	return "used by " + strings.Join(l.UsedBy, ", ")
}

// busyText says another app holds a model's lock (hub.Holder): it
// downloads or deletes the model now.
func busyText(holder string) string {
	if holder == "" {
		holder = i18n.T("Another app")
	}
	return i18n.T("%s is downloading or using this model. Try again later.", holder)
}

// catalogRepos are the repos of the catalogue's models and their
// auxiliary repos.
func catalogRepos() []string {
	var out []string
	for _, e := range catalog.Builtin().Entries {
		for _, r := range append([]string{e.Repo}, auxRepos(e)...) {
			if !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

func auxRepos(e catalog.Entry) []string {
	var out []string
	for _, x := range e.Aux {
		out = append(out, x.Repo)
	}
	return out
}

// eraseKeeps finds, in the background, the models on disk that other
// ActNow apps use, which "Erase everything" with the models keeps, for
// its confirmation (spec §7.6 step 3); said only when it can name them
// (spec §7.10).
func (a *app) eraseKeeps() {
	opts := a.hubClient().Options()
	a.resetKeeps = ""
	a.async(func() {
		ls, _ := hub.Leftovers(opts.CacheDir, catalogRepos(), opts.Family)
		a.post(func() { a.resetKeeps = a.keepsText(ls) })
	})
}

// keepsText lists the models of ls other apps use: "These models are also
// used by Dicta and will be kept: …".
func (a *app) keepsText(ls []hub.Leftover) string {
	var names, apps []string
	var bytes int64
	for _, l := range ls {
		users := a.familyNames(l.Users)
		if !l.InUse || len(users) == 0 {
			continue
		}
		bytes += l.Bytes
		if name := catalog.Builtin().DisplayName(l.Repo); !slices.Contains(names, name) {
			names = append(names, name)
		}
		for _, u := range users {
			if !slices.Contains(apps, u) {
				apps = append(apps, u)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return i18n.T("These models are also used by %s and will be kept: %s (%s)", listJoin(apps), listJoin(names), humanBytes(bytes))
}

// removeSharedDialog asks how to delete e, which other ActNow apps use
// (spec §7.10.3): "Remove from AudioSmith" (the default: their files
// stay), "Delete for all apps…" (confirmed again: they would download it
// again), or Cancel.
func (a *app) removeSharedDialog(c *ui.Context, e catalog.Entry) {
	t := c.Theme()
	open := a.deleteAsk
	ui.Modal(c.Key("delete-shared"), &open, func() {
		ui.Column(c).Width(px(t, 460)).Gap(px(t, sp3)).Children(func() {
			if a.deleteAllAsk {
				ui.Text(c, i18n.T("Delete %s for all apps?", modelRef(e))).Bold()
				ui.Text(c, i18n.T("%s will need to download it again (%s).", listJoin(a.deleteShared), mb(a.deleteFree))).TextColor(t.TextMuted)
				ui.Row(c).Gap(px(t, sp2)).Justify(ui.End).Children(func() {
					if secondaryButton(c.Key("delete-all-cancel"), i18n.T("Cancel")).AutoFocus().Clicked() {
						open = false
					}
					if recordButton(c.Key("delete-all-ok"), i18n.T("Delete")).Clicked() {
						open = false
						a.deleteModel(e, models.AllApps)
					}
				})
				return
			}
			ui.Text(c, i18n.T("Remove %s from AudioSmith?", modelRef(e))).Bold()
			ui.Text(c, sharedText(a.deleteShared, a.model(e).local.Bytes)).TextColor(t.TextMuted)
			ui.Row(c).Gap(px(t, sp2)).Justify(ui.End).Children(func() {
				if secondaryButton(c.Key("delete-cancel"), i18n.T("Cancel")).Clicked() {
					open = false
				}
				if secondaryButton(c.Key("delete-all"), i18n.T("Delete for all apps…")).Clicked() {
					a.deleteAllAsk = true
				}
				if primaryButton(c.Key("delete-remove"), i18n.T("Remove from AudioSmith")).AutoFocus().Clicked() {
					open = false
					a.deleteModel(e, models.ThisApp)
				}
			})
		})
	})
	if !open {
		a.deleteAsk, a.deleteAllAsk = false, false
	}
}

// waitingText says a download waits for another app that downloads a
// file it needs (spec §7.10.4).
func waitingText(p hub.Progress) string {
	pct := 0
	if p.TotalBytes > 0 {
		pct = int(100 * p.Bytes / p.TotalBytes)
	}
	app := p.WaitingFor
	if app == "" {
		app = i18n.T("another app")
	}
	return i18n.T("Downloading in %s… %d%%", app, pct)
}

// sharedText says that apps (other ActNow apps) use a model, so its files
// (bytes) stay (spec §7.10.3).
func sharedText(apps []string, bytes int64) string {
	return i18n.T("%s also uses this model, so its files (%s) stay on this Mac.", listJoin(apps), mb(bytes))
}
