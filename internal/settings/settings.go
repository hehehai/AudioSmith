// Package settings is the user's preferences: one typed, versioned
// document, persisted as JSON by the store (settings table, key "app"),
// read by any goroutine, changed through Service.Update, announced with
// Changed on the core.Bus. Thread C implements Service in M0 and owns the
// schema; other modules add their section here (a field with a default in
// Defaults) through C or main.
//
// Not settings: secrets (HF token, API keys) live in the macOS Keychain;
// window geometry stays with MyGo (WindowOptions.StateKey); editor
// contents are drafts (core.DraftService).
//
// Imports: nothing internal (Changed satisfies core.Event structurally).
package settings

import (
	"context"
	"encoding/json"
	"time"
)

// Version is the schema version Defaults writes. Bump it when a field's
// meaning changes and migrate older documents in Migrate.
//
//   - 2 (#109): Models.AutoUnload defaults to 30 minutes; a version-1
//     document's 0 was the old default (the UI could not tell it from a
//     choice), so it becomes 30 once.
const Version = 2

// DefaultAutoUnload is the idle limit of a new install, in minutes
// (Models.AutoUnload).
const DefaultAutoUnload = 30

// Settings is the whole document. Zero values must be valid (they are what
// a field added in a newer version reads as in an older document, before
// Migrate fills it in).
type Settings struct {
	Version    int        `json:"version"`
	General    General    `json:"general"`
	Appearance Appearance `json:"appearance"`
	Studio     Studio     `json:"studio"`
	Models     Models     `json:"models"`
	Engine     Engine     `json:"engine"`
	Privacy    Privacy    `json:"privacy"`
	// M2 (thread #17, llm.go).
	LLM        LLM        `json:"llm"`
	Transcribe Transcribe `json:"transcribe"`
	Dictation  Dictation  `json:"dictation"`
	Dubbing    Dubbing    `json:"dubbing"` // M4 (thread #19, dubbing.go)
	// M5 (thread #20, api.go).
	API API `json:"api"`
	// M7a (thread #22, setup.go): the first-run setup and the media tools.
	Setup Setup `json:"setup"`
	Media Media `json:"media"`
	Calls Calls `json:"calls"` // M6b (thread #23, calls.go)
	// The Export audio dialog's last choices, per kind (export.go).
	Export Export `json:"export"`
	// Software updates (thread #52, updates.go of the app).
	Updates Updates `json:"updates"`
	// The ActNow family (family.go; internal/family).
	Family Family `json:"family,omitzero"`
}

// Updates: the in-app updater's preferences (updates.go). Up to
// 0.4.0-beta.5 the daily check and installing without asking were MyGo's
// updater plugin's, in its updater.json; the first launch after it carries
// them over (keepUpdaterChoices).
type Updates struct {
	// AutoCheck checks once a day; nil until chosen (or carried over):
	// on (AutoCheckOn).
	AutoCheck *bool `json:"autoCheck,omitempty"`
	// AutoInstall downloads and installs a version the daily check found
	// without asking; it runs after the next restart.
	AutoInstall bool `json:"autoInstall,omitempty"`
	// LastCheck is when a check last answered.
	LastCheck time.Time `json:"lastCheck,omitzero"`
	// Beta takes beta versions (prereleases such as 0.4.0-beta.1)
	// as well as releases. Nil until the user chooses: then a beta copy
	// takes betas and a release copy releases only (the app's
	// betaUpdates).
	Beta *bool `json:"beta,omitempty"`
}

// AutoCheckOn says whether updates are checked once a day (on unless the
// user turned it off).
func (u Updates) AutoCheckOn() bool { return u.AutoCheck == nil || *u.AutoCheck }

// General: language and app behaviour.
type General struct {
	// Language is the UI language as a BCP-47 tag ("en", "zh-Hans"), or ""
	// to follow the system (mygo.App.PreferredLanguages()); see i18n.Match.
	Language string `json:"language"`
	// CloseToTray keeps AudioSmith running in the menu bar when its
	// window closes, so dictation keeps working (M2).
	CloseToTray bool `json:"closeToTray,omitempty"`
	// OpenAtLogin… arrives later.
}

// Appearance: theme and scale.
type Appearance struct {
	Theme        string  `json:"theme"`        // "system" | "light" | "dark"
	Scale        float64 `json:"scale"`        // UI scale, 0.9–1.25 (1 = 100 %)
	ReduceMotion bool    `json:"reduceMotion"` // in addition to the system setting
	// M7a (setup.go): the sidebar as icons only, its width in DIPs at 100 %
	// (0 = the default) and the sidebar sections the user closed. (0.3
	// dropped M7a's accent palette: a stored "palette" is ignored, and
	// gone after the next save.)
	Rail           bool     `json:"rail,omitempty"`
	SidebarWidth   float64  `json:"sidebarWidth,omitempty"`
	ClosedSections []string `json:"closedSections,omitempty"`
	// Advanced lists the pages whose 「高级」 section is open ("studio",
	// "story", "audiobook", "dub", "export", "clone").
	Advanced []string `json:"advanced,omitempty"`
	// Seen lists what was shown once and should not show again (0.5
	// overhaul): an "introducing" dialog ("intro:dub"), a dismissed promo
	// card or corner tip ("tip:…").
	Seen []string `json:"seen,omitempty"`
}

