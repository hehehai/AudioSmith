package main

// Settings → Writing help: the routes, the providers (add, remove, keys,
// test) and their cards; split from transcribe_llm.go.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/i18n"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
)

// presetLabels are the presets "Add a provider" offers.
func presetLabels() []string {
	var out []string
	for _, p := range llm.Presets() {
		out = append(out, p.Name)
	}
	return out
}

// routeOptions are the choices of "Use for writing help": automatic,
// none, then each provider set up.
func (a *app) routeOptions() (labels, ids []string) {
	labels = []string{i18n.T("Automatic"), i18n.T("None: the built-in polish only")}
	ids = []string{"auto", ""}
	if a.localLLMReady() {
		labels, ids = append(labels, providerName(localProvider, nil)), append(ids, localProvider)
	}
	for _, p := range a.prefs.LLM.Providers {
		labels, ids = append(labels, providerName(p.ID, a.prefs.LLM.Providers)), append(ids, p.ID)
	}
	return labels, ids
}

// chooseLLMRoute chooses writing help's model: "auto", "" (none) or a
// provider id.
func (a *app) chooseLLMRoute(id string) {
	a.updateSettings(func(s *settings.Settings) {
		s.LLM.Manual = id != "auto"
		s.LLM.Default = nil
		if id != "auto" && id != "" {
			s.LLM.Default = []settings.LLMRoute{{Provider: id}}
		}
	})
	a.applyLLM()
	a.syncLLMForm()
}

// addProvider adds a provider from a preset.
func (a *app) addProvider(label string) {
	for _, p := range llm.Presets() {
		if p.Name != label {
			continue
		}
		id := p.ID
		for n := 2; slices.ContainsFunc(a.prefs.LLM.Providers, func(x settings.LLMProvider) bool { return x.ID == id }); n++ {
			id = p.ID + "-" + itoa(n)
		}
		np := settings.LLMProvider{ID: id, Preset: p.ID}
		a.updateSettings(func(s *settings.Settings) { s.LLM.Providers = append(slices.Clone(s.LLM.Providers), np) })
		a.applyLLM()
		a.syncLLMForm()
		return
	}
}

// removeProvider removes a provider and its routes (its key stays in the
// Keychain until "Remove the key").
func (a *app) removeProvider(id string) {
	a.updateSettings(func(s *settings.Settings) {
		s.LLM.Providers = slices.DeleteFunc(slices.Clone(s.LLM.Providers), func(p settings.LLMProvider) bool { return p.ID == id })
		s.LLM.Default = slices.DeleteFunc(slices.Clone(s.LLM.Default), func(r settings.LLMRoute) bool { return r.Provider == id })
		if s.LLM.Skills != nil {
			m := map[string][]settings.LLMRoute{}
			for k, v := range s.LLM.Skills {
				if v = slices.DeleteFunc(slices.Clone(v), func(r settings.LLMRoute) bool { return r.Provider == id }); len(v) > 0 {
					m[k] = v
				}
			}
			s.LLM.Skills = m
		}
	})
	a.applyLLM()
}

// setProvider changes a provider's fields.
func (a *app) setProvider(id string, fn func(*settings.LLMProvider)) {
	a.updateSettings(func(s *settings.Settings) {
		ps := slices.Clone(s.LLM.Providers)
		for i := range ps {
			if ps[i].ID == id {
				fn(&ps[i])
			}
		}
		s.LLM.Providers = ps
	})
	a.applyLLM()
}

// loadKeyInfo finds whether a provider's key is saved (the Keychain blocks).
func (a *app) loadKeyInfo(id string, p llm.Preset) {
	if p.Local || p.Auth == llm.AuthNone {
		return
	}
	if ks, ok := llmKeySlot(a.prefs, id); ok && a.lib.keys != nil {
		a.loadKey(ks, a.keyLoaded(ks)) // family_keys.go: own and shared keys
		return
	}
	name := llm.ProviderConfig{ID: id, Preset: p.ID}
	pr, err := name.Resolve()
	if err != nil {
		pr = p
	}
	secret := pr.SecretName()
	a.async(func() {
		_, err := llmSecrets.Secret(context.Background(), secret)
		a.post(func() { a.llmForm.hasKey[id] = err == nil })
	})
}

