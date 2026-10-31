package main

// Settings → Local API (M5): the server (on/off, port, base URL), access
// (API key, "Require the key", browser origins), LAN sharing (PIN, URLs),
// agents (MCP output, folder, per-agent voices) and the API reference
// (the endpoints, and the bundled viewer in a page of its own).

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// monoFont is the font of code and URLs.
const monoFont = "ui-monospace, SFMono-Regular, Menlo, monospace"

func (a *app) apiSettings(c *ui.Context) {
	cfg := a.prefs.API

	// The server.
	settingGroup(c, i18n.T("Server"), func() {
		on := cfg.Enabled
		settingRow(c, "api-on", i18n.T("Run the Local API"), i18n.T("OpenAI-compatible speech and transcription, AudioSmith's own API and an MCP server for agents, on this Mac only."), func() {
			if inkSwitch(c.Key("api-on-switch"), &on).Label(i18n.T("Run the Local API")).Changed() {
				a.updateSettings(func(s *settings.Settings) { s.API.Enabled = on })
				a.applyAPI()
			}
		})
		settingRow(c, "api-port", i18n.T("Port"), i18n.T("Clients connect to 127.0.0.1 at this port. Changing it breaks the configurations you gave out."), func() {
			numberInput(c.Key("api-port-input"), &a.apiForm.port, 1024, 65535, 1, numOpts{Label: i18n.T("Port")})
			p := int(a.apiForm.port)
			if secondaryButton(c.Key("api-port-apply"), i18n.T("Apply")).Disabled(p == cfg.LoopbackPort() || p < 1024).Clicked() {
				a.updateSettings(func(s *settings.Settings) { s.API.Port = p })
				a.applyAPI()
			}
		})
		settingBlock(c, "api-status", func() { a.apiStatusRow(c) })
	})
	a.apiAccessSettings(c)
}

// apiStatusRow is the server's state and Copy base URL.
func (a *app) apiStatusRow(c *ui.Context) {
	t := c.Theme()
	cfg := a.prefs.API
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		switch {
		case !cfg.Enabled:
			ui.Text(c, i18n.T("Off")).TextColor(t.TextMuted).Grow(1)
		case a.apiErr != "":
			ui.Text(c, a.apiErr).TextColor(t.Danger).Wrap().Grow(1)
		case a.apiPort == 0:
			ui.Text(c, i18n.T("Starting…")).TextColor(t.TextMuted).Grow(1)
		default:
			ui.Badge(c, i18n.T("Running"))
			ui.Text(c, a.apiBaseURL()).Font(monoFont).Grow(1).SingleLine()
		}
		if secondaryButton(c.Key("api-copy-url"), i18n.T("Copy base URL")).Tooltip(i18n.T("The OpenAI base URL: %s", a.apiBaseURL()+"/v1")).Clicked() {
			c.Services().WriteClipboard(a.apiBaseURL() + "/v1")
			a.toast(i18n.T("Copied"))
		}
	})
}

