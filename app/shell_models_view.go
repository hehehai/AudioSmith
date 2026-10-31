package main

// Settings → Models & network's list (the Models page it was): the catalogue by kind, each entry's card with
// what of it is on disk, its details and actions, and the delete alert.
// The state, the downloads and the licence dialog are shell_models.go.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/catalog"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/licence"
	"github.com/hehehai/actnowkit/models"
)

// modelsList is the head of Settings → Models & network (the Models
// page it was): the catalogue by kind, searchable, each entry's card.
func (a *app) modelsList(c *ui.Context) {
	t := c.Theme()
	entries := catalog.Builtin().Entries
	counts := make([]int, len(modelKinds))
	for _, e := range entries {
		counts[0]++
		counts[modelKindOf(e)]++
	}
	labels := make([]string, len(modelKinds))
	for i, k := range modelKinds {
		labels[i] = k() + "  " + itoa(counts[i])
	}
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		if a.pageWidth(c) < px(t, 520) {
			// Too narrow for the tabs: the same choice as a select.
			cur := labels[a.modelsKind]
			if ui.Select(c.Key("models-kind-select"), &cur, labels).Label(i18n.T("Kind of model")).Width(px(t, 150)).Shrink(0).Changed() {
				a.modelsKind = max(slices.Index(labels, cur), 0)
			}
		} else {
			underlineTabs(c.Key("models-kind"), &a.modelsKind, labels...).Label(i18n.T("Kind of model")).Shrink(0)
		}
		ui.Spacer(c)
		if iconButton(c.Key("models-refresh"), icons.Refresh, i18n.T("Look again at what is on disk")).Clicked() {
			a.refreshModels()
		}
		moreMenu(c.Key("models-more"), "", func(m *ui.Menu) {
			if m.Item(i18n.T("Open model folder")).Chosen() {
				mygo.Shell.OpenPath(a.hubClient().Options().CacheDir)
			}
			if m.Item(i18n.T("Download source and proxy")).Chosen() {
				a.settingsReveal = "network" // under the list
			}
		})
	})
	ui.Row(c).Children(func() {
		searchField(c, "models-search", &a.modelsFind, i18n.T("Search models by name, language or use"), px(t, 420)).Label(i18n.T("Search models"))
	})
	ui.Column(c.Key("models-list-" + itoa(a.modelsKind))).Gap(px(t, sp2)).Children(func() {
		find := strings.TrimSpace(a.modelsFind)
		if find == "" {
			a.moveModelsBanner(c) // family_shared_view.go: the family's model folder
		}
		if find == "" && a.removedShows(a.modelsKind) {
			a.removedRow(c) // removed models still on disk
		}
		n := 0
		for _, e := range entries {
			if (a.modelsKind == 0 || modelKindOf(e) == a.modelsKind) && e.Matches(find) {
				a.modelCard(c, e)
				n++
			}
		}
		if n == 0 && find != "" {
			caption(c, i18n.T("No model matches")).TextColor(tok(t).Text3).Padding(px(t, sp3), 0)
		}
	})
}

// modelKinds are the Models page's tabs: all, then speech synthesis,
// speech recognition (with word alignment), language models and the
// rest (separation, speakers, VAD, enhancement, watermark).
var modelKinds = []func() string{
	func() string { return i18n.T("All") },
	func() string { return i18n.X("model kind", "Speech") },
	func() string { return i18n.X("model kind", "Transcription") },
	func() string { return i18n.X("model kind", "Writing") },
	func() string { return i18n.T("Other") },
}

// modelKindOf is the tab (an index of modelKinds) a catalogue entry is in.
func modelKindOf(e catalog.Entry) int {
	switch {
	case slices.Contains(e.Tasks, catalog.TaskTTS):
		return 1
	case slices.Contains(e.Tasks, catalog.TaskSTT), slices.Contains(e.Tasks, catalog.TaskAlign):
		return 2
	case slices.Contains(e.Tasks, catalog.TaskLLM):
		return 3
	}
	return 4
}