// Studio: defaults of the TTS form.
type Studio struct {
	Model    string `json:"model"`    // catalog.Entry.ID last used ("" = the first)
	Autoplay bool   `json:"autoplay"` // play each take when it is ready
	// KeepTakes caps the history: beyond it the oldest takes that are not
	// starred (nor part of a project) are deleted. 0 = keep every take.
	KeepTakes int `json:"keepTakes,omitempty"`
	// Overrides are the user's changes to each model's recommended
	// settings (the catalogue's presets), by catalogue id: picking the
	// model again brings them back; "Restore recommended" clears them.
	Overrides map[string]ModelParams `json:"overrides,omitempty"`
}

// ModelParams are generation parameters changed from a model's
// recommendation; 0 = not changed.
type ModelParams struct {
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
	Steps       int     `json:"steps,omitempty"`
	Guidance    float64 `json:"guidance,omitempty"`
	Speed       float64 `json:"speed,omitempty"` // 0 = 1×
}

// Models: where and how models download.
type Models struct {
	CacheDir  string   `json:"cacheDir"`  // "" = hub.CacheDir()
	Endpoints []string `json:"endpoints"` // mirrors to race; nil = default
	// Proxy for downloads: "" = automatic (environment, then the macOS
	// system proxy), "direct", or a proxy URL (hub.Options.Proxy).
	Proxy string `json:"proxy,omitempty"`
	// AutoUnload is the minutes a model stays loaded unused before it is
	// unloaded, on every engine worker (dictation's included); 0 = never.
	AutoUnload int `json:"autoUnload"`
	// HFCredentialRef names the ActNow family's shared Hugging Face token
	// ("actnow:vendor.huggingface", family spec §8.4); "" = AudioSmith's
	// own Keychain item only.
	HFCredentialRef string `json:"hfCredentialRef,omitempty"`
	// MovingTo is the target of a move of the models that has not
	// finished (cancelled, skipped, interrupted): the models it moved are
	// there, so they are looked for there too (#101 M2). "" = none.
	MovingTo string `json:"movingTo,omitempty"`
}

// Engine: resource limits.
type Engine struct {
	// MemoryBudgetGB caps resident models across workers; 0 = automatic
	// (engine.Config.MemoryBudget).
	MemoryBudgetGB float64 `json:"memoryBudgetGB"`
}

// Privacy: what may leave the machine.
type Privacy struct {
	// AllowNetworkSearch enables web lookups that are not downloads the
	// user asked for (voice portraits, gallery updates). Off by default.
	AllowNetworkSearch bool `json:"allowNetworkSearch"`
	// LocalOnlyLLM keeps text on this Mac: only local language models
	// (Ollama, LM Studio, the engine) are used (M2).
	LocalOnlyLLM bool `json:"localOnlyLLM,omitempty"`
	// NoWatermark turns off the invisible watermark (AudioSeal) that is
	// added to generated audio by default.
	NoWatermark bool `json:"noWatermark,omitempty"`
	// WatermarkID is this install's id in the watermark's message
	// (1–255, chosen at random on first use; internal/watermark).
	WatermarkID int `json:"watermarkID,omitempty"`
}

// Defaults returns the settings of a fresh install. They match the MVP's
// behaviour (Kokoro first, autoplay on, system theme, English unless the
// system language is supported).
func Defaults() Settings {
	s := Settings{
		Version:    Version,
		Appearance: Appearance{Theme: "system", Scale: 1},
		Studio:     Studio{Autoplay: true},
		Models:     Models{AutoUnload: DefaultAutoUnload},
	}
	defaultsM2(&s)
	defaultsAPI(&s) // M5: api.go
	return s
}

// Migrate upgrades a stored document to Version, filling fields added
// since with their defaults. Unknown fields are dropped.
func Migrate(data []byte) (Settings, error) {
	s, _, err := migrate(data)
	return s, err
}

// Migration is a change Migrate made to a stored value, for the
// operations log: the setting's JSON path, the value before and after.
type Migration struct {
	Field    string
	From, To any
}

// migrate is Migrate, also listing the stored values it changed.
func migrate(data []byte) (Settings, []Migration, error) {
	s := Defaults()
	if len(data) == 0 {
		return s, nil, nil
	}
	s.Version = 0 // what the document says
	if err := json.Unmarshal(data, &s); err != nil {
		return Defaults(), nil, err
	}
	var done []Migration
	migrateM7(data, &s)
	// A document without a version is version 1 (the first).
	if s.Version < 2 && s.Models.AutoUnload == 0 {
		// Version 2: 0 ("never") was the default nobody chose.
		s.Models.AutoUnload = DefaultAutoUnload
		done = append(done, Migration{Field: "models.autoUnload", From: 0, To: DefaultAutoUnload})
	}
	if s.Appearance.Scale == 0 {
		s.Appearance.Scale = 1
	}
	if s.Appearance.Theme == "" {
		s.Appearance.Theme = "system"
	}
	s.Version = Version
	return s, done, nil
}

// Changed is published after an update is saved. Topic "settings.changed".
type Changed struct{ Old, New Settings }

func (Changed) Topic() string { return "settings.changed" }

// Service holds the current settings. Goroutine-safe.
type Service interface {
	// Get returns the current settings: a copy, from memory (no I/O), so
	// views may call it while building.
	Get() Settings
	// Update applies fn to a copy, saves it, swaps it in and publishes
	// Changed; a no-op change publishes nothing. fn must not block.
	Update(ctx context.Context, fn func(*Settings)) error
}
