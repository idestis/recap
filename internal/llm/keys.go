package llm

import (
	"net/url"

	"github.com/zalando/go-keyring"

	"github.com/idestis/recap/internal/config"
)

const keychainService = "recap"

// KeyAccount names the Keychain entry for a model endpoint: the provider plus
// host, so two OpenAI-compatible hosts keep separate keys.
func KeyAccount(m config.Model) string {
	base := m.BaseURL
	if base == "" {
		base = defaultBase[m.Provider]
	}
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return m.Provider + "@" + u.Host
	}
	return m.Provider
}

// Keys live in the macOS Keychain / Secret Service, never in config (SPEC D-15).
func SetKey(account, key string) error      { return keyring.Set(keychainService, account, key) }
func GetKey(account string) (string, error) { return keyring.Get(keychainService, account) }
func DeleteKey(account string) error        { return keyring.Delete(keychainService, account) }
