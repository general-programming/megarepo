//go:build live

// Live, read-only smoke tests against real fleet devices. Excluded from
// the normal test run; run with:
//
//	go test -tags live -v ./go/barf/device/
//
// Requires a Vault token in the environment (secrets come via the `vault`
// CLI). Every command issued is a `show`; nothing here changes a device.
package device

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// vaultCLI resolves secrets by shelling out to the vault binary.
type vaultCLI struct{}

func (vaultCLI) HostSecret(hostname, key string) (string, error) {
	out, err := exec.Command("vault", "kv", "get",
		"-mount=cluster-secrets", "-field="+key, "host-"+hostname).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (vaultCLI) GlobalSecret(name string) (string, error) {
	out, err := exec.Command("vault", "kv", "get",
		"-mount=secret", "-field=secret", name).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func liveOptions() Options {
	return Options{
		Secrets:            vaultCLI{},
		GlobalSecrets:      vaultCLI{},
		InsecureSkipVerify: true, // fleet devices run self-signed certs
		Timeout:            15 * time.Second,
	}
}

func TestLiveEOS(t *testing.T) {
	host := &model.Host{
		Hostname:   "fmt2-cor-r-140752-1",
		DeviceType: "eos",
		Address:    addrPtr("10.65.67.1/32"),
		Interfaces: []model.Interface{
			{Name: "Management1", Management: true, Addresses: []model.Address{addr("10.255.1.1/32")}},
		},
	}
	reader, err := New(host, liveOptions())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	status, err := reader.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EOS status: version=%q uptime=%q model=%q", status.Version, status.Uptime, status.Model)
	if status.Version == "" || status.Model == "" || status.Uptime == "" {
		t.Error("empty status field")
	}

	config, err := reader.RunningConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EOS running-config: %d bytes, %d lines, first line %q",
		len(config), strings.Count(config, "\n"), strings.SplitN(config, "\n", 2)[0])
	if !strings.Contains(config, "hostname") {
		t.Error("running-config has no hostname line")
	}

	section, err := reader.(*EOSReader).RunningConfigSection(ctx, "management api http-commands")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EOS eAPI section:\n%s", section)
}

func TestLiveVyOS(t *testing.T) {
	hosts := []*model.Host{
		{
			Hostname:   "fmt2-vpn-spine-1",
			DeviceType: "vyos",
			Interfaces: []model.Interface{
				{Name: "dum0", Management: true, Addresses: []model.Address{addr("10.255.2.1/32")}},
			},
		},
		{
			Hostname:   "fmt2-vpn-leaf-1",
			DeviceType: "vyos",
			Interfaces: []model.Interface{
				{Name: "dum0", Management: true, Addresses: []model.Address{addr("10.255.2.3/32")}},
			},
		},
	}

	for _, host := range hosts {
		t.Run(host.Hostname, func(t *testing.T) {
			reader, err := New(host, liveOptions())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			status, err := reader.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("VyOS status: version=%q uptime=%q model=%q", status.Version, status.Uptime, status.Model)

			images, err := reader.(*VyOSReader).SystemImages(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("VyOS images: %+v", images)

			tree, err := reader.(*VyOSReader).RetrieveConfig(ctx)
			if err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(tree))
			for key := range tree {
				keys = append(keys, key)
			}
			t.Logf("VyOS config top-level nodes: %v", keys)
		})
	}
}
