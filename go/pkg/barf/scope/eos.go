package scope

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/general-programming/megarepo/go/common/pytext"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/vyosconfig"
)

// ManagedUsername is the one account barf owns on EOS devices.
const ManagedUsername = "admin"

// maxSSHKeys is all EOS supports per user: one primary, one secondary.
// Aliased, not redeclared, so limit and validation cannot drift apart.
const maxSSHKeys = render.MaxSSHKeys

// EOSSections are the only config barf reads from an EOS device for diffing;
// fetching the full running-config produced nonsense `+4 -32859` summaries.
var EOSSections = []string{
	"username",
	"enable",
	"management api http-commands",
}

// EOS compares the Arista managed slice: the admin user with its
// ssh-keys, the enable password, and the eAPI block.
type EOS struct{}

var _ Comparer = EOS{}

// EOSManagedState is the device's parsed managed-scope config; a zero-value
// field means the item is absent from the device.
type EOSManagedState struct {
	// AdminLine is the full `username admin ...` line, AdminHash its crypt hash.
	AdminLine string
	AdminHash string

	SSHKey          string
	SSHKeySecondary string

	// EnableLine is the full `enable password ...` line, EnableHash its hash.
	EnableLine string
	EnableHash string

	// EAPIEnabled is the api block's own shutdown state; nil when absent.
	EAPIEnabled *bool
	// EAPIHTTPS is whether the block carries an https protocol line.
	EAPIHTTPS *bool
	// EAPIVRFEnabled is the shutdown state of the host's eapi_vrf sub-block.
	EAPIVRFEnabled *bool
}

// Compare fetches the managed sections and reports what has drifted.
// Desired state is recomputed from Vault + network.yml so passwords can be
// verified against the device's hash rather than compared as hash text:
// salts differ per render, so text comparison drifts forever.
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
	return buildResult(drift, in.ShowSecrets), nil
}

// -- parsing ----------------------------------------------------------

var (
	// The ssh-key patterns must be tried before the secret pattern: a
	// `username admin ssh-key ...` line can contain "secret" in a comment.
	eosSSHKeySecondaryRe = regexp.MustCompile(`^username ` + ManagedUsername + ` ssh-key secondary (.+)$`)
	eosSSHKeyRe          = regexp.MustCompile(`^username ` + ManagedUsername + ` ssh-key (.+)$`)
	eosAdminRe           = regexp.MustCompile(`^username ` + ManagedUsername + ` .*secret (?:sha512 )?(\S+)$`)
	eosEnableRe          = regexp.MustCompile(`^enable password (?:sha512 )?(\S+)$`)
	eosVRFRe             = regexp.MustCompile(`^vrf (\S+)$`)
)

// ParseEOSManagedState pulls the managed items out of the concatenated section
// output. Anything unrecognised is ignored: the `section` filter is generous
// (`enable` alone drags in hundreds of unrelated lines).
func ParseEOSManagedState(text, eapiVRF string) EOSManagedState {
	state := EOSManagedState{}
	parseEOSAPISection(text, eapiVRF, &state)

	for _, raw := range pytext.SplitLines(text) {
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
// `show running-config all` prints defaults explicitly, so polarity is the
// evidence, not presence; a bare `no shutdown` means the block or the VRF
// depending on which `vrf <name>` sub-block is open.
func parseEOSAPISection(text, eapiVRF string, state *EOSManagedState) {
	inBlock := false
	currentVRF := ""

	for _, raw := range pytext.SplitLines(text) {
		stripped := strings.TrimSpace(raw)
		if strings.HasPrefix(stripped, "management api http-commands") {
			inBlock = true
			currentVRF = ""
			continue
		}
		if inBlock && raw != "" && !startsIndented(raw) {
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

// startsIndented reports whether line is still inside the open config block.
// Python asks `line[0].isspace()`; unicode.IsSpace is that set except the C0
// file/group/record/unit separators (0x1c-0x1f), hence the extra range —
// without it the eAPI block closes early and barf reports spurious drift.
func startsIndented(line string) bool {
	if line == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(line)
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

func boolPtr(v bool) *bool { return &v }

func isTrue(v *bool) bool { return v != nil && *v }

// -- drift ------------------------------------------------------------

// EOSHashMatches reports whether password is the secret behind a device's
// sha512-crypt hash. Salts differ, so verifying rather than comparing hash
// text is what stops barf rewriting an adopted device's credentials.
//
// deviceHash is device-reported and so untrusted: a hostile or corrupted
// `$6$rounds=999999999$...` would otherwise reach sha512_crypt.Verify
// directly, which does the work — minutes of CPU per probe. Sharing
// vyosconfig.WellFormedSHA512Crypt keeps this bound identical to the VyOS
// side rather than a second copy that could drift out of range. A malformed
// or out-of-bounds hash reports no match, matching Python passlib's
// ValueError -> False for verify(), so it shows as drift rather than hanging.
func EOSHashMatches(password, deviceHash string) bool {
	if deviceHash == "" || !strings.HasPrefix(deviceHash, "$6$") || !vyosconfig.WellFormedSHA512Crypt(deviceHash) {
		return false
	}
	return sha512_crypt.New().Verify(deviceHash, []byte(password)) == nil
}

// EOSDrift returns the out-of-sync managed items in Python's fixed order:
// admin user, ssh-key, secondary ssh-key, enable password, eAPI block, VRF.
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
	// render.EOSSSHKeys, not a local copy: the drift report must use exactly
	// the list `generate` would emit, refusals included.
	keys, err := render.EOSSSHKeys(h, global)
	if err != nil {
		return nil, err
	}

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
