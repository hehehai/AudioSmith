package hub

// The Hugging Face token and the other Keychain secrets.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestToken(t *testing.T) {
	saved, savedStore := security, platformSecrets
	defer func() { security, platformSecrets = saved, savedStore }()
	platformSecrets = nil
	var stdin string
	var args []string
	security = func(in string, a ...string) ([]byte, error) {
		stdin, args = in, a
		if a[0] == "find-generic-password" {
			return []byte("hf_fromkeychain\n"), nil
		}
		return nil, nil
	}
	k := Keychain{Service: "S", Account: "A"}
	if err := k.Set("hf_abc"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"-i"}) || !strings.Contains(stdin, `-w "hf_abc"`) || strings.Contains(strings.Join(args, " "), "hf_abc") {
		t.Errorf("set: args %q stdin %q", args, stdin)
	}
	if err := k.Set(`bad"token`); err == nil {
		t.Error("accepted a quote")
	}
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HF_TOKEN_PATH", filepath.Join(t.TempDir(), "token"))
	if tok, src := Token(k); tok != "hf_fromkeychain" || src != TokenKeychain {
		t.Errorf("keychain: %q %q", tok, src)
	}
	t.Setenv("HF_TOKEN", "hf_env")
	if tok, src := Token(k); tok != "hf_env" || src != TokenEnv {
		t.Errorf("env: %q %q", tok, src)
	}
	t.Setenv("HF_TOKEN", "")
	security = func(string, ...string) ([]byte, error) { return nil, ErrNoKeychain }
	os.WriteFile(os.Getenv("HF_TOKEN_PATH"), []byte("hf_file\n"), 0o600)
	if tok, src := Token(k); tok != "hf_file" || src != TokenCLI {
		t.Errorf("file: %q %q", tok, src)
	}
	if MaskToken("hf_abcdefghijkl") != "hf_…ijkl" || MaskToken("short") != "•••••" {
		t.Error(MaskToken("hf_abcdefghijkl"))
	}
}

// Every secret is an AudioSmith item with its own account and label; the
// HF token keeps the account it always had.
func TestSecrets(t *testing.T) {
	saved, savedStore := security, platformSecrets
	defer func() { security, platformSecrets = saved, savedStore }()
	platformSecrets = nil
	var stdin string
	security = func(in string, a ...string) ([]byte, error) { stdin = in; return nil, nil }
	if DefaultKeychain.Service != "AudioSmith" || DefaultKeychain.Account != "huggingface-token" {
		t.Errorf("HF token item moved: %+v", DefaultKeychain)
	}
	if err := Secret("llm.openai", "AudioSmith OpenAI API key").Set("sk-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdin, `-s "AudioSmith" -a "llm.openai" -l "AudioSmith OpenAI API key"`) {
		t.Errorf("stdin %q", stdin)
	}
	Secret("mt.deepl", "").Set("k")
	if !strings.Contains(stdin, `-l "AudioSmith mt.deepl"`) {
		t.Errorf("default label: %q", stdin)
	}
}

// The family spec's order (§8.4): $HF_TOKEN, the shared token, AudioSmith's
// own item, the token file; a shared token that cannot be read counts as
// none.
func TestTokenOrder(t *testing.T) {
	saved, savedStore := security, platformSecrets
	defer func() { security, platformSecrets = saved, savedStore }()
	platformSecrets = nil
	own := ""
	security = func(_ string, a ...string) ([]byte, error) {
		if a[0] == "find-generic-password" && own != "" {
			return []byte(own + "\n"), nil
		}
		return nil, ErrNoKeychain
	}
	k := Keychain{Service: "S", Account: "A"}
	file := filepath.Join(t.TempDir(), "token")
	t.Setenv("HF_TOKEN_PATH", file)
	os.WriteFile(file, []byte("hf_file\n"), 0o600)
	shared := func() (string, error) { return " hf_shared ", nil }
	broken := func() (string, error) { return "", errors.New("not consented") }
	cases := []struct {
		env, own string
		shared   func() (string, error)
		want     string
		src      TokenSource
	}{
		{"hf_env", "hf_own", shared, "hf_env", TokenEnv},
		{"", "hf_own", shared, "hf_shared", TokenShared},
		{"", "hf_own", broken, "hf_own", TokenKeychain},
		{"", "hf_own", nil, "hf_own", TokenKeychain},
		{"", "", broken, "hf_file", TokenCLI},
	}
	for _, c := range cases {
		t.Setenv("HF_TOKEN", c.env)
		own = c.own
		if tok, src := TokenFrom(c.shared, k); tok != c.want || src != c.src {
			t.Errorf("%+v: %q %q", c, tok, src)
		}
	}
}

type memSecretStore struct {
	data map[string]string
}

func (m *memSecretStore) Get(service, account string) (string, error) {
	return m.data[service+"/"+account], nil
}

func (m *memSecretStore) Set(service, account, value string) error {
	m.data[service+"/"+account] = value
	return nil
}

func (m *memSecretStore) Delete(service, account string) error {
	delete(m.data, service+"/"+account)
	return nil
}

func TestPlatformSecretStore(t *testing.T) {
	saved := platformSecrets
	defer func() { platformSecrets = saved }()
	platformSecrets = &memSecretStore{data: map[string]string{}}

	k := Secret("llm.openai", "AudioSmith OpenAI API key")
	if err := k.Set("  sk-test-123  "); err != nil {
		t.Fatal(err)
	}
	got, err := k.Get()
	if err != nil || got != "sk-test-123" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	t.Setenv("HF_TOKEN", "")
	t.Setenv("HF_TOKEN_PATH", filepath.Join(t.TempDir(), "missing"))
	if tok, src := Token(k); tok != "sk-test-123" || src != TokenKeychain {
		t.Fatalf("Token = %q %q", tok, src)
	}
	if err := k.Set(""); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Get(); err != nil || got != "" {
		t.Fatalf("after delete: %q, %v", got, err)
	}
}
