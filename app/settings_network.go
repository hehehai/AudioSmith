package main

// Settings → Network (M7a): where models download from and the proxy
// every download uses (moved here from Models and downloads), and a
// connection test through that proxy.

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/icons"
	"github.com/hehehai/AudioSmith/internal/settings"
	famset "github.com/hehehai/actnowkit/settings"
)

// netCheck is the result of reaching one host.
type netCheck struct {
	host    string
	ok      bool
	latency time.Duration
	err     string
}

// networkState is the Network page's state.
type networkState struct {
	netChecks  []netCheck
	netTesting bool
}

// netHosts are the hosts AudioSmith downloads from: Hugging Face and its
// mirror (models), GitHub and the ActNow mirror (ffmpeg, yt-dlp).
var netHosts = []string{"https://huggingface.co", "https://hf-mirror.com", "https://github.com", "https://mirror.actnow.dev"}

// testNetwork reaches each host through the downloads' proxy.
func (a *app) testNetwork(then func([]netCheck)) {
	a.netTesting = true
	client := hub.NewHTTPClient(a.prefs.Models.Proxy, 10*time.Second)
	hosts := slices.Clone(netHosts)
	if eps := a.prefs.Models.Endpoints; len(eps) > 0 {
		for _, e := range eps {
			if !slices.Contains(hosts, e) {
				hosts = append(hosts, e)
			}
		}
	}
	a.background(func() {
		out := make([]netCheck, len(hosts))
		var wg sync.WaitGroup
		for i, h := range hosts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out[i] = reach(client, h)
			}()
		}
		wg.Wait()
		a.post(func() {
			a.netChecks, a.netTesting = out, false
			if then != nil {
				then(out)
			}
		})
	})
}

// reach asks host for its headers (a HEAD request, 10 s at most).
var reach = func(client *http.Client, host string) netCheck {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, host, nil)
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i > 0 {
			msg = msg[i+2:]
		}
		return netCheck{host: host, err: msg}
	}
	resp.Body.Close()
	return netCheck{host: host, ok: resp.StatusCode < 500, latency: time.Since(start), err: resp.Status}
}

// networkSettings is Settings → Network.
func (a *app) networkSettings(c *ui.Context) {
	a.revealAnchor(c, "network")
	t := c.Theme()
	settingGroup(c, i18n.T("Downloads"), func() {
		settingNote(c, "net-intro", i18n.T("For models, ffmpeg and yt-dlp. The engine itself never goes online."))
		// The family's wording (actnowkit mirror.TextSourceNote), then the token.
		sourceNote := i18n.T("Models come from Hugging Face, ffmpeg and yt-dlp from GitHub. Every download is checked (signature or checksum), so a mirror cannot change what you get.") + " " +
			i18n.T("Mirrors never receive your token; gated models always come from huggingface.co.")
		settingRow(c, "source", i18n.T("Download from"), sourceNote, func() {
			opts := sourceOptions()
			if ui.Select(c.Key("source-select"), &a.source, opts).Label(i18n.T("Download from")).Width(px(t, 440)).Shrink(1).MinWidth(px(t, 200)).Changed() {
				var eps []string
				switch slices.Index(opts, a.source) {
				case 1:
					eps = []string{"https://huggingface.co"}
				case 2:
					eps = []string{"https://hf-mirror.com"}
				case 3:
					eps = strings.Fields(a.endpoint)
					if len(eps) == 0 {
						return // until a URL is typed
					}
				}
				a.updateSettings(func(s *settings.Settings) { s.Models.Endpoints = eps })
			}
		})
		if slices.Index(sourceOptions(), a.source) == 3 {
			settingRow(c, "endpoint", i18n.T("Endpoint"), i18n.T("One or more Hugging Face URLs, separated by spaces; the fastest answers. ffmpeg and yt-dlp come from the faster of github.com and mirror.actnow.dev."), func() {
				in := ui.TextInput(c.Key("endpoint-input"), &a.endpoint).Placeholder("https://hf-mirror.com").Width(px(t, 280)).Label(i18n.T("Endpoint"))
				if in.Submitted() {
					eps := strings.Fields(a.endpoint)
					a.updateSettings(func(s *settings.Settings) { s.Models.Endpoints = eps })
				}
			})
		}
		a.sharedScope(c, famset.KeyHFEndpoints) // family_shared_view.go: the ActNow family's
		settingRowNote(c, "proxy", i18n.T("Proxy"), a.proxyInfo, func() {
			opts := proxyOptions()
			if ui.Select(c.Key("proxy-select"), &a.proxyMode, opts).Label(i18n.T("Proxy")).Width(px(t, 240)).Changed() {
				mode := ""
				switch slices.Index(opts, a.proxyMode) {
				case 1:
					mode = hub.ProxyDirect
				case 2:
					mode = strings.TrimSpace(a.proxy)
					if mode == "" {
						return
					}
				}
				a.updateSettings(func(s *settings.Settings) { s.Models.Proxy = mode })
			}
		})
		if slices.Index(proxyOptions(), a.proxyMode) == 2 {
			settingRow(c, "proxy-url", i18n.T("Proxy URL"), i18n.T("http://, https:// or socks5://host:port"), func() {
				in := ui.TextInput(c.Key("proxy-input"), &a.proxy).Placeholder("http://127.0.0.1:7890").Width(px(t, 280)).Label(i18n.T("Proxy URL"))
				if in.Submitted() {
					mode := strings.TrimSpace(a.proxy)
					a.updateSettings(func(s *settings.Settings) { s.Models.Proxy = mode })
				}
			})
		}
		a.sharedScope(c, famset.KeyProxy)

	})
	settingGroup(c, i18n.T("Connection"), func() {
		settingRow(c, "net-test", i18n.T("Test the connection"), i18n.T("Reaches Hugging Face, GitHub and their mirrors through the proxy above."), func() {
			if a.netTesting {
				ui.Spinner(c)
			}
			if secondaryButton(c.Key("net-test"), i18n.T("Test")).Disabled(a.netTesting).Clicked() {
				a.testNetwork(nil)
			}
		})
		if len(a.netChecks) == 0 {
			return
		}
		settingFoot(c, "net-results", func() { a.netResults(c) })
	})
}

// netResults are the connection test's hosts, each with its time or error.
func (a *app) netResults(c *ui.Context) {
	t := c.Theme()
	for _, r := range a.netChecks {
		ui.Row(c.Key("net-" + r.host)).Gap(px(t, 8)).Children(func() {
			mark, col := icons.CheckCircle, t.Success
			detail := i18n.T("%d ms", r.latency.Milliseconds())
			if !r.ok {
				mark, col, detail = icons.CloseCircle, t.Danger, r.err
			}
			ui.Icon(c, mark.Linear).Size(px(t, 14), px(t, 14)).TextColor(col).Shrink(0)
			ui.Text(c, strings.TrimPrefix(r.host, "https://")).Width(px(t, 160))
			ui.Text(c, detail).TextColor(t.TextMuted).FontSize(px(t, 12)).Grow(1).MinWidth(0).SingleLine().Ellipsis("…")
		})
	}
}
