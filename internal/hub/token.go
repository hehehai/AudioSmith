package hub

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hehehai/AudioSmith/internal/logs"
)

// The Hugging Face token, for gated repos. It is a secret: it lives in the
// macOS Keychain (never in settings or logs), is sent only to the official
// endpoint (Options.Official), and is looked up in this order:
//
//	$HF_TOKEN                       development, CI
//	the ActNow family's shared one  when the user shares it (TokenFrom's shared: the app injects it)
//	the Keychain                    AudioSmith's own item, what Settings → Models saves
//	huggingface_hub's token file    $HF_TOKEN_PATH, else $HF_HOME/token, else ~/.cache/huggingface/token
//
// (the family spec v1.0.4 §8.4 order; this package knows nothing of the
// family: the app hands TokenFrom the shared token's reader).
//
// Every function here may block (the Keychain runs /usr/bin/security):
// call them off the main thread.

// TokenSource names where a token came from.
type TokenSource string

const (
	TokenNone     TokenSource = ""
	TokenEnv      TokenSource = "env"
	TokenShared   TokenSource = "shared" // the ActNow family's shared token
	TokenKeychain TokenSource = "keychain"
	TokenCLI      TokenSource = "file" // huggingface-cli login
)

// ErrNoKeychain is returned off macOS, where there is no Keychain to use.
var ErrNoKeychain = errors.New("no keychain on this system")

// Keychain is a generic password item of the login keychain (or of the
// keychain file File, for tests). AudioSmith keeps every secret under
// Service "AudioSmith", one Account per secret: "huggingface-token" (the
// HF token, DefaultKeychain), "llm.<provider>" (LLM API keys),
// "mt.<service>" (translation API keys); see Secret. Label is what
// Keychain Access shows; "" uses "AudioSmith <account>".
type Keychain struct{ Service, Account, Label, File string }

// KeychainService is the Keychain service of AudioSmith's secrets.
const KeychainService = "AudioSmith"

// Secret is the Keychain item of one of AudioSmith's secrets, e.g.
// Secret("llm.openai", "AudioSmith OpenAI API key").
func Secret(account, label string) Keychain {
	return Keychain{Service: KeychainService, Account: account, Label: label}
}

// with appends the keychain file to a security command, when there is one.
func (k Keychain) with(args ...string) []string {
	if k.File != "" {
		args = append(args, k.File)
	}
	return args
}

// DefaultKeychain is where AudioSmith keeps the HF token.
var DefaultKeychain = Secret("huggingface-token", "AudioSmith Hugging Face token")

// security runs /usr/bin/security with args, feeding it stdin.
var security = func(stdin string, args ...string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, ErrNoKeychain
	}
	cmd := exec.Command("/usr/bin/security", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("security: %s", msg)
		}
		return out, err
	}
	return out, nil
}

// secretStore is an optional platform credential store (Windows uses
// mygo.Secrets; macOS keeps /usr/bin/security for existing Keychain items).
type secretStore interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
	Delete(service, account string) error
}

// Get returns the token saved, "" when there is none.
func (k Keychain) Get() (string, error) {
	if platformSecrets != nil && k.File == "" {
		tok, err := platformSecrets.Get(k.Service, k.Account)
		if err != nil {
			if errors.Is(err, ErrNoKeychain) {
				return "", nil
			}
			return "", err
		}
		tok = strings.TrimSpace(tok)
		if tok != "" {
			logs.AddSecret(tok) // never in a log line (#108)
		}
		return tok, nil
	}
	out, err := security("", k.with("find-generic-password", "-s", k.Service, "-a", k.Account, "-w")...)
	if err != nil {
		if errors.Is(err, ErrNoKeychain) || strings.Contains(err.Error(), "could not be found") || isExit(err, 44) {
			return "", nil
		}
		return "", err
	}
	tok := strings.TrimSpace(string(out))
	logs.AddSecret(tok) // never in a log line (#108)
	return tok, nil
}

// Set saves token, replacing what was saved. The token goes to security
// on its standard input (interactive mode), not its arguments, so that
// other processes cannot read it from the process list.
func (k Keychain) Set(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return k.Delete()
	}
	if strings.ContainsAny(token, "\"\\\n\r") {
		return errors.New("the token has characters a Hugging Face token never has")
	}
	logs.AddSecret(token)
	if platformSecrets != nil && k.File == "" {
		return platformSecrets.Set(k.Service, k.Account, token)
	}
	label := k.Label
	if label == "" {
		label = "AudioSmith " + k.Account
	}
	cmd := fmt.Sprintf("add-generic-password -U -s %q -a %q -l %q -w \"%s\"", k.Service, k.Account, label, token)
	if k.File != "" {
		cmd += fmt.Sprintf(" %q", k.File)
	}
	cmd += "\n"
	_, err := security(cmd, "-i")
	return err
}

// Delete removes the saved token; none saved is not an error.
func (k Keychain) Delete() error {
	if platformSecrets != nil && k.File == "" {
		err := platformSecrets.Delete(k.Service, k.Account)
		if errors.Is(err, ErrNoKeychain) {
			return nil
		}
		return err
	}
	_, err := security("", k.with("delete-generic-password", "-s", k.Service, "-a", k.Account)...)
	if err != nil && (errors.Is(err, ErrNoKeychain) || strings.Contains(err.Error(), "could not be found") || isExit(err, 44)) {
		return nil
	}
	return err
}

func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

// TokenFile is huggingface_hub's token file.
func TokenFile() string {
	if p := os.Getenv("HF_TOKEN_PATH"); p != "" {
		return p
	}
	if d := os.Getenv("HF_HOME"); d != "" {
		return filepath.Join(d, "token")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "huggingface", "token")
}

// Token finds the token and where it came from (see above), without a
// shared one; "" when none.
func Token(k Keychain) (string, TokenSource) { return TokenFrom(nil, k) }

// TokenFrom finds the token and where it came from (see above); shared
// reads the family's shared token (nil = none; an error counts as none).
func TokenFrom(shared func() (string, error), k Keychain) (string, TokenSource) {
	if t := strings.TrimSpace(os.Getenv("HF_TOKEN")); t != "" {
		return t, TokenEnv
	}
	if shared != nil {
		if t, err := shared(); err == nil && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t), TokenShared
		}
	}
	if t, err := k.Get(); err == nil && t != "" {
		return t, TokenKeychain
	}
	if data, err := os.ReadFile(TokenFile()); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, TokenCLI
		}
	}
	return "", TokenNone
}

// MaskToken shows a token as the UI may: "hf_…wxyz".
func MaskToken(t string) string {
	if len(t) <= 8 {
		return strings.Repeat("•", len(t))
	}
	return t[:3] + "…" + t[len(t)-4:]
}
