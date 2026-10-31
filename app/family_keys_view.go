package main

// The views of shared keys (family_keys.go; family spec v1.0.4 §8.3):
// the offer of another app's key, the share checkbox, "Also updates it
// in …", "… has a different … key", the security note, the alert of
// removing a key other apps use, and the online translators' key row.
// Every family text shows only with another ActNow app's release build
// installed (familyUI), and names it.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/i18n"
)

// ensureKey reads a key's state once, when a view first shows it.
func (a *app) ensureKey(ks keySlot) *keyView {
	v := a.keyView(ks.id)
	if a.lib.keys != nil && !v.known && !v.busy {
		v.busy = true
		a.loadKey(ks, func(info family.KeyInfo) {
			a.keyView(ks.id).busy = false
			a.keyLoaded(ks)(info)
		})
	}
	return v
}

// vendorName is the vendor's name in the UI's language.
func vendorName(vendor string) string {
	return family.VendorName(vendor, strings.HasPrefix(i18n.Locale(), "zh"))
}

// keyOffer shows, instead of the key's input, another app's key for the
// vendor — "Use the DeepSeek key from Auralink (sk-…a1b2)" with [Use
// this key] and [Enter another key] — while AudioSmith has no key of
// its own; it reports whether it did. Nothing secret is read until the
// user clicks Use this key. primary: Use this key is the view's primary
// button (not on the Dub page, which has its own).
func (a *app) keyOffer(c *ui.Context, ks keySlot, primary bool) bool {
	v, ok := a.offering(ks)
	if !ok {
		return false
	}
	in := v.info
	t := c.Theme()
	ui.Row(c.Key("key-offer")).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, i18n.T("Use the %s key from %s (%s)", vendorName(in.Vendor), in.From, in.Offer.Hint)).Grow(1).MinWidth(px(t, 160))
		use := secondaryButton
		if primary {
			use = primaryButton
		}
		if use(c.Key("key-use"), i18n.T("Use this key")).Disabled(v.busy).Clicked() {
			a.useFamilyKey(ks)
		}
		if secondaryButton(c.Key("key-another"), i18n.T("Enter another key")).Clicked() {
			v.another = true
		}
	})
	return true
}

// offering reports whether the key's input gives way to another app's
// key (keyOffer).
func (a *app) offering(ks keySlot) (*keyView, bool) {
	if a.lib.keys == nil || len(a.familyUI()) == 0 {
		return nil, false
	}
	v := a.ensureKey(ks)
	in := v.info
	return v, v.known && !v.another && in.Vendor != "" && in.Offer != nil && in.From != "" && !in.Own && !in.Shared && !in.ReadOnly
}

// keyNotes go under a key's input: the share checkbox (for a key not
// shared yet), "Also updates it in Auralink." while a new key for a
// shared one is typed, and "Auralink has a different DeepSeek key."
// with Use this key.
func (a *app) keyNotes(c *ui.Context, ks keySlot, typed string) {
	if a.lib.keys == nil {
		return
	}
	others := a.familyUI()
	if len(others) == 0 {
		return
	}
	if _, offered := a.offering(ks); offered {
		return // the offer stands for the input and its notes
	}
	v := a.ensureKey(ks)
	in := v.info
	if !v.known || in.Vendor == "" || in.ReadOnly {
		return
	}
	shows := !in.Shared || in.Shared && len(in.Others) > 0 && strings.TrimSpace(typed) != "" || in.Different && in.From != ""
	if !shows {
		return
	}
	if settingDepth > 0 {
		// In a Settings group: a foot of the key's row, not text against it.
		settingFoot(c, "key-notes", func() { a.keyNoteLines(c, ks, v, typed) })
		return
	}
	a.keyNoteLines(c, ks, v, typed)
}

