package logs

import (
	"strings"
	"testing"
)

// TestRedact (#108, #100 L3): the family's rules, the Local API's keys,
// Twilio tokens by name, secrets read from the Keychain, the home folder.
func TestRedact(t *testing.T) {
	key := "as-" + strings.Repeat("aB3", 13) + "x"
	AddSecret("s3cr3t-provider-key-value")
	AddSecret("short") // too short to mask
	for in, want := range map[string]string{
		"loaded with key " + key:                         "loaded with key REDACTED",
		"auth_token=0123456789abcdef0123456789abcdef ok": "auth_token=REDACTED ok",
		"Twilio token: 0123456789abcdef0123456789abcdef": "Twilio token: REDACTED",
		"x-api-key: s3cr3t-provider-key-value":           "x-api-key: REDACTED",
		"provider said s3cr3t-provider-key-value":        "provider said REDACTED",
		"GET https://h.co/x?token=abc&q=1":               "GET https://h.co/x?token=REDACTED&q=1",
		"a short word stays":                             "a short word stays",
		"Authorization: Bearer abcdefghijkl":             "Authorization: Bearer REDACTED",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shortenHome("/Users/x/Library/Logs and /Users/xy/a and /Users/x", "/Users/x"); got != "~/Library/Logs and /Users/xy/a and ~" {
		t.Errorf("home: %q", got)
	}
}
