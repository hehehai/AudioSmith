package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hehehai/AudioSmith/internal/family"
	"github.com/hehehai/AudioSmith/internal/llm"
	"github.com/hehehai/AudioSmith/internal/settings"
	"github.com/hehehai/actnowkit/conformance"
	"github.com/hehehai/actnowkit/credentials"
)

const auralinkBundle = "dev.hehehai.auralink"

// keysApp is the app with the family in a fixture, its keys over a fake
// Keychain (never the real one), and Auralink's release build installed
// when withAuralink.
func keysApp(t *testing.T, withAuralink bool) (*app, *ui.Tester, *family.FakeKeychain, *conformance.Fixture) {
	t.Helper()
	a, _, tt := newTestApp(t)
	f, fx := testFamily(t, a, true)
	if withAuralink {
		fx.Register(auralinkBundle)
	}
	f.Rediscover()
	kc := family.NewFakeKeychain()
	a.lib.keys = f.Keys(kc.Keychain())
	tt.SetSize(1100, 2400) // tall: the settings fit without scrolling
	return a, tt, kc, fx
}

// auralinkKeys is Auralink's side of the shared keys.
func auralinkKeys(fx *conformance.Fixture, kc *family.FakeKeychain) *credentials.Store {
	return &credentials.Store{Family: fx.Family, BundleID: auralinkBundle, Keychain: kc.Keychain()}
}

func providerRef(a *app, id string) string {
	for _, p := range a.settings.Get().LLM.Providers {
		if p.ID == id {
			return p.CredentialRef
		}
	}
	return "?"
}