// keyNoteLines are keyNotes' lines.
func (a *app) keyNoteLines(c *ui.Context, ks keySlot, v *keyView, typed string) {
	t := c.Theme()
	in := v.info
	if !in.Shared {
		if ui.Checkbox(c.Key("key-share"), &v.share, i18n.T("Let other ActNow apps on this Mac use this key")).Disabled(v.busy).Changed() && v.share && in.Own {
			a.shareFamilyKey(ks) // the key AudioSmith had: shared now
		}
	}
	if in.Shared && len(in.Others) > 0 && strings.TrimSpace(typed) != "" {
		caption(c.Key("key-updates"), i18n.T("Also updates it in %s.", listJoin(in.Others)))
	}
	if in.Different && in.From != "" {
		ui.Row(c.Key("key-different")).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
			caption(c, i18n.T("%s has a different %s key.", in.From, vendorName(in.Vendor)))
			if secondaryButton(c.Key("key-use-theirs"), i18n.T("Use this key")).Disabled(v.busy).Clicked() {
				a.useFamilyKey(ks)
			}
		})
	}
}

// keySecurityHelp is the family spec's note on where shared keys live
// (§8.2), with the keys' settings.
func (a *app) keySecurityHelp(c *ui.Context) {
	if a.lib.keys == nil || len(a.familyUI()) == 0 {
		return
	}
	settingNote(c, "key-security", i18n.T("Shared keys are stored in your login keychain. Like each app's own keys, other programs you run can read them with the security tool.")) // the family's wording (actnowkit credentials.TextSecurityHelp)
}

// keyAlerts asks before removing a key other apps use (spec §8.3): Stop
// using it here (the default) or Delete for all apps.
func (a *app) keyAlerts(c *ui.Context) {
	if !a.keyAsk {
		return
	}
	ks, names := a.keyAskSlot, listJoin(a.keyAskNames)
	switch ui.AlertDialog(c, &a.keyAsk, i18n.T("%s also uses this key.", names),
		i18n.T("Stop using it here keeps the key for %s.", names),
		i18n.T("Cancel"), i18n.T("Delete for all apps"), i18n.T("Stop using it here")) {
	case 1:
		a.removeFamilyKeyNow(ks, true)
	case 2:
		a.removeFamilyKeyNow(ks, false)
	}
}

// mtKeyRow is an online translator's API key on the Dub page (the
// Translate step's options): the offer of another app's key, else the
// key's input with Save and, once saved, Remove the key.
func (a *app) mtKeyRow(c *ui.Context, svc string) {
	if a.lib.keys == nil {
		return // tests: the key row needs the Keychain
	}
	t := c.Theme()
	ks := mtKeySlot(a.prefs, svc)
	v := a.ensureKey(ks)
	if a.keyOffer(c, ks, false) {
		return
	}
	input := a.mtKeyInput(svc)
	ui.Row(c.Key("mt-key")).Wrap().Gap(px(t, sp2)).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("mt-key-input"), input).Password().Placeholder(i18n.T("API key")).Label(i18n.T("API key")).Width(px(t, 220))
		if secondaryButton(c.Key("mt-key-save"), i18n.T("Save")).Disabled(strings.TrimSpace(*input) == "" || v.busy).Clicked() {
			key := *input
			*input = ""
			a.saveFamilyKey(ks, key)
		}
		if v.info.Own || v.info.Shared {
			caption(c, i18n.T("A key is saved in the Keychain.")).TextColor(t.Success)
			if secondaryButton(c.Key("mt-key-remove"), i18n.T("Remove the key")).Disabled(v.busy).Clicked() {
				a.removeFamilyKey(ks)
			}
		}
	})
	a.keyNotes(c, ks, *input)
	a.keySecurityHelp(c)
}

// mtKeyInput is what is typed in a translator's key input (not saved).
func (a *app) mtKeyInput(svc string) *string {
	if a.mtKeys == nil {
		a.mtKeys = map[string]*string{}
	}
	p := a.mtKeys[svc]
	if p == nil {
		p = new(string)
		a.mtKeys[svc] = p
	}
	return p
}