// saveKey keeps a provider's API key in the Keychain ("" removes it).
func (a *app) saveKey(id, key string) {
	cfg := a.providerConfig(id)
	pr, err := cfg.Resolve()
	if err != nil {
		a.toastWarn(i18n.T("Could not save the key: %v", err))
		return
	}
	item := keychainItem(pr.SecretName())
	key = strings.TrimSpace(key)
	if e := a.llmForm.edits[id]; e != nil {
		e.key = ""
	}
	if ks, ok := llmKeySlot(a.prefs, id); ok && a.lib.keys != nil {
		// family_keys.go: shared with the other ActNow apps when the user
		// lets them; removing asks first when they use it.
		if key == "" {
			a.removeFamilyKey(ks)
		} else {
			a.saveFamilyKey(ks, key)
		}
		return
	}
	a.async(func() {
		err := item.Set(key)
		a.post(func() {
			switch {
			case err != nil:
				a.toastWarn(i18n.T("Could not save the key: %v", err))
			case key == "":
				a.llmForm.hasKey[id] = false
				a.toast(i18n.T("Key removed"))
			default:
				a.llmForm.hasKey[id] = true
				a.toast(i18n.T("Key saved in the Keychain"))
			}
		})
	})
}

func (a *app) providerConfig(id string) llm.ProviderConfig {
	for _, p := range a.prefs.LLM.Providers {
		if p.ID == id {
			return llm.ProviderConfig{ID: p.ID, Preset: p.Preset, Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, Headers: p.Headers, JSONMode: llm.JSONMode(p.JSONMode)}
		}
	}
	return llm.ProviderConfig{ID: id}
}

// testProvider sends a provider a one-word question and shows the answer
// and how long it took; it lists the provider's models too.
func (a *app) testProvider(id string) {
	b, ok := a.llmReg.Backend(id)
	if !ok {
		a.llmForm.test[id], a.llmForm.testErr[id] = i18n.T("This provider is not set up: check its address."), true
		return
	}
	a.llmForm.testing[id] = true
	a.llmForm.test[id] = ""
	model := a.providerConfig(id).Model
	a.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := time.Now()
		client := &llm.Client{Backend: b, Timeout: 25 * time.Second, Retry: llm.RetryPolicy{MaxAttempts: 1}}
		resp, err := client.Chat(ctx, llm.Request{Model: model, MaxTokens: 16,
			Messages: []llm.Message{{Role: llm.User, Content: "Reply with the single word OK."}}})
		models, _ := b.Models(ctx)
		elapsed := time.Since(start)
		a.post(func() {
			a.llmForm.testing[id] = false
			if err != nil {
				a.llmForm.test[id], a.llmForm.testErr[id] = llmErrorText(err), true
				return
			}
			a.llmForm.test[id], a.llmForm.testErr[id] = i18n.T("It answered %q in %.1f s (%s).", strings.TrimSpace(resp.Text), elapsed.Seconds(), resp.Model), false
			if len(models) > 0 {
				var names []string
				for _, m := range models {
					names = append(names, m.ID)
				}
				a.llmForm.models[id] = names
			}
		})
	})
}

// llmErrorText explains a language model's error.
func llmErrorText(err error) string {
	var e *llm.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	switch e.Code {
	case llm.CodeNoKey:
		return i18n.T("No API key: paste one and save it.")
	case llm.CodeAuth:
		return i18n.T("The API key was refused.")
	case llm.CodeRateLimited:
		return i18n.T("Too many requests: try again in a moment.")
	case llm.CodeModelNotFound:
		return i18n.T("The provider does not have the model %s.", e.Params["model"])
	case llm.CodeTimeout:
		return i18n.T("No answer in time.")
	case llm.CodeNetwork:
		return i18n.T("Could not connect: is the server running, and the address right?")
	case llm.CodeConfig:
		return i18n.T("The provider's address is missing or wrong.")
	}
	return err.Error()
}