func (a *app) modelCard(c *ui.Context, e catalog.Entry) {
	t := c.Theme()
	m := a.model(e)
	eng := a.engineSummary()
	loaded := false
	for _, r := range eng.Loaded {
		loaded = loaded || r == e.Repo
	}
	state := func() {
		ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
			st := m.local.State
			switch {
			case m.downloading:
			case st == hub.Downloaded:
				ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Success)
			case st == hub.Partial:
				ui.Icon(c, icons.DangerTriangle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Warning)
			}
			caption(c, a.modelStateText(e, m)).SingleLine()
		})
	}
	narrow := a.pageWidth(c) < px(t, 640)
	card(c.Key("model-"+e.ID)).Padding(px(t, sp3), px(t, sp4)).Children(func() {
		ui.Row(c).Gap(px(t, sp4)).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, sp1)).Children(func() {
				a.modelInfo(c, e, m, loaded)
			})
			if narrow {
				return
			}
			ui.Column(c).Shrink(0).Gap(px(t, sp2)).AlignItems(ui.End).Children(func() {
				state()
				ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
					a.modelActions(c, e, m, loaded)
				})
			})
		})
		if narrow {
			// The state and the actions go under the description.
			ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
				state()
				ui.Spacer(c)
				a.modelActions(c, e, m, loaded)
			})
		}
		if m.downloading {
			frac := -1.0
			if m.progress.TotalBytes > 0 {
				frac = float64(m.progress.Bytes) / float64(m.progress.TotalBytes)
			}
			ui.Progress(c, frac).Label(i18n.T("Downloading %s", modelRef(e)))
			info := i18n.T("%s of %s", mb(m.progress.Bytes), mb(m.progress.TotalBytes))
			if m.progress.Mirror != "" {
				info += " · " + strings.TrimPrefix(m.progress.Mirror, "https://")
			}
			truncText(c, info).FontSize(px(t, fsMicro)).TextColor(tok(t).Text2)
		}
		if m.err != "" && !m.downloading {
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, icons.DangerCircle.Linear).Size(px(t, 14), px(t, 14)).TextColor(t.Danger).Shrink(0)
				ui.Text(c, m.err).FontSize(px(t, fsCaption)).TextColor(t.Danger).Selectable().Grow(1).MinWidth(0)
			})
		}
	})
}

