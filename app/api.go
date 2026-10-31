package main

// The Local API (M5, thread #20): the HTTP server of internal/api with
// the MCP server of internal/mcp mounted at /mcp, run inside the app
// while it runs (Settings → Local API turns it off or moves its port).
// It listens on 127.0.0.1; LAN sharing opens a second port with a PIN.
// The port file <data>/api.json tells the MCP stdio shim (api_stdio.go)
// and scripts where it is.
//
// Files: api.go (state, lifetime, actions), api_backend.go (api.Backend
// over the library), api_settings.go (Settings → Local API),
// api_stdio.go (AudioSmith --mcp-stdio), integrations*.go, tools*.go.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/hehehai/AudioSmith/internal/api"
	"github.com/hehehai/AudioSmith/internal/hub"
	"github.com/hehehai/AudioSmith/internal/i18n"
	mcpsrv "github.com/hehehai/AudioSmith/internal/mcp"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// apiKeyStore keeps the API key: the Keychain item "api.key"; tests
// replace it.
var apiKeyStore interface {
	Get() (string, error)
	Set(string) error
	Delete() error
} = hub.Secret("api.key", "AudioSmith API key")

// apiState is the Local API's state, on the main thread.
type apiState struct {
	apiSrv     *api.Server
	apiMCP     *mcpsrv.Server
	apiBackend *apiBackend
	apiData    string // the data folder (port file, MCP sandbox)

	apiPort    int    // the loopback port listened on; 0 = not running
	apiErr     string // why it is not running (translated)
	apiStarted bool   // startAPI ran
	apiHasKey  bool   // a key is in the Keychain
	// apiNewKey is a key just generated, shown (and copyable) until the
	// page is left.
	apiNewKey string
	apiShare  *api.Share // LAN sharing while on
	apiShareE string

	apiForm     apiForm
	apiClients  []mcpsrv.Client // agents seen, for the bindings
	apiDocs     *mygo.WebView   // the reference viewer, while its page shows
	apiDocsErr  string
	integManual bool // thread #33: an agent page's Manual setup is open
	skillAsk    bool // thread #34: the Install skill dialog is open
}

// apiForm is what the settings page's controls bind to.
type apiForm struct {
	port      float64
	sharePort float64
	origins   string
	output    int // 0 both, 1 inline, 2 file
	mcpDir    string
	newClient string
	newVoice  string
	newLabel  string
	ops       bool // the endpoint list is open
	keyShown  bool
	keyIncl   bool // integrations: put the key in snippets
}

// apiPortFile is the port file's path.
func (a *app) apiPortFile() string { return filepath.Join(a.apiData, api.PortFileName) }

// startAPI makes the API and MCP servers and starts listening if the
// settings say so. main calls it once the engine is connected; tests too.
func (a *app) startAPI(dataDir string) {
	if a.apiStarted {
		return
	}
	a.apiStarted = true
	a.apiData = dataDir
	b := a.newAPIBackend(dataDir)
	b.cleanTemp()
	srv := api.New(api.Options{Backend: b, Sched: a.lib.sched, Bus: a.lib.bus, Version: a.version,
		Seen: func(time.Time) { a.post(func() {}) }}) // Developers shows it (apiLastRequest)
	m := mcpsrv.New(mcpsrv.Options{API: srv, Backend: b, Version: a.version})
	srv.MountMCP(m.Handler())
	a.apiSrv, a.apiMCP, a.apiBackend = srv, m, b
	a.syncAPIForm()
	a.async(func() {
		key, _ := apiKeyStore.Get()
		srv.SetKey(key)
		a.post(func() { a.apiHasKey = key != "" })
	})
	a.applyAPI()
}

// apiLastRequest is when a client's request last came (zero: none yet).
func (a *app) apiLastRequest() time.Time {
	if a.apiSrv == nil {
		return time.Time{}
	}
	return a.apiSrv.LastRequest()
}

// applyAPI starts, moves or stops the loopback listener to match the
// settings.
func (a *app) applyAPI() {
	srv := a.apiSrv
	if srv == nil {
		return
	}
	cfg := a.prefs.API
	port := cfg.LoopbackPort()
	if !cfg.Enabled {
		if a.apiShare != nil {
			a.stopSharing()
		}
		a.apiPort, a.apiErr = 0, ""
		pf := a.apiPortFile()
		a.async(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			srv.Stop(ctx)
			os.Remove(pf)
		})
		return
	}
	if a.apiPort == port && a.apiErr == "" {
		return
	}
	pf, version := a.apiPortFile(), a.version
	a.async(func() {
		err := srv.Start(port)
		if err == nil {
			err = api.WritePortFile(pf, api.PortFile{URL: "http://127.0.0.1:" + strconv.Itoa(port), Port: port,
				PID: os.Getpid(), Version: version, Started: time.Now()})
		}
		a.post(func() {
			if a.prefs.API.LoopbackPort() != port || !a.prefs.API.Enabled {
				return // changed again meanwhile
			}
			switch {
			case errors.Is(err, api.ErrPortInUse):
				a.apiPort, a.apiErr = 0, i18n.T("Port %d is used by another program: choose another one.", port)
			case err != nil:
				a.apiPort, a.apiErr = 0, i18n.T("The Local API could not start: %v", err)
			default:
				a.apiPort, a.apiErr = port, ""
			}
		})
	})
}