// writingSettings is Settings → Writing help.
func (a *app) writingSettings(c *ui.Context) {
	t := c.Theme()
	f := &a.llmForm
	if f.edits == nil {
		return
	}
	caption(c, i18n.T("A language model tidies transcripts and dictations: fillers, self-corrections, punctuation."))

	settingGroup(c, i18n.T("Which model"), func() {
		labels, ids := a.routeOptions()
		settingRow(c, "llm-route", i18n.T("Use for writing help"), i18n.T("Automatic: AudioSmith's model, else Ollama."), func() {
			if ui.Select(c.Key("llm-route-select"), &f.route, labels).Label(i18n.T("Use for writing help")).Width(settingW(t)).Changed() {
				a.chooseLLMRoute(ids[max(slices.Index(labels, f.route), 0)])
			}
		})
		cleanLabels, cleanIDs := cleanOptions(labels, ids)
		settingRow(c, "llm-clean-route", i18n.T("Dictation clean-up"), i18n.T("Waits at most 4 s: a fast local model works best."), func() {
			if ui.Select(c.Key("llm-clean-select"), &f.clean, cleanLabels).Label(i18n.T("Dictation clean-up")).Width(settingW(t)).Changed() {
				id := cleanIDs[max(slices.Index(cleanLabels, f.clean), 0)]
				a.updateSettings(func(s *settings.Settings) {
					m := map[string][]settings.LLMRoute{}
					for k, v := range s.LLM.Skills {
						m[k] = slices.Clone(v)
					}
					delete(m, string(llm.SkillDictationCleanup))
					if id != "" {
						m[string(llm.SkillDictationCleanup)] = []settings.LLMRoute{{Provider: id}}
					}
					s.LLM.Skills = m
				})
				a.applyLLM()
			}
		})
		settingRow(c, "llm-local", i18n.T("Keep text on this Mac"), i18n.T("Only local models (Ollama, LM Studio) are used; cloud providers are skipped."), func() {
			if inkSwitch(c.Key("llm-local-switch"), &f.local).Label(i18n.T("Keep text on this Mac")).Changed() {
				on := f.local
				a.updateSettings(func(s *settings.Settings) { s.Privacy.LocalOnlyLLM = on })
				a.applyLLM()
			}
		})

	})
	a.localLLMSettings(c) // local_llm.go
	settingGroup(c, i18n.T("Providers"), func() {
		a.keySecurityHelp(c) // family_keys_view.go: with another ActNow app only
		ollama := i18n.T("Not running on this Mac.")
		if a.llmOllama != nil {
			ollama = i18n.N(len(a.llmOllama), "Running with %d model; it needs no setup.", "Running with %d models; it needs no setup.", len(a.llmOllama))
			if len(a.llmOllama) == 0 {
				ollama = i18n.T("Running, but without a model: pull one, e.g. ollama pull qwen3:4b.")
			}
		}
		settingRowNote(c, "llm-ollama", i18n.T("Ollama on this Mac"), ollama, func() {
			if secondaryButton(c.Key("llm-recheck"), i18n.T("Look for Ollama")).Clicked() {
				a.checkOllama()
			}
		})
		for _, p := range a.prefs.LLM.Providers {
			settingBlock(c, "llm-pb-"+p.ID, func() { a.providerCard(c, p) })
		}
		settingRow(c, "llm-add", i18n.T("Add a provider"), i18n.T("Cloud services or local servers."), func() {
			opts := presetLabels()
			if f.add == "" {
				f.add = opts[0]
			}
			ui.Select(c.Key("llm-add-select"), &f.add, opts).Label(i18n.T("Provider")).Width(px(t, 180))
			if secondaryButton(c.Key("llm-add-button"), i18n.T("Add")).Clicked() {
				a.addProvider(f.add)
			}
		})
	})
}

