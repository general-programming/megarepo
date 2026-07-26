package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// managedUsername is the one account barf owns on EOS devices.
const managedUsername = "admin"

// MaxSSHKeys is all EOS models support per user: one primary, one
// secondary. Exported alongside EOSSSHKeys so barf/scope shares the
// limit rather than redeclaring it.
const MaxSSHKeys = 2

// EOS renders the scoped Arista management slice: the admin user with
// its SSH keys, the enable password, and eAPI itself. Nothing else on
// the device is in scope (see projects/barf/barf/vendors/arista.py).
type EOS struct{}

// Render returns the managed-slice config as flat EOS CLI commands.
func (EOS) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	commands, err := EOSManagedCommands(h, n.Global, s)
	if err != nil {
		return "", err
	}
	return strings.Join(commands, "\n") + "\n", nil
}

// EOSManagedCommands is the full managed-scope config, as EOS CLI
// commands (the Python EosHost.managed_commands).
func EOSManagedCommands(h *model.Host, global model.GlobalMeta, s SecretSource) ([]string, error) {
	adminPassword, err := s.HostSecret(h.Hostname, "admin-password")
	if err != nil {
		return nil, err
	}
	enablePassword, err := s.HostSecret(h.Hostname, "enable-password")
	if err != nil {
		return nil, err
	}

	adminHash, err := DeterministicSHA512(adminPassword, h.Hostname+":"+managedUsername)
	if err != nil {
		return nil, err
	}
	enableHash, err := DeterministicSHA512(enablePassword, h.Hostname+":enable")
	if err != nil {
		return nil, err
	}

	keys, err := EOSSSHKeys(h, global)
	if err != nil {
		return nil, err
	}

	commands := []string{
		fmt.Sprintf("username %s privilege 15 role network-admin secret sha512 %s",
			managedUsername, adminHash),
	}
	if len(keys) > 0 {
		commands = append(commands, fmt.Sprintf("username %s ssh-key %s", managedUsername, keys[0]))
	}
	if len(keys) == MaxSSHKeys {
		commands = append(commands,
			fmt.Sprintf("username %s ssh-key secondary %s", managedUsername, keys[1]))
	}
	commands = append(commands, fmt.Sprintf("enable password sha512 %s", enableHash))

	// The eAPI slice: HTTPS on, plus the internal-facing VRF. `vrf
	// <name>` enters a sub-mode of the api block, so order is hierarchy.
	commands = append(commands,
		"management api http-commands",
		"protocol https port 443",
		"no shutdown",
	)
	if h.EAPIVRF != "" {
		commands = append(commands, "vrf "+h.EAPIVRF, "no shutdown")
	}

	return commands, nil
}

// EOSSSHKeys is the trimmed, non-empty ssh key list for h, rejecting a
// list EOS cannot express.
//
// Exported because barf/scope needs the identical list to decide whether
// a device's keys have drifted. It kept its own copy, which trimmed and
// filtered the same way but did NOT enforce MaxSSHKeys — so the strict
// answer and the lenient answer were one call apart, and `diff` could in
// principle report drift against a key list `generate` refuses to
// produce. That only stayed consistent because EOSDrift happened to call
// EOSManagedCommands (which validates) before building its own list; the
// invariant depended on statement order in another package. There is now
// one function and the strict behaviour is the only behaviour.
func EOSSSHKeys(h *model.Host, global model.GlobalMeta) ([]string, error) {
	var keys []string
	for _, key := range global.SSHKeys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	if len(keys) > MaxSSHKeys {
		return nil, fmt.Errorf("%s: EOS supports one primary and one secondary ssh-key"+
			" per user; got %d global_meta.ssh_keys", h.Hostname, len(keys))
	}
	return keys, nil
}

// DeterministicSHA512 is a sha512-crypt hash whose salt is a pure
// function of its inputs, so repeated renders are byte-identical.
//
// saltSeed is a stable per-host/per-purpose seed, e.g. "<hostname>:admin".
// sha512-crypt salts take [./0-9A-Za-z]; hex digest chars are a subset,
// and 16 chars is the format's maximum salt length.
func DeterministicSHA512(password, saltSeed string) (string, error) {
	digest := sha256.Sum256([]byte(saltSeed + ":" + password))
	salt := hex.EncodeToString(digest[:])[:16]

	crypter := sha512_crypt.New()
	// $6$rounds=5000$ is the format default, which passlib omits from
	// the output; the bare "$6$<salt>" setting produces the same string.
	return crypter.Generate([]byte(password), []byte("$6$"+salt))
}