// stopAPI closes the servers and removes the port file, waiting up to
// 2 s for the requests in progress (tests; the app uses apiStopper off
// the main thread).
func (a *app) stopAPI() {
	if stop := a.apiStopper(); stop != nil {
		stop()
	}
}

// apiStopper returns what stopAPI does, to run on any goroutine; nil
// when no server was made. Main thread.
func (a *app) apiStopper() func() {
	srv := a.apiSrv
	if srv == nil {
		return nil
	}
	portFile := a.apiPortFile()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Close(ctx)
		os.Remove(portFile)
	}
}

// apiBaseURL is the loopback URL clients use (the configured port, even
// while it is not running).
func (a *app) apiBaseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(a.prefs.API.LoopbackPort())
}

// generateAPIKey makes a new key (the old one stops working) and shows it.
func (a *app) generateAPIKey() {
	key := api.NewKey()
	srv := a.apiSrv
	a.async(func() {
		err := apiKeyStore.Set(key)
		a.post(func() {
			if err != nil {
				a.toastWarn(i18n.T("Could not save the key in the Keychain: %v", err))
				return
			}
			if srv != nil {
				srv.SetKey(key)
			}
			a.apiHasKey, a.apiNewKey, a.apiForm.keyShown = true, key, true
			a.toast(i18n.T("New API key made. Copy it now: it is not shown again."))
		})
	})
}

// revokeAPIKey deletes the key; clients using it are refused, and "Require
// a key" is turned off (no key could satisfy it).
func (a *app) revokeAPIKey() {
	srv := a.apiSrv
	a.async(func() {
		err := apiKeyStore.Delete()
		a.post(func() {
			if err != nil && !errors.Is(err, hub.ErrNoKeychain) {
				a.toastWarn(i18n.T("Could not remove the key: %v", err))
				return
			}
			if srv != nil {
				srv.SetKey("")
			}
			a.apiHasKey, a.apiNewKey = false, ""
			if a.prefs.API.RequireKey {
				a.updateSettings(func(s *settings.Settings) { s.API.RequireKey = false })
			}
			a.toast(i18n.T("API key revoked"))
		})
	})
}

// startSharing opens the LAN listener with a new PIN.
func (a *app) startSharing() {
	srv := a.apiSrv
	if srv == nil {
		return
	}
	port := a.prefs.API.LANPort()
	a.apiShareE = ""
	a.async(func() {
		share, err := srv.StartLAN(port)
		a.post(func() {
			if err != nil {
				a.apiShare = nil
				if errors.Is(err, api.ErrPortInUse) {
					a.apiShareE = i18n.T("Port %d is used by another program: choose another one.", port)
				} else {
					a.apiShareE = i18n.T("Sharing could not start: %v", err)
				}
				return
			}
			a.apiShare = &share
		})
	})
}

// stopSharing closes the LAN listener; the PIN stops working.
func (a *app) stopSharing() {
	srv := a.apiSrv
	a.apiShare = nil
	if srv == nil {
		return
	}
	a.async(srv.StopLAN)
}

// refreshAPIClients reads the agents the MCP server has seen.
func (a *app) refreshAPIClients() {
	if a.apiMCP != nil {
		a.apiClients = a.apiMCP.Clients()
	}
}

// syncAPIForm sets the form from the settings.
func (a *app) syncAPIForm() {
	cfg := a.prefs.API
	f := &a.apiForm
	f.port, f.sharePort = float64(cfg.LoopbackPort()), float64(cfg.LANPort())
	f.origins = joinLines(cfg.Origins)
	f.mcpDir = cfg.MCPDir
	switch cfg.Output() {
	case settings.MCPOutputInline:
		f.output = 1
	case settings.MCPOutputFile:
		f.output = 2
	default:
		f.output = 0
	}
}

// apiSandbox is where MCP tools write audio.
func (a *app) apiSandbox() string {
	if a.apiMCP != nil {
		return a.apiMCP.Sandbox()
	}
	if d := a.prefs.API.MCPDir; d != "" {
		return d
	}
	return filepath.Join(a.apiData, "mcp")
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

func joinLines(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "\n"
		}
		out += x
	}
	return out
}
