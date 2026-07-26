package scope

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
)

// ManagedUsername is the one account barf owns on EOS devices.
const ManagedUsername = "admin"

// maxSSHKeys is all EOS models per user: one primary, one secondary.
const maxSSHKeys = 2

// EOSSections are the `show running-config all section <name>` blocks the
// managed scope lives in — and the only config barf ever reads from an
// EOS device for diffing. The full running-config is never fetched: it is
// a megabyte of config barf does not own, and comparing against it is
// what produced the nonsense `+4 -32859` summaries.
var EOSSections = []string{
	"username",
	"enable",
	"management api http-commands",
}

// EOS compares the Arista managed slice: the admin user with its
// ssh-keys, the enable password, and the eAPI block.
type EOS struct{}

var _ Comparer = EOS{}

// EOSManagedState is the device's current managed-scope config, parsed.
// A zero-value field means the item is absent from the device.
type EOSManagedState struct {
	// AdminLine is the full `username admin ... secret ...` line.
	AdminLine string
	// AdminHash is the crypt hash from AdminLine.
	AdminHash string
	// SSHKey is the primary ssh-key value.
	SSHKey string
	// SSHKeySecondary is the secondary ssh-key value.
	SSHKeySecondary string
	// EnableLine is the full `enable password ...` line.
	EnableLine string
	// EnableHash is the crypt hash from EnableLine.
	EnableHash string

	// EAPIEnabled is the api block's own shutdown state; nil when the
	// block is absent entirely.
	EAPIEnabled *bool
	// EAPIHTTPS is whether the block carries an https protocol line.
	EAPIHTTPS *bool
	// EAPIVRFEnabled is the shutdown state of the host's eapi_vrf
	// sub-block; nil when that VRF is not configured under the block.
	EAPIVRFEnabled *bool
}

// Compare fetches the managed sections and reports what has drifted.
//
// The desired state is recomputed from Vault + network.yml rather than
// taken from a pre-rendered string, because passwords must be compared by
// *verifying* the secret against whatever hash the device holds, not by
// comparing hash text: sha512-crypt salts differ per render seed, so a
// hand-set device with the right password would otherwise look drifted
// forever and get its credentials rewritten on every deploy.
func (EOS) Compare(ctx context.Context, in Input) (Result, error) {
	if in.Host == nil {
		return Result{}, fmt.Errorf("scope: eos: nil host")
	}
	if in.Network == nil {
		return Result{}, fmt.Errorf("scope: %s: no network metadata (ssh keys live there)", in.Host.Hostname)
	}
	if in.Reader == nil {
		return Result{}, fmt.Errorf("scope: %s: no device reader", in.Host.Hostname)
	}

	text, err := in.Reader.RunningConfigSections(ctx, EOSSections...)
	if err != nil {
		return Result{}, err
	}
	state := ParseEOSManagedState(text, in.Host.EAPIVRF)

	drift, err := EOSDrift(in.Host, in.Network.Global, in.Secrets, state)
	if err != nil {
		return Result{}, err
	}
	return buildResult(drift, in.Redact), nil
}

// -- parsing ----------------------------------------------------------

var (
	// The ssh-key patterns must be tried before the secret pattern: a
	// `username admin ssh-key ...` line can contain the word "secret"
	// inside a key comment, and the ordering is what the Python
	// implementation relies on too.
	eosSSHKeySecondaryRe = regexp.MustCompile(`^username ` + ManagedUsername + ` ssh-key secondary (.+)$`)
	eosSSHKeyRe          = regexp.MustCompile(`^username ` + ManagedUsername + ` ssh-key (.+)$`)
	eosAdminRe           = regexp.MustCompile(`^username ` + ManagedUsername + ` .*secret (?:sha512 )?(\S+)$`)
	eosEnableRe          = regexp.MustCompile(`^enable password (?:sha512 )?(\S+)$`)
	eosVRFRe             = regexp.MustCompile(`^vrf (\S+)$`)
)

// ParseEOSManagedState pulls the managed items out of the concatenated
// section output. Anything it does not recognise is ignored — the
// `section` filter is generous (the `enable` section alone drags in a few
// hundred `default snmp-server enable traps ...` lines), and unmanaged
// config must stay invisible.
func ParseEOSManagedState(text, eapiVRF string) EOSManagedState {
	state := EOSManagedState{}
	parseEOSAPISection(text, eapiVRF, &state)

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if m := eosSSHKeySecondaryRe.FindStringSubmatch(line); m != nil {
			state.SSHKeySecondary = strings.TrimSpace(m[1])
			continue
		}
		if m := eosSSHKeyRe.FindStringSubmatch(line); m != nil {
			state.SSHKey = strings.TrimSpace(m[1])
			continue
		}
		if m := eosAdminRe.FindStringSubmatch(line); m != nil {
			state.AdminLine = line
			state.AdminHash = m[1]
			continue
		}
		if m := eosEnableRe.FindStringSubmatch(line); m != nil {
			state.EnableLine = line
			state.EnableHash = m[1]
		}
	}
	return state
}

