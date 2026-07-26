package render_test

import (
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// The goldens' hashes came from Python passlib with rounds=5000 and a
// 16-hex-char salt; pin salt derivation and output format.
func TestDeterministicSHA512(t *testing.T) {
	tests := []struct {
		password string
		saltSeed string
		want     string
	}{
		{
			password: "SECRET-host-fmt2-cor-r-140752-1-admin-password",
			saltSeed: "fmt2-cor-r-140752-1:admin",
			want: "$6$7e5172a8298b8071$GR7TqWJOvFpoBHyFPyr0ow9wRIAeyP/s.Oz0ziHjAG9XqL." +
				"ws6I.KPKdoj8Okna93VerBK3Ed0u4Nl9XCfR5U1",
		},
		{
			password: "SECRET-host-fmt2-cor-r-140752-1-enable-password",
			saltSeed: "fmt2-cor-r-140752-1:enable",
			want: "$6$1e973627f01bc1a4$t7ESnNbxvH/1EOboHvgyopk7fdEOYH0Rf0jZAa8KJ2kkcuaR" +
				"610jaurWFEOY1DrepClPuryexWN8ypWzePZlz0",
		},
	}

	for _, test := range tests {
		got, err := render.DeterministicSHA512(test.password, test.saltSeed)
		if err != nil {
			t.Fatalf("DeterministicSHA512: %v", err)
		}
		if got != test.want {
			t.Errorf("hash(%q, %q) =\n  %q\nwant\n  %q", test.password, test.saltSeed, got, test.want)
		}
	}
}

// EOS models one primary and one secondary key per user.
func TestEOSRejectsTooManySSHKeys(t *testing.T) {
	host := &model.Host{Hostname: "sw-1", DeviceType: "eos", Role: "core"}
	network := &model.Network{
		Global: model.GlobalMeta{SSHKeys: []string{
			"ssh-ed25519 AAAA a", "ssh-ed25519 BBBB b", "ssh-ed25519 CCCC c",
		}},
		Hosts: []model.Host{*host},
	}

	_, err := vendor.Render(&network.Hosts[0], network, fakeSecrets{})
	if err == nil || !strings.Contains(err.Error(), "secondary ssh-key") {
		t.Fatalf("expected an ssh-key limit error, got %v", err)
	}
}

func TestEOSOmitsVRFWhenUnset(t *testing.T) {
	network := &model.Network{
		Hosts: []model.Host{{Hostname: "sw-1", DeviceType: "eos", Role: "core"}},
	}
	got, err := vendor.Render(&network.Hosts[0], network, fakeSecrets{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "vrf ") {
		t.Errorf("unexpected vrf stanza:\n%s", got)
	}
	if !strings.HasSuffix(got, "no shutdown\n") {
		t.Errorf("config should end with the eAPI enable:\n%s", got)
	}
}

// An unported vendor must fail loudly, not render something plausible.
func TestUnknownDeviceTypeIsReported(t *testing.T) {
	network := &model.Network{
		Hosts: []model.Host{{Hostname: "x", DeviceType: "junos", Role: "vpn"}},
	}
	if _, err := vendor.Render(&network.Hosts[0], network, fakeSecrets{}); err == nil {
		t.Fatal("expected an error for an unregistered device type")
	}
}

// Only the vpn role is ported for vyos; a core-role host must not render one.
func TestVyOSRejectsNonVPNRole(t *testing.T) {
	network := &model.Network{
		Hosts: []model.Host{{Hostname: "x", DeviceType: "vyos", Role: "core"}},
	}
	_, err := vendor.Render(&network.Hosts[0], network, fakeSecrets{})
	if err == nil || !strings.Contains(err.Error(), `no config for role "core"`) {
		t.Fatalf("expected a role error, got %v", err)
	}
}

// Implements only the frozen SecretSource: renders needing a Vault attribute
// must fail rather than emit an empty key.
type noVaultSecrets struct{}

func (noVaultSecrets) HostSecret(hostname, key string) (string, error) {
	return "SECRET-host-" + hostname + "-" + key, nil
}

func TestMissingVaultSourceIsReported(t *testing.T) {
	network := loadFleet(t)
	host, _ := network.Host("fmt2-vpn-spine-1")

	_, err := vendor.Render(host, network, noVaultSecrets{})
	if err == nil || !strings.Contains(err.Error(), "vault key") {
		t.Fatalf("expected a vault-source error, got %v", err)
	}
}