// apiAccessSettings are the Local API's key, origins, sharing, agents
// and reference.
func (a *app) apiAccessSettings(c *ui.Context) {
	t := c.Theme()
	cfg := a.prefs.API
	settingGroup(c, i18n.T("Access"), func() {
		keyDesc := i18n.T("No key. Programs on this Mac need none; send one to require it, or to reach AudioSmith over the network.")
		if a.apiHasKey {
			keyDesc = i18n.T("A key is saved in the Keychain. OpenAI SDKs send it as their api_key.")
		}
		settingRowNote(c, "api-key", i18n.T("API key"), keyDesc, func() {
			label := i18n.T("Generate")
			if a.apiHasKey {
				label = i18n.T("Replace")
			}
			if secondaryButton(c.Key("api-key-gen"), label).Clicked() {
				a.generateAPIKey()
			}
			if secondaryButton(c.Key("api-key-revoke"), i18n.T("Revoke")).Disabled(!a.apiHasKey).Clicked() {
				a.revokeAPIKey()
			}
		})
		if a.apiNewKey != "" {
			settingFoot(c, "api-new-key-foot", func() { a.apiNewKeyRow(c) })
		}
		require := cfg.RequireKey
		settingRow(c, "api-require", i18n.T("Require the key on this Mac too"), i18n.T("Every client must send the key, also programs on this Mac. Health and the API reference stay open."), func() {
			if inkSwitch(c.Key("api-require-switch"), &require).Label(i18n.T("Require the key on this Mac too")).Disabled(!a.apiHasKey && !require).Changed() {
				a.updateSettings(func(s *settings.Settings) { s.API.RequireKey = require })
			}
		})
		settingRow(c, "api-origins-head", i18n.T("Browser origins allowed"), i18n.T("One per line, such as http://localhost:5173. Web pages from other origins cannot call the API."), func() {})
		settingFoot(c, "api-origins-foot", func() {
			ui.TextArea(c.Key("api-origins"), &a.apiForm.origins).Height(px(t, 52)).Font(monoFont).Label(i18n.T("Browser origins allowed"))
			ui.Row(c.Key("api-origins-row")).Justify(ui.End).Children(func() {
				origins := splitLines(a.apiForm.origins)
				if secondaryButton(c.Key("api-origins-save"), i18n.T("Save")).Disabled(slices.Equal(origins, cfg.Origins)).Clicked() {
					a.updateSettings(func(s *settings.Settings) { s.API.Origins = origins })
				}
			})
		})

		// LAN sharing.
	})
	settingGroup(c, i18n.T("Sharing on the local network"), func() {
		sharing := a.apiShare != nil
		settingRow(c, "api-share", i18n.T("Share with other devices"), i18n.T("Opens port %d on your network. Devices need the PIN below for speech, transcription and voices; the key for everything else. Until you turn it off or quit.", cfg.LANPort()), func() {
			if inkSwitch(c.Key("api-share-switch"), &sharing).Label(i18n.T("Share with other devices")).Disabled(a.apiPort == 0 && !sharing).Changed() {
				if sharing {
					a.startSharing()
				} else {
					a.stopSharing()
				}
			}
		})
		if a.apiShareE != "" {
			settingFoot(c, "api-share-err", func() { ui.Text(c, a.apiShareE).TextColor(t.Danger).Wrap() })
		}
		if sh := a.apiShare; sh != nil {
			settingFoot(c, "api-share-foot", func() { a.apiShareInfo(c, sh) })
		}
	})
	a.apiAgentSettings(c)
}

// apiShareInfo is LAN sharing's PIN and addresses.
func (a *app) apiShareInfo(c *ui.Context, sh *api.Share) {
	t := c.Theme()
	muted := func(s string) { ui.Text(c, s).FontSize(px(t, 12)).TextColor(t.TextMuted).Wrap() }
	ui.Column(c.Key("api-share-info")).Gap(px(t, 6)).Padding(px(t, 12)).Radius(px(t, radiusControl)).Background(tok(t).Subtle).Children(func() {
		ui.Row(c).Gap(px(t, 12)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("PIN")).TextColor(t.TextMuted)
			ui.Text(c, sh.PIN[:3]+" "+sh.PIN[3:]).Font(monoFont).FontSize(px(t, 22)).Bold()
		})
		if len(sh.URLs) == 0 {
			muted(i18n.T("This Mac has no address on a local network."))
		}
		for i, u := range sh.URLs {
			ui.Row(c.Key("share-url-" + strconv.Itoa(i))).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, u+"/v1").Font(monoFont).Grow(1).SingleLine()
				if secondaryButton(c.Key("copy"), i18n.T("Copy")).Clicked() {
					c.Services().WriteClipboard(u + "/v1")
					a.toast(i18n.T("Copied"))
				}
			})
		}
		muted(i18n.T("Send the PIN as the X-AudioSmith-Pin header, or add ?pin=%s to the URL. Anyone on this network with the PIN can make speech with your voices.", sh.PIN))
	})
}