// parseEOSAPISection reads the `management api http-commands` block.
//
// `show running-config all` prints default state explicitly, so both
// `shutdown` and `no shutdown` appear literally — presence of a line is
// not evidence of intent, its polarity is. The block's own shutdown line
// sits at one indent level; per-VRF shutdown lines sit inside their
// `vrf <name>` sub-block, so a bare `no shutdown` means something
// different depending on which sub-block is open.
func parseEOSAPISection(text, eapiVRF string, state *EOSManagedState) {
	inBlock := false
	currentVRF := ""

	for _, raw := range strings.Split(text, "\n") {
		stripped := strings.TrimSpace(raw)
		if strings.HasPrefix(stripped, "management api http-commands") {
			inBlock = true
			currentVRF = ""
			continue
		}
		if inBlock && raw != "" && !isSpace(raw[0]) {
			// An un-indented line: the section ended.
			inBlock = false
			currentVRF = ""
		}
		if !inBlock {
			continue
		}

		if m := eosVRFRe.FindStringSubmatch(stripped); m != nil {
			currentVRF = m[1]
			continue
		}
		// `protocol https port 443` and `protocol https ssl profile eapi`
		// both count; `no protocol http port 80` deliberately does not.
		if strings.HasPrefix(stripped, "protocol https") {
			state.EAPIHTTPS = boolPtr(true)
			continue
		}
		switch stripped {
		case "no shutdown":
			if currentVRF == "" {
				state.EAPIEnabled = boolPtr(true)
			} else if currentVRF == eapiVRF {
				state.EAPIVRFEnabled = boolPtr(true)
			}
		case "shutdown":
			if currentVRF == "" {
				state.EAPIEnabled = boolPtr(false)
			} else if currentVRF == eapiVRF {
				state.EAPIVRFEnabled = boolPtr(false)
			}
		}
	}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' }

func boolPtr(v bool) *bool { return &v }

func isTrue(v *bool) bool { return v != nil && *v }

// -- drift ------------------------------------------------------------

// EOSHashMatches reports whether password is the secret behind a device's
// sha512-crypt hash.
//
// This is the whole point of the scoped comparison: the device's salt is
// whatever it was hashed with, ours is derived from the hostname, so the
// hash *text* differs even when the password is identical. Verifying
// instead of comparing is what stops barf rewriting the credentials of an
// adopted device on every deploy.
func EOSHashMatches(password, deviceHash string) bool {
	if deviceHash == "" || !strings.HasPrefix(deviceHash, "$6$") {
		return false
	}
	return sha512_crypt.New().Verify(deviceHash, []byte(password)) == nil
}

// EOSDrift returns the out-of-sync managed items, in the fixed order the
// Python implementation reports them: admin user, primary ssh-key,
// secondary ssh-key, enable password, eAPI block, eAPI VRF.
func EOSDrift(h *model.Host, global model.GlobalMeta, secrets SecretSource, state EOSManagedState) ([]Change, error) {
	if secrets == nil {
		return nil, fmt.Errorf("scope: %s: no secret source", h.Hostname)
	}
	adminPassword, err := secrets.HostSecret(h.Hostname, "admin-password")
	if err != nil {
		return nil, err
	}
	enablePassword, err := secrets.HostSecret(h.Hostname, "enable-password")
	if err != nil {
		return nil, err
	}

	desired, err := render.EOSManagedCommands(h, global, secrets)
	if err != nil {
		return nil, err
	}
	desiredAdmin := desired[0]
	desiredEnable := ""
	for _, line := range desired {
		if strings.HasPrefix(line, "enable password") {
			desiredEnable = line
			break
		}
	}
	keys := eosSSHKeys(global)

	var drift []Change

	adminOK := state.AdminLine != "" &&
		strings.Contains(state.AdminLine, "privilege 15") &&
		EOSHashMatches(adminPassword, state.AdminHash)
	if !adminOK {
		drift = append(drift, Change{Device: state.AdminLine, Desired: desiredAdmin})
	}

	if len(keys) > 0 && state.SSHKey != keys[0] {
		device := ""
		if state.SSHKey != "" {
			device = fmt.Sprintf("username %s ssh-key %s", ManagedUsername, state.SSHKey)
		}
		drift = append(drift, Change{
			Device:  device,
			Desired: fmt.Sprintf("username %s ssh-key %s", ManagedUsername, keys[0]),
		})
	}

	if len(keys) == maxSSHKeys && state.SSHKeySecondary != keys[1] {
		device := ""
		if state.SSHKeySecondary != "" {
			device = fmt.Sprintf("username %s ssh-key secondary %s", ManagedUsername, state.SSHKeySecondary)
		}
		drift = append(drift, Change{
			Device:  device,
			Desired: fmt.Sprintf("username %s ssh-key secondary %s", ManagedUsername, keys[1]),
		})
	}

	if !EOSHashMatches(enablePassword, state.EnableHash) {
		drift = append(drift, Change{Device: state.EnableLine, Desired: desiredEnable})
	}

	if !isTrue(state.EAPIEnabled) || !isTrue(state.EAPIHTTPS) {
		device := ""
		if state.EAPIEnabled != nil {
			device = "management api http-commands: shutdown or non-https"
		}
		drift = append(drift, Change{
			Device:  device,
			Desired: "management api http-commands / protocol https port 443 / no shutdown",
		})
	}

	if h.EAPIVRF != "" && !isTrue(state.EAPIVRFEnabled) {
		device := ""
		if state.EAPIVRFEnabled != nil {
			device = fmt.Sprintf("management api http-commands vrf %s: shutdown", h.EAPIVRF)
		}
		drift = append(drift, Change{
			Device:  device,
			Desired: fmt.Sprintf("management api http-commands / vrf %s / no shutdown", h.EAPIVRF),
		})
	}

	return drift, nil
}

// eosSSHKeys is the trimmed, non-empty ssh key list. An over-long list is
// not rejected here: render.EOSManagedCommands already fails on it, and
// EOSDrift calls that first.
func eosSSHKeys(global model.GlobalMeta) []string {
	var keys []string
	for _, key := range global.SSHKeys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
}
