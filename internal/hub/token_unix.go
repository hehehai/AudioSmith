//go:build !windows

package hub

// On macOS, Keychain uses /usr/bin/security directly so existing Keychain
// items and ACLs remain untouched; on Linux, security returns ErrNoKeychain.
var platformSecrets secretStore