// Requests read a key with a credentialRef through the family (the shared
// key, by provider: SiliconFlow's two sites keep one own item but are
// two vendors), and the others as before.
func TestFamilySecretsResolve(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	au := auralinkKeys(fx, kc)
	au.Save(context.Background(), "siliconflow-intl", "sk-sf-intl-00001", credentials.SaveOptions{})
	au.Save(context.Background(), "deepl", "deepl-shared-0001", credentials.SaveOptions{})
	ctx := context.Background()
	for _, v := range []string{"siliconflow-intl", "deepl"} {
		if _, err := a.lib.keys.Shared.Use(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	a.lib.settings.Update(ctx, func(s *settings.Settings) {
		s.LLM.Providers = []settings.LLMProvider{{ID: "siliconflow"}, {ID: "siliconflow-intl", CredentialRef: credentials.Ref("siliconflow-intl")}}
		s.Dubbing.CredentialRefs = map[string]string{"deepl": credentials.Ref("deepl")}
	})
	fs := familySecrets{keys: a.lib.keys, prefs: a.lib.settings.Get, legacy: llm.StaticSecrets{"llm.siliconflow": "sk-own-cn", "mt.microsoft": "ms-own"}}
	cases := []struct{ provider, name, want string }{
		{"siliconflow", "llm.siliconflow", "sk-own-cn"},
		{"siliconflow-intl", "llm.siliconflow", "sk-sf-intl-00001"},
		{"", "mt.deepl", "deepl-shared-0001"},
		{"", "mt.microsoft", "ms-own"},
	}
	for _, c := range cases {
		if v, err := fs.ProviderSecret(ctx, c.provider, c.name); v != c.want || err != nil {
			t.Errorf("%+v: %q %v", c, v, err)
		}
	}
	if _, err := fs.Secret(ctx, "llm.deepseek"); err != llm.ErrNoSecret {
		t.Errorf("missing: %v", err)
	}
}

// The HF token's order (family spec §8.4) as downloads see it: $HF_TOKEN,
// the shared token the settings name, AudioSmith's own item, the file.
func TestHFTokenOrderShared(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	ctx := context.Background()
	auralinkKeys(fx, kc).Save(ctx, "huggingface", "hf_shared_token_01", credentials.SaveOptions{})
	file := filepath.Join(testDir(t), "token")
	os.WriteFile(file, []byte("hf_file_token"), 0o600)
	t.Setenv("HF_TOKEN_PATH", file)
	t.Setenv("HF_TOKEN", "")
	token := func() string { return hubToken(a.lib.sharedHFToken()) }
	if got := token(); got != "hf_file_token" {
		t.Errorf("no ref: %q", got)
	}
	if kc.Read(credentials.Service, "vendor.huggingface") {
		t.Error("the shared token was read without a ref")
	}
	ref, err := a.lib.keys.Use(ctx, hfKeySlot(a.prefs).slot)
	if err != nil {
		t.Fatal(err)
	}
	a.lib.settings.Update(ctx, func(s *settings.Settings) { s.Models.HFCredentialRef = ref })
	if got := token(); got != "hf_shared_token_01" {
		t.Errorf("shared: %q", got)
	}
	t.Setenv("HF_TOKEN", "hf_env")
	if got := token(); got != "hf_env" {
		t.Errorf("env: %q", got)
	}
}

// Auralink saved a DeepSeek key: the provider's card offers it, nothing
// secret is read until "Use this key", which then sets the credentialRef.
func TestKeyOfferUseThisKey(t *testing.T) {
	a, tt, kc, fx := keysApp(t, true)
	auralinkKeys(fx, kc).Save(context.Background(), "deepseek", "sk-auralink-0001", credentials.SaveOptions{})
	kc.Forget()
	a.addProvider("DeepSeek")
	a.navigate("/settings/writing")
	tt.Frame()
	tt.Frame()
	offer := "Use the DeepSeek key from Auralink (sk-…0001)"
	if !tt.HasText(offer) || !tt.HasText("Enter another key") {
		t.Fatalf("no offer: %q", tt.Texts())
	}
	if !tt.HasText("Shared keys are stored in your login keychain. Like each app's own keys, other programs you run can read them with the security tool.") {
		t.Error("no security note")
	}
	if kc.Read(credentials.Service, "vendor.deepseek") {
		t.Fatal("read before the user agreed")
	}
	if err := tt.Click("Use this key"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ref := providerRef(a, "deepseek"); ref != "actnow:vendor.deepseek" {
		t.Errorf("ref %q", ref)
	}
	if tt.HasText(offer) || !tt.HasText("A key is saved in the Keychain.") {
		t.Errorf("after use: %q", tt.Texts())
	}
	// "Enter another key" brings the input back instead.
	a2, tt2, kc2, fx2 := keysApp(t, true)
	auralinkKeys(fx2, kc2).Save(context.Background(), "deepseek", "sk-auralink-0001", credentials.SaveOptions{})
	a2.addProvider("DeepSeek")
	a2.navigate("/settings/writing")
	tt2.Frame()
	tt2.Frame()
	if err := tt2.Click("Enter another key"); err != nil {
		t.Fatal(err)
	}
	tt2.Frame()
	if tt2.HasText(offer) || !tt2.HasText("Let other ActNow apps on this Mac use this key") {
		t.Errorf("enter another: %q", tt2.Texts())
	}
	if kc2.Read(credentials.Service, "vendor.deepseek") {
		t.Error("Enter another key read the shared key")
	}
}

// Without another ActNow app nothing of the family shows, and a key saved
// is shared all the same (the hidden checkbox is on).
func TestKeySavedSharedWithoutOthers(t *testing.T) {
	a, tt, kc, _ := keysApp(t, false)
	a.addProvider("DeepSeek")
	a.navigate("/settings/writing")
	tt.Frame()
	if tt.HasText("Let other ActNow apps on this Mac use this key") || tt.HasText("Shared keys are stored in your login keychain. Like each app's own keys, other programs you run can read them with the security tool.") {
		t.Errorf("family texts alone: %q", tt.Texts())
	}
	a.saveKey("deepseek", "sk-alone-0000001")
	if ref := providerRef(a, "deepseek"); ref != "actnow:vendor.deepseek" {
		t.Errorf("ref %q", ref)
	}
	if v, _ := kc.Item(credentials.Service, "vendor.deepseek"); v != "sk-alone-0000001" {
		t.Errorf("shared item %q", v)
	}
	if v, _ := kc.Item("AudioSmith", "llm.deepseek"); v != "sk-alone-0000001" {
		t.Errorf("own item %q", v)
	}
	if !a.llmForm.hasKey["deepseek"] {
		t.Error("no key shown")
	}
}

// With Auralink installed the checkbox shows, on by default; off keeps
// the key AudioSmith's own; turned on for a saved key it shares it.
func TestKeyShareCheckbox(t *testing.T) {
	a, tt, kc, _ := keysApp(t, true)
	a.addProvider("DeepSeek")
	a.navigate("/settings/writing")
	tt.Frame()
	tt.Frame()
	v := a.keyView("llm:deepseek")
	if !tt.HasText("Let other ActNow apps on this Mac use this key") || !v.share {
		t.Fatalf("checkbox %v: %q", v.share, tt.Texts())
	}
	if err := tt.Click("Let other ActNow apps on this Mac use this key"); err != nil {
		t.Fatal(err)
	}
	if v.share {
		t.Fatal("still on")
	}
	a.saveKey("deepseek", "sk-mine-00000001")
	tt.Frame()
	if ref := providerRef(a, "deepseek"); ref != "" {
		t.Errorf("shared with the box off: %q", ref)
	}
	if _, ok := kc.Item(credentials.Service, "vendor.deepseek"); ok {
		t.Error("shared item written")
	}
	if err := tt.Click("Let other ActNow apps on this Mac use this key"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ref := providerRef(a, "deepseek"); ref != "actnow:vendor.deepseek" {
		t.Errorf("not shared when turned on: %q", ref)
	}
	if v, _ := kc.Item(credentials.Service, "vendor.deepseek"); v != "sk-mine-00000001" {
		t.Errorf("shared item %q", v)
	}
	if _, ok := kc.Item("AudioSmith", "llm.deepseek"); !ok {
		t.Error("own item gone after sharing")
	}
}

// Removing a key Auralink uses asks: Stop using it here (Auralink keeps
// it) or Delete for all apps.
func TestKeyRemoveAsks(t *testing.T) {
	for _, forAll := range []bool{false, true} {
		a, tt, kc, fx := keysApp(t, true)
		a.addProvider("DeepSeek")
		a.saveKey("deepseek", "sk-shared-000001")
		auralinkKeys(fx, kc).Use(context.Background(), "deepseek")
		a.reloadKeys()
		a.navigate("/settings/writing")
		tt.Frame()
		tt.Frame()
		typed := "sk-new-000000001"
		a.llmForm.edits["deepseek"].key = typed
		tt.Frame()
		if !tt.HasText("Shared across ActNow: the other apps get the new key too.") {
			t.Errorf("no also-updates: %q", tt.Texts())
		}
		a.llmForm.edits["deepseek"].key = ""
		if err := tt.Click("Remove the key"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !a.keyAsk || !tt.HasText("Auralink also uses this key.") {
			t.Fatalf("no alert: %q", tt.Texts())
		}
		button := "Stop using it here"
		if forAll {
			button = "Delete for all apps"
		}
		if err := tt.Click(button); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		_, kept := kc.Item(credentials.Service, "vendor.deepseek")
		if kept == forAll {
			t.Errorf("%s: shared item kept %v", button, kept)
		}
		if ref := providerRef(a, "deepseek"); ref != "" {
			t.Errorf("%s: ref %q", button, ref)
		}
		if _, ok := kc.Item("AudioSmith", "llm.deepseek"); ok {
			t.Errorf("%s: own item kept", button)
		}
	}
}

// AudioSmith's own key differs from Auralink's: the card says so, and
// offers Auralink's.
func TestKeyDifferentPrompt(t *testing.T) {
	a, tt, kc, fx := keysApp(t, true)
	auralinkKeys(fx, kc).Save(context.Background(), "deepseek", "sk-auralink-0001", credentials.SaveOptions{})
	kc.Put("AudioSmith", "llm.deepseek", "sk-audiosmith-01")
	kc.Forget()
	a.addProvider("DeepSeek")
	a.navigate("/settings/writing")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Auralink has a different DeepSeek key.") || tt.HasText("Use the DeepSeek key from Auralink (sk-…0001)") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if kc.Read(credentials.Service, "vendor.deepseek") {
		t.Error("read the shared key to compare")
	}
	if err := tt.Click("Use this key"); err != nil {
		t.Fatal(err)
	}
	if ref := providerRef(a, "deepseek"); ref != "actnow:vendor.deepseek" {
		t.Errorf("ref %q", ref)
	}
}

// The Hugging Face token and the translators' keys go the same way.
func TestKeyHFAndTranslator(t *testing.T) {
	a, tt, kc, fx := keysApp(t, true)
	tallSettings(tt) // the token is under the list of models
	au := auralinkKeys(fx, kc)
	au.Save(context.Background(), "huggingface", "hf_auralink_tok_01", credentials.SaveOptions{})
	openModels(a)
	a.settingsReveal = "storage-models" // the token, under the list of models
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Use the Hugging Face key from Auralink (hf_…k_01)") {
		t.Fatalf("no HF offer: %q", tt.Texts())
	}
	if err := tt.Click("Use this key"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Get().Models.HFCredentialRef != "actnow:vendor.huggingface" {
		t.Errorf("HF ref %q", a.settings.Get().Models.HFCredentialRef)
	}
	ks := mtKeySlot(a.prefs, "deepl")
	a.saveFamilyKey(ks, "deepl-key-000001")
	if got := a.settings.Get().Dubbing.CredentialRefs["deepl"]; got != "actnow:vendor.deepl" {
		t.Errorf("MT ref %q", got)
	}
	a.removeFamilyKey(mtKeySlot(a.prefs, "deepl"))
	if _, ok := a.settings.Get().Dubbing.CredentialRefs["deepl"]; ok {
		t.Error("MT ref kept")
	}
	if _, ok := kc.Item(credentials.Service, "vendor.deepl"); ok {
		t.Error("a key only AudioSmith used stayed")
	}
}

// Erase everything takes AudioSmith off the shared keys and deletes none.
func TestEraseStopsUsingSharedKeys(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	a.addProvider("DeepSeek")
	a.saveKey("deepseek", "sk-shared-000001")
	auralinkKeys(fx, kc).Use(context.Background(), "deepseek")
	a.forgetFamilyKeys()
	if _, ok := kc.Item(credentials.Service, "vendor.deepseek"); !ok {
		t.Fatal("erase deleted Auralink's key")
	}
	e, _ := a.lib.keys.Shared.Offer("deepseek")
	if !slices.Equal(e.Users, []string{auralinkBundle}) {
		t.Errorf("users %v", e.Users)
	}
}

// Auralink deleted the shared DeepSeek key for all apps (a leaked key):
// a request finds the key missing — AudioSmith's own copy of the same key
// is deleted too, the ref and its fingerprint go — and a toast asks for a
// key again.
func TestSharedKeyDeletedForAllOnUse(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	a.addProvider("DeepSeek")
	a.saveKey("deepseek", "sk-leaked-000001")
	auralinkKeys(fx, kc).Use(context.Background(), "deepseek")
	fp := credentials.Fingerprint("sk-leaked-000001")
	if got := a.settings.Get().Family.KeyFingerprints["deepseek"]; got != fp {
		t.Fatalf("fingerprint kept %q", got)
	}
	if err := auralinkKeys(fx, kc).DeleteForAll(context.Background(), "deepseek"); err != nil {
		t.Fatal(err)
	}
	a.toasts = nil
	fs := familySecrets{keys: a.lib.keys, prefs: a.lib.settings.Get, legacy: llm.StaticSecrets{}, gone: a.dropSharedKey}
	if v, err := fs.ProviderSecret(context.Background(), "deepseek", "llm.deepseek"); err != llm.ErrNoSecret {
		t.Errorf("after the delete: %q %v", v, err)
	}
	if _, ok := kc.Item("AudioSmith", "llm.deepseek"); ok {
		t.Error("AudioSmith's copy of the deleted key stayed")
	}
	if ref := providerRef(a, "deepseek"); ref != "" {
		t.Errorf("ref %q", ref)
	}
	if len(a.settings.Get().Family.KeyFingerprints) != 0 {
		t.Errorf("fingerprints %v", a.settings.Get().Family.KeyFingerprints)
	}
	if !slices.Contains(a.toasts, "The shared DeepSeek key was deleted. Enter a key again.") {
		t.Errorf("toasts %q", a.toasts)
	}
	if a.llmForm.hasKey["deepseek"] {
		t.Error("the card still shows a key")
	}
}

// The notification path (credentials.json changed): an own key that
// differs, typed for AudioSmith alone, stays and is used; the HF token's
// own copy goes when it is the same token.
func TestSharedKeyDeletedForAllOnNotice(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	ctx := context.Background()
	au := auralinkKeys(fx, kc)
	au.Save(ctx, "deepseek", "sk-auralink-0001", credentials.SaveOptions{})
	au.Save(ctx, "huggingface", "hf_auralink_tok_01", credentials.SaveOptions{})
	kc.Put("AudioSmith", "llm.deepseek", "sk-audiosmith-01")
	a.addProvider("DeepSeek")
	ks, _ := llmKeySlot(a.prefs, "deepseek")
	a.useFamilyKey(ks) // "Auralink has a different DeepSeek key." → Use this key
	a.useFamilyKey(hfKeySlot(a.prefs))
	if providerRef(a, "deepseek") == "" || a.settings.Get().Models.HFCredentialRef == "" {
		t.Fatal("not using the shared keys")
	}
	// AudioSmith's own copy of the HF token, as an older version left it.
	kc.Put("AudioSmith", "huggingface-token", "hf_auralink_tok_01")
	au.DeleteForAll(ctx, "deepseek")
	au.DeleteForAll(ctx, "huggingface")
	kc.Forget()
	a.checkSharedKeys()
	if providerRef(a, "deepseek") != "" || a.settings.Get().Models.HFCredentialRef != "" {
		t.Errorf("refs kept: %q %q", providerRef(a, "deepseek"), a.settings.Get().Models.HFCredentialRef)
	}
	if v, _ := kc.Item("AudioSmith", "llm.deepseek"); v != "sk-audiosmith-01" {
		t.Errorf("the different own key went: %q", v)
	}
	if _, ok := kc.Item("AudioSmith", "huggingface-token"); ok {
		t.Error("the own copy of the deleted HF token stayed")
	}
	if kc.Read(credentials.Service, "vendor.deepseek") || kc.Read(credentials.Service, "vendor.huggingface") {
		t.Error("read a shared item")
	}
	own := llm.SecretsFunc(func(_ context.Context, name string) (string, error) { return a.lib.keys.Own.Get(name) })
	fs := familySecrets{keys: a.lib.keys, prefs: a.lib.settings.Get, legacy: own, gone: a.dropSharedKey}
	if v, err := fs.ProviderSecret(ctx, "deepseek", "llm.deepseek"); v != "sk-audiosmith-01" || err != nil {
		t.Errorf("the own key is used: %q %v", v, err)
	}
}

// Another app changed the shared key: the fingerprint kept follows it.
func TestSharedKeyFingerprintFollows(t *testing.T) {
	a, _, kc, fx := keysApp(t, true)
	a.addProvider("DeepSeek")
	a.saveKey("deepseek", "sk-first-0000001")
	auralinkKeys(fx, kc).Save(context.Background(), "deepseek", "sk-second-000001", credentials.SaveOptions{})
	a.checkSharedKeys()
	if got := a.settings.Get().Family.KeyFingerprints["deepseek"]; got != credentials.Fingerprint("sk-second-000001") {
		t.Errorf("fingerprint %q", got)
	}
}