// apiAgentSettings are the agents' (MCP) settings and the API reference.
func (a *app) apiAgentSettings(c *ui.Context) {
	settingGroup(c, i18n.T("Agents (MCP)"), func() {
		settingRow(c, "api-mcp-output", i18n.T("Audio for agents"), i18n.T("How generate_speech hands back audio: in the answer, as a WAV file in the folder below, or both."), func() {
			if segmented(c.Key("api-output-seg"), &a.apiForm.output, i18n.T("Both"), i18n.T("Inline"), i18n.T("File")).Label(i18n.T("Audio for agents")).Changed() {
				mode := []string{settings.MCPOutputBoth, settings.MCPOutputInline, settings.MCPOutputFile}[a.apiForm.output]
				a.updateSettings(func(s *settings.Settings) { s.API.MCPOutput = mode })
			}
		})
		settingRowNote(c, "api-mcp-dir", i18n.T("Folder for agents' audio"), a.apiSandbox(), func() {
			if secondaryButton(c.Key("api-dir-choose"), i18n.T("Choose…")).Clicked() {
				a.chooseAPIDir()
			}
			if secondaryButton(c.Key("api-dir-reveal"), i18n.T("Show in Finder")).Clicked() {
				a.revealAPIDir()
			}
		})
		a.apiBindings(c)

		// Reference.
	})
	settingGroup(c, i18n.T("API reference"), func() {
		settingRowNote(c, "api-ref", i18n.T("OpenAPI description"), a.apiBaseURL()+"/openapi.json", func() {
			if secondaryButton(c.Key("api-ref-open"), i18n.T("Open the reference")).Disabled(a.apiPort == 0).Clicked() {
				a.navigate("/api-reference")
			}
			if secondaryButton(c.Key("api-ref-browser"), i18n.T("Open in the browser")).Disabled(a.apiPort == 0).Clicked() {
				go mygo.Shell.OpenExternal(a.apiBaseURL() + "/docs")
			}
		})
		settingBlock(c, "api-ops-block", func() { a.apiOperations(c) })
	})
}

// apiOperations is the folded list of the API's endpoints.
func (a *app) apiOperations(c *ui.Context) {
	t := c.Theme()
	ui.Collapsible(c.Key("api-ops"), i18n.T("Endpoints"), &a.apiForm.ops, func() {
		ui.Column(c).Gap(px(t, 2)).Children(func() {
			tag := ""
			for i, op := range api.Operations() {
				if op.Tag != tag {
					tag = op.Tag
					ui.Text(c.Key("tag-"+tag), tag).Bold().FontSize(px(t, 12)).TextColor(t.TextMuted).Padding(px(t, 6), 0, px(t, 2), 0)
				}
				ui.Row(c.Key("op-" + strconv.Itoa(i))).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, op.Method).Font(monoFont).FontSize(px(t, 11)).Bold().Width(px(t, 52)).TextColor(methodColor(t, op.Method))
					ui.Text(c, op.Path).Font(monoFont).FontSize(px(t, 12)).Width(px(t, 230)).SingleLine()
					ui.Text(c, op.Summary).FontSize(px(t, 12)).TextColor(t.TextMuted).Grow(1).SingleLine()
				})
			}
		})
	})
}

// methodColor tints an HTTP method.
func methodColor(t *ui.Theme, m string) ui.Color {
	switch m {
	case "GET":
		return t.Success
	case "POST":
		return t.Accent
	case "DELETE":
		return t.Danger
	}
	return t.Warning
}

// apiBindings is the per-agent voices list.
func (a *app) apiBindings(c *ui.Context) {
	labels, ids := a.apiVoiceOptions()
	settingRow(c, "api-bindings", i18n.T("Each agent's voice"), i18n.T("Agents are told apart by the X-AudioSmith-Client header (Integrations' setups send it), else by their MCP name."), func() {})
	settingFoot(c, "api-bindings-foot", func() { a.apiBindingList(c, labels, ids) })
}