// modelInfo describes an entry: its friendly name and tags, what it is
// for, what it speaks and needs, whether its licence allows commercial
// use, and under Details its technical identity (thread #45): the
// technical name, family, quantisation, size, repo, revision, licence.
func (a *app) modelInfo(c *ui.Context, e catalog.Entry, m *modelState, loaded bool) {
	t := c.Theme()
	k := tok(t)
	ui.Row(c).Gap(px(t, sp2)).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, e.DisplayName()).FontWeight(600).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1).Tooltip(e.TechnicalSummary())
		modelTags(c, e, 0)
		if e.Hidden {
			tagChip(c, i18n.T("Preview"))
		}
		if e.NeedsReference {
			tagChip(c, i18n.T("Needs a reference clip"))
		}
		if loaded {
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
				statusDot(c, t.Success)
				ui.Text(c, i18n.T("In memory")).FontSize(px(t, fsMicro)).TextColor(k.Text2)
			})
		}
	})
	about := e.Description()
	ui.Text(c, about).FontSize(px(t, fsCaption)).TextColor(k.Text2).MaxLines(2).Ellipsis("…").Tooltip(about)
	var facts []string
	if n := len(e.Languages); n > 0 {
		facts = append(facts, i18n.N(n, "%d language", "%d languages", n))
	}
	if n := len(e.VoiceIDs()); n > 0 {
		facts = append(facts, i18n.N(n, "%d voice", "%d voices", n))
	}
	if s := catalog.HumanSize(e.Size); s != "" {
		facts = append(facts, i18n.T("%s download", s))
	}
	if e.MinMemoryGB > 0 {
		facts = append(facts, i18n.T("needs %g GB of memory", e.MinMemoryGB))
	}
	truncText(c, strings.Join(facts, " · ")).FontSize(px(t, fsMicro)).TextColor(k.Text3)
	if line := a.familyLine(e, m); line != "" {
		truncText(c, line).FontSize(px(t, fsMicro)).TextColor(k.Text2) // family_reuse.go
	}

	// The licence.
	ui.Row(c).Gap(px(t, 6)).AlignItems(ui.Center).Wrap().Children(func() {
		lic := e.Licence
		use := i18n.T("commercial use allowed")
		color := t.Success
		if !lic.Commercial {
			use, color = i18n.T("non-commercial use only"), t.Warning
		}
		statusDot(c, color)
		ui.Text(c, use).FontSize(px(t, fsCaption))
		if lic.RequiresAcceptance {
			if licence.Covers(a.accepted, e) {
				ui.Text(c, "· "+i18n.T("accepted")).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			} else if ghostButton(c.Key("licence"), i18n.T("Review licence…")).FontSize(px(t, fsCaption)).Clicked() {
				a.askLicence(e, nil)
			}
		}
		if a.modelsDetail == nil {
			a.modelsDetail = map[string]bool{}
		}
		open := a.modelsDetail[e.ID]
		label := i18n.T("Details")
		b := ui.ButtonBase(c.Key("details")).Gap(px(t, 2)).AlignItems(ui.Center).Padding(px(t, 1), px(t, 4)).Radius(px(t, 4)).Label(label).Checked(open)
		if b.Hovered() {
			b.Background(k.Hover)
		}
		b.Children(func() {
			ui.Text(c, label).FontSize(px(t, fsCaption)).TextColor(k.Text2)
			ic := icons.AltArrowRight
			if open {
				ic = icons.AltArrowDown
			}
			ui.Icon(c, ic.Linear).Size(px(t, 12), px(t, 12)).TextColor(k.Text2)
		})
		if b.Clicked() {
			a.modelsDetail[e.ID] = !open
		}
	})
	if a.modelsDetail[e.ID] {
		a.modelDetails(c, e)
	}
}

// modelDetails is the technical identity of a model, under its card's
// Details: what the app, the API and Hugging Face call it.
func (a *app) modelDetails(c *ui.Context, e catalog.Entry) {
	t := c.Theme()
	lic := e.Licence.Name
	if !e.Licence.Commercial {
		lic += " · " + i18n.T("non-commercial use only")
	}
	rows := [][2]string{
		{i18n.T("Technical name"), e.Label},
		{i18n.T("Family"), e.Family},
		{i18n.T("Model ID"), e.ID},
	}
	if e.Quantization != "" {
		rows = append(rows, [2]string{i18n.T("Quantisation"), e.Quantization})
	}
	rows = append(rows, [2]string{i18n.T("Download size"), catalog.HumanSize(e.Size)}, [2]string{i18n.T("Repository"), e.Repo})
	if e.Revision != "" {
		rows = append(rows, [2]string{i18n.T("Revision"), e.Revision[:min(len(e.Revision), 12)]})
	}
	rows = append(rows, [2]string{i18n.T("Licence"), lic})
	if e.Summary != "" && e.About != "" {
		rows = append(rows, [2]string{i18n.X("model detail", "About"), i18n.T(e.About)})
	}
	fillCard(c.Key("details-" + e.ID)).Padding(px(t, sp3)).Gap(px(t, sp2)).Children(func() {
		infoGrid(c, rows)
		if e.Licence.URL != "" {
			ui.Link(c, i18n.T("Model card"), e.Licence.URL).FontSize(px(t, fsCaption))
		}
	})
}