// providerCard is one provider: address, model, key, test, remove.
func (a *app) providerCard(c *ui.Context, p settings.LLMProvider) {
	t := c.Theme()
	f := &a.llmForm
	e := f.edits[p.ID]
	if e == nil {
		return
	}
	cfg := a.providerConfig(p.ID)
	pr, resolveErr := cfg.Resolve()
	preset, _ := llm.LookupPreset(pid(cfg))
	ui.Column(c.Key("llm-p-" + p.ID)).Gap(px(t, sp2)).Children(func() {
		ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, providerName(p.ID, a.prefs.LLM.Providers)).Bold().Grow(1)
			where := i18n.T("Cloud: text is sent to this provider")
			if pr.Local || preset.Local {
				where = i18n.T("On this Mac")
			}
			ui.Text(c, where).FontSize(px(t, 11)).TextColor(t.TextMuted)
			if secondaryButton(c.Key("test"), i18n.T("Test")).Disabled(f.testing[p.ID]).Clicked() {
				a.testProvider(p.ID)
			}
			if secondaryButton(c.Key("remove"), i18n.T("Remove")).Clicked() {
				a.removeProvider(p.ID)
			}
		})
		ui.Field(c, i18n.T("Address (Return saves it)"), func() {
			if ui.TextInput(c.Key("base"), &e.base).Placeholder(preset.BaseURL).Label(i18n.T("Address")).Submitted() {
				v := strings.TrimSpace(e.base)
				a.setProvider(p.ID, func(x *settings.LLMProvider) { x.BaseURL = v })
			}
		})
		if resolveErr != nil {
			ui.Text(c, i18n.T("The provider's address is missing or wrong.")).FontSize(px(t, 12)).TextColor(t.Danger)
		}
		ui.Field(c, i18n.T("Model"), func() {
			if ms := f.models[p.ID]; len(ms) > 0 {
				if e.model == "" {
					e.model = pr.DefaultModel
				}
				if !slices.Contains(ms, e.model) {
					ms = append([]string{e.model}, ms...)
				}
				if ui.Select(c.Key("model-select"), &e.model, ms).Label(i18n.T("Model")).Width(px(t, 260)).Changed() {
					v := e.model
					a.setProvider(p.ID, func(x *settings.LLMProvider) { x.Model = v })
				}
			} else if ui.TextInput(c.Key("model"), &e.model).Placeholder(pr.DefaultModel).Label(i18n.T("Model")).Width(px(t, 260)).Submitted() {
				v := strings.TrimSpace(e.model)
				a.setProvider(p.ID, func(x *settings.LLMProvider) { x.Model = v })
			}
		})
		if !pr.Local && !preset.Local && preset.Auth != llm.AuthNone {
			ui.Text(c, i18n.T("API key")).FontSize(px(t, 12)).TextColor(t.TextMuted)
			a.providerKey(c, p.ID, e)
		}
		if msg := f.test[p.ID]; msg != "" {
			color := t.Success
			if f.testErr[p.ID] {
				color = t.Danger
			}
			ui.Text(c, msg).FontSize(px(t, 12)).TextColor(color).Selectable()
		} else if f.testing[p.ID] {
			ui.Text(c, i18n.T("Testing…")).FontSize(px(t, 12)).TextColor(t.TextMuted)
		}
	})
}

// providerKey is a provider's API key: its input, Save and Remove the
// key; or another ActNow app's key to use (family_keys_view.go).
func (a *app) providerKey(c *ui.Context, id string, e *providerEdit) {
	t := c.Theme()
	f := &a.llmForm
	ks, fam := llmKeySlot(a.prefs, id)
	if fam && a.keyOffer(c, ks, true) {
		return
	}
	ui.Row(c).Gap(px(t, 8)).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("key"), &e.key).Password().Placeholder(i18n.T("API key")).Label(i18n.T("API key")).Width(px(t, 220))
		if secondaryButton(c.Key("key-save"), i18n.T("Save")).Disabled(strings.TrimSpace(e.key) == "").Clicked() {
			a.saveKey(id, e.key)
		}
		if f.hasKey[id] {
			ui.Text(c, i18n.T("A key is saved in the Keychain.")).FontSize(px(t, 12)).TextColor(t.Success)
			if secondaryButton(c.Key("key-remove"), i18n.T("Remove the key")).Clicked() {
				a.saveKey(id, "")
			}
		}
	})
	if fam {
		a.keyNotes(c, ks, e.key)
	}
}