// apiBindingList is the agents' voices, the row adding one, and the
// agents seen.
func (a *app) apiBindingList(c *ui.Context, labels, ids []string) {
	t := c.Theme()
	cfg := a.prefs.API
	for i, b := range cfg.Bindings {
		ui.Row(c.Key("binding-" + b.Client)).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, b.Client).Font(monoFont).Width(px(t, 160)).SingleLine()
			name := labels[0]
			if j := slices.Index(ids, b.Voice); j >= 0 {
				name = labels[j]
			}
			ui.Text(c, name).Grow(1).SingleLine()
			if secondaryButton(c.Key("remove"), i18n.T("Remove")).Clicked() {
				idx := i
				a.updateSettings(func(s *settings.Settings) {
					if idx < len(s.API.Bindings) {
						s.API.Bindings = slices.Delete(slices.Clone(s.API.Bindings), idx, idx+1)
					}
				})
			}
		})
	}
	f := &a.apiForm
	var seen []string
	for _, cl := range a.apiClients {
		if _, bound := cfg.Binding(cl.ID); !bound {
			seen = append(seen, cl.ID)
		}
	}
	for _, s := range []string{"claude-code", "cursor", "codex-cli"} {
		if _, bound := cfg.Binding(s); !bound && !slices.Contains(seen, s) {
			seen = append(seen, s)
		}
	}
	ui.Row(c.Key("binding-new")).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		ui.Combobox(c.Key("binding-client"), &f.newClient, seen).Width(px(t, 160)).Label(i18n.T("Agent"))
		if f.newVoice == "" {
			f.newVoice = labels[0]
		}
		ui.Select(c.Key("binding-voice"), &f.newVoice, labels).Grow(1).Label(i18n.T("Voice"))
		client := strings.TrimSpace(f.newClient)
		_, exists := cfg.Binding(client)
		if secondaryButton(c.Key("binding-add"), i18n.T("Add")).Disabled(client == "" || exists).Clicked() {
			voice := ""
			if j := slices.Index(labels, f.newVoice); j >= 0 {
				voice = ids[j]
			}
			a.updateSettings(func(s *settings.Settings) {
				s.API.Bindings = append(slices.Clone(s.API.Bindings), settings.APIBinding{Client: client, Voice: voice})
			})
			f.newClient = ""
		}
	})
	if len(a.apiClients) > 0 {
		var parts []string
		for _, cl := range a.apiClients {
			parts = append(parts, i18n.T("%s (%s, %d calls)", cl.ID, i18n.DateTime(cl.LastSeen), cl.Calls))
		}
		ui.Text(c, i18n.T("Agents seen since AudioSmith started: %s", strings.Join(parts, " · "))).FontSize(px(t, 12)).TextColor(t.TextMuted).Wrap()
	}
}

// apiVoiceOptions are the voices a binding may name: the default, then
// the saved voices.
func (a *app) apiVoiceOptions() (labels, ids []string) {
	labels, ids = []string{i18n.T("The default voice")}, []string{""}
	for _, v := range a.voices {
		labels, ids = append(labels, v.Name), append(ids, v.ID)
	}
	return labels, ids
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, strings.TrimRight(l, "/"))
		}
	}
	return out
}

// chooseAPIDir picks the agents' folder.
func (a *app) chooseAPIDir() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: i18n.T("Folder for agents' audio"), Directory: true, CreateDirectories: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			dir := paths[0]
			a.updateSettings(func(s *settings.Settings) { s.API.MCPDir = dir })
		})
	}()
}

// revealAPIDir shows the agents' folder (made if needed).
func (a *app) revealAPIDir() {
	dir := a.apiSandbox()
	a.async(func() {
		mkdirAll(dir)
		mygo.Shell.OpenPath(dir)
	})
}

// apiNewKeyRow is a key just made: shown once, to copy.
func (a *app) apiNewKeyRow(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("api-new-key")).Gap(px(t, 8)).AlignItems(ui.Center).Padding(px(t, 8)).Radius(px(t, radiusControl)).Background(tok(t).Subtle).Children(func() {
		ui.Text(c, a.apiNewKey).Font(monoFont).Grow(1).SingleLine()
		if secondaryButton(c.Key("api-key-copy"), i18n.T("Copy")).Clicked() {
			c.Services().WriteClipboard(a.apiNewKey)
			a.toast(i18n.T("Copied"))
		}
		if secondaryButton(c.Key("api-key-hide"), i18n.T("Done")).Clicked() {
			a.apiNewKey = ""
		}
	})
	caption(c, i18n.T("Copy it now: AudioSmith keeps it only in the Keychain and does not show it again."))
}