// modelActions are the buttons of an entry for its state: Download (or
// Resume), Cancel while it downloads, and the ⋯ menu (Verify, Show in
// Finder, Delete) once there are files.
func (a *app) modelActions(c *ui.Context, e catalog.Entry, m *modelState, loaded bool) {
	st := m.local.State
	switch {
	case m.downloading:
		if iconButton(c.Key("cancel"), icons.CloseCircle, i18n.T("Cancel")).Tooltip(i18n.T("Cancel the download")).Clicked() {
			a.cancelDownload(e)
		}
	case st == hub.Partial:
		if secondaryIconButton(c.Key("download"), icons.DownloadMinimalistic, i18n.T("Resume")).Label(i18n.T("Resume")).Clicked() {
			a.download(e)
		}
	case st != hub.Downloaded:
		if secondaryIconButton(c.Key("download"), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download")).Disabled(!m.known).Clicked() {
			a.download(e)
		}
	case a.installedBy(e, m) != "":
		// Another app installed it: AudioSmith claims it to use it.
		if secondaryButton(c.Key("use"), i18n.T("Use")).Tooltip(i18n.T("Use it in AudioSmith too: nothing to download")).Clicked() {
			a.useModel(e)
		}
	}
	if (st == hub.Downloaded || st == hub.Partial) && !m.downloading {
		snapshot := m.local.Snapshot
		moreMenu(c.Key("model-more"), i18n.T("More actions for %s", modelRef(e)), func(mm *ui.Menu) {
			if st == hub.Downloaded {
				if mm.Item(i18n.T("Verify")).Disabled(m.verifying).Chosen() {
					a.verifyModel(e)
				}
				if mm.Item(i18n.T("Show in Finder")).Chosen() {
					mygo.Shell.ShowItemInFolder(snapshot)
				}
				mm.Separator()
			}
			label := i18n.T("Delete")
			if loaded {
				label = i18n.T("Delete (unload it first)")
			}
			if mm.Item(label).Disabled(loaded).Chosen() {
				a.askDelete(e)
			}
		})
	}
}

// modelStateText says what of an entry is on disk.
func (a *app) modelStateText(e catalog.Entry, m *modelState) string {
	switch {
	case m.downloading && m.progress.Waiting:
		return waitingText(m.progress) // models_family.go
	case m.downloading:
		if m.progress.TotalBytes > 0 {
			return i18n.T("Downloading · %d%%", int(100*m.progress.Bytes/m.progress.TotalBytes))
		}
		return i18n.T("Starting the download…")
	case m.verifying:
		return i18n.T("Checking…")
	case !m.known:
		return i18n.T("Checking…")
	case m.local.State == hub.Downloaded:
		if by := a.installedBy(e, m); by != "" {
			return i18n.T("Installed · from %s", by) // family_reuse.go
		}
		return i18n.T("Downloaded · %s", mb(m.local.Bytes))
	case m.local.State == hub.Partial:
		return i18n.T("Incomplete · %s on disk", mb(m.local.Bytes))
	}
	if name := a.famDeleted[strings.ToLower(e.Repo)]; name != "" {
		return i18n.T("Deleted in %s · Download again", name) // family_app.go
	}
	return i18n.T("Not downloaded")
}

// mb formats bytes as the Models page shows them.
func mb(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.0f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}

// modelsAlerts asks before deleting a model. A model another installed
// ActNow app uses gets the family's dialog (models_family.go); the files
// of one only a dev build uses stay too, but the dialog is the plain one.
func (a *app) modelsAlerts(c *ui.Context) {
	e := a.deleting
	if len(a.deleteShared) > 0 {
		a.removeSharedDialog(c, e) // models_family.go
		return
	}
	switch ui.AlertDialog(c, &a.deleteAsk, i18n.T("Delete %s?", modelRef(e)),
		i18n.T("Its files leave the model folder. You can download it again."), i18n.T("Cancel"), i18n.T("Delete")) {
	case 1:
		a.deleteModel(e, models.ThisApp)
	}
}
