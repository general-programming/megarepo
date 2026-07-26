package render_test

import (
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/render"
)

// fakeSecrets is the Go twin of the Python golden harness fakes
// (tests/test_golden_render.py, tests/conftest.py): host secrets render
// as SECRET-host-<hostname>-<key>, VaultSecrets attribute lookups as
// VAULT-<key>, and WireGuard keypairs as PUB-<path>/PRIV-<path>.
type fakeSecrets struct{}

func (fakeSecrets) HostSecret(hostname, key string) (string, error) {
	return fmt.Sprintf("SECRET-host-%s-%s", hostname, key), nil
}

func (fakeSecrets) VaultSecret(key string) (string, error) {
	return "VAULT-" + key, nil
}

func (fakeSecrets) WireguardKeypair(path string) (render.Keypair, error) {
	return render.Keypair{Public: "PUB-" + path, Private: "PRIV-" + path}, nil
}
