package main

// Settings → Models & network: Windows Runtimes section.
// Shown only on Windows (never on macOS or Linux) so users can inspect,
// download, or remove lightweight native runtimes (~12–55 MB each) and see
// whether the optional Python worker for Qwen3-TTS / Irodori-TTS is present.

import (
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/winruntime"
)

func (a *app) windowsRuntimesCard(c *ui.Context) {
	if a.platformOS() != "windows" {
		return
	}
	if !a.winRT.known {
		a.refreshWinRuntimes()
	}
	t := c.Theme()
	k := tok(t)
	mgr := a.winRuntimeManager()

	fillCard(c.Key("win-runtimes-card")).Padding(px(t, sp3), px(t, sp4)).Gap(px(t, sp2)).Children(func() {
		ui.Column(c).Gap(px(t, sp1)).Children(func() {
			ui.Text(c, i18n.T("Windows runtimes")).FontWeight(600)
			caption(c, i18n.T("Lightweight native engines (~12–55 MB each), downloaded automatically when needed.")).TextColor(k.Text2)
		})

		for _, name := range winruntime.OrderedNames() {
			a.winRuntimeRow(c, mgr, name)
		}

		// Optional Python worker status row (never auto-downloaded).
		ui.Row(c.Key("winrt-row-python")).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				ui.Text(c, i18n.T("Python worker (optional)")).FontSize(px(t, fsCaption)).FontWeight(500)
				if a.winRT.pythonPath != "" {
					ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 13), px(t, 13)).TextColor(t.Success)
						caption(c, i18n.T("Installed · used by Qwen3-TTS and Irodori-TTS"))
					})
				} else {
					caption(c, i18n.T("Optional · only needed for Qwen3-TTS and Irodori-TTS")).TextColor(k.Text3)
				}
			})
		})
	})
}

func (a *app) winRuntimeRow(c *ui.Context, mgr *winruntime.Manager, name winruntime.Name) {
	t := c.Theme()
	k := tok(t)
	spec, _ := mgr.Spec(name)
	asset, hasAsset := mgr.Asset(name)
	st := a.winRT.statuses[name]
	dl := a.winRT.downloading[name]
	prog := a.winRT.progress[name]
	errMsg := a.winRT.errors[name]

	ui.Column(c.Key("winrt-row-" + string(name))).Gap(px(t, sp1)).Children(func() {
		ui.Row(c).Gap(px(t, sp3)).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(px(t, 2)).Children(func() {
				title := spec.DisplayName
				if title == "" {
					title = string(name)
				}
				ui.Text(c, title).FontSize(px(t, fsCaption)).FontWeight(500)
				ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Children(func() {
					if !dl && st.Installed {
						ui.Icon(c, icons.CheckCircle.Linear).Size(px(t, 13), px(t, 13)).TextColor(t.Success)
					}
					caption(c, winRuntimeStatusText(st, dl, prog, hasAsset, asset.Size, spec.Version, mgr.Arch())).TextColor(k.Text2)
				})
			})
			ui.Row(c).Gap(px(t, sp1)).AlignItems(ui.Center).Shrink(0).Children(func() {
				switch {
				case dl:
					if iconButton(c.Key("winrt-cancel-"+string(name)), icons.CloseCircle, i18n.T("Cancel")).Clicked() {
						a.cancelWinRuntime(name)
					}
				case st.Installed:
					if ui.Button(c.Key("winrt-remove-"+string(name)), i18n.T("Remove")).Label(i18n.T("Remove %s", spec.DisplayName)).Clicked() {
						a.removeWinRuntime(name)
					}
				case hasAsset:
					if secondaryIconButton(c.Key("winrt-dl-"+string(name)), icons.DownloadMinimalistic, i18n.T("Download")).Label(i18n.T("Download %s", spec.DisplayName)).Clicked() {
						a.downloadWinRuntime(name, nil)
					}
				}
			})
		})
		if dl {
			frac := -1.0
			if prog.Stage == "downloading" && prog.TotalBytes > 0 {
				frac = float64(prog.Bytes) / float64(prog.TotalBytes)
			}
			ui.Progress(c, frac).Label(i18n.T("Downloading %s", spec.DisplayName))
		}
		if errMsg != "" && !dl {
			ui.Text(c, errMsg).FontSize(px(t, fsMicro)).TextColor(t.Danger)
		}
	})
}

func winRuntimeStatusText(st winruntime.Status, dl bool, p winruntime.Progress, hasAsset bool, assetSize int64, specVer, arch string) string {
	if dl {
		switch p.Stage {
		case "verifying":
			return i18n.T("Verifying…")
		case "extracting":
			return i18n.T("Extracting…")
		default:
			if p.TotalBytes > 0 {
				return i18n.T("Downloading · %d%%", int(100*p.Bytes/p.TotalBytes))
			}
			return i18n.T("Starting the download…")
		}
	}
	if st.Installed {
		ver := st.Version
		if ver == "" {
			ver = specVer
		}
		if ver != "" {
			return i18n.T("Installed · v%s", ver)
		}
		return i18n.T("Installed")
	}
	if hasAsset && assetSize > 0 {
		return i18n.T("Not installed · ≈ %s", mb(assetSize))
	}
	if !hasAsset {
		return i18n.T("Not available for %s", arch)
	}
	return i18n.T("Not installed")
}
