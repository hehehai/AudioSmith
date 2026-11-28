//go:build windows

package hub

import (
	"errors"

	"github.com/egoist/mygo"
)

// On Windows, AudioSmith stores secrets in Windows Credential Manager via
// mygo.Secrets (DPAPI-encrypted per user).
var platformSecrets secretStore = mygoSecretStore{}

type mygoSecretStore struct{}

func (mygoSecretStore) Get(service, account string) (string, error) {
	b, err := mygo.Secrets.Service(service).Get(account)
	if err != nil {
		if errors.Is(err, mygo.ErrSecretNotFound) {
			return "", nil
		}
		if errors.Is(err, errors.ErrUnsupported) {
			return "", ErrNoKeychain
		}
		return "", err
	}
	return string(b), nil
}

func (mygoSecretStore) Set(service, account, value string) error {
	err := mygo.Secrets.Service(service).Set(account, []byte(value))
	if errors.Is(err, errors.ErrUnsupported) {
		return ErrNoKeychain
	}
	return err
}

func (mygoSecretStore) Delete(service, account string) error {
	err := mygo.Secrets.Service(service).Delete(account)
	if errors.Is(err, mygo.ErrSecretNotFound) || errors.Is(err, errors.ErrUnsupported) {
		return nil
	}
	return err
}
