package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func renderHost(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	r, err := newRenderer(h.DeviceType)
	if err != nil {
		return "", err
	}
	return r.Render(h, n, s)
}

func newGenerateCmd(o *Options) *cobra.Command {
	var outputDir string

	cmd := &cobra.Command{
		Use:   "generate <hosts...|all>",
		Short: "Render configs for the selected hosts into an output directory",
		Long: "generate renders each selected host's config into\n" +
			"<output>/<role>/<hostname>, plus a cloud-init twin under\n" +
			"<output>/<role>/cloud_init/<hostname>. It touches no devices.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(cmd.Context(), o, args, outputDir)
		},
	}
	cmd.Flags().StringVarP(&outputDir, "output", "o", "output", "directory to write rendered configs into")
	// NetBox-sourced generators (see generatedns.go).
	cmd.AddCommand(newGenerateDNSCmd(o), newGenerateDHCPCmd(o))
	return cmd
}

func runGenerate(ctx context.Context, o *Options, targets []string, outputDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	net, hosts, err := o.loadTargets(targets)
	if err != nil {
		return err
	}
	hosts = filterHosts(hosts, func(h *model.Host) bool { return isTemplatable(h.DeviceType) })
	if len(hosts) == 0 {
		return fmt.Errorf("no templatable devices selected")
	}

	secrets, err := newSecrets()
	if err != nil {
		return fmt.Errorf("secret backend unavailable: %w", err)
	}
	prefetchLinkKeys(ctx, secrets, hosts, net.Links)

	for _, h := range hosts {
		text, err := renderHost(h, net, secrets)
		if err != nil {
			return fmt.Errorf("%s: %w", h.Hostname, err)
		}
		path, err := writeRenderedConfig(h, text, outputDir)
		if err != nil {
			return fmt.Errorf("%s: %w", h.Hostname, err)
		}
		o.printf("[%s] wrote %s\n", h.Hostname, path)
	}
	return nil
}

// Rendered configs carry live credentials, so the output tree is owner-only:
// 0700 directories, 0600 files. `output/` is gitignored only under
// projects/barf, so run from the repo root generate writes somewhere
// `git add -A` would stage; the mode is what has to be right here.
const (
	renderDirMode  os.FileMode = 0o700
	renderFileMode os.FileMode = 0o600
)

// writeRenderedConfig writes a rendered config and its cloud-init twin under
// outputDir, returning the main config file's path. Ported from
// barf/util/render.py write_rendered_config.
func writeRenderedConfig(h *model.Host, rendered, outputDir string) (string, error) {
	roleDir := filepath.Join(outputDir, h.Role)
	if err := os.MkdirAll(filepath.Join(roleDir, "cloud_init"), renderDirMode); err != nil {
		return "", err
	}
	// MkdirAll leaves an existing directory's mode alone and umask only
	// clears bits, so tighten explicitly.
	for _, dir := range []string{outputDir, roleDir, filepath.Join(roleDir, "cloud_init")} {
		if err := os.Chmod(dir, renderDirMode); err != nil {
			return "", err
		}
	}

	configPath := filepath.Join(roleDir, h.Hostname)
	if err := writeSecretFile(configPath, []byte(rendered)); err != nil {
		return "", err
	}

	var commands []string
	for _, line := range strings.Split(rendered, "\n") {
		if line != "" {
			commands = append(commands, line)
		}
	}
	body, err := yaml.Marshal(map[string]any{"vyos_config_commands": commands})
	if err != nil {
		return "", err
	}
	cloudInit := append([]byte("#cloud-config\n"), body...)
	if err := writeSecretFile(filepath.Join(roleDir, "cloud_init", h.Hostname), cloudInit); err != nil {
		return "", err
	}
	return configPath, nil
}

// writeSecretFile writes credential-bearing output owner-only, chmod'ing even
// on an existing file (os.WriteFile keeps the old mode) and despite umask.
func writeSecretFile(path string, body []byte) error {
	if err := os.WriteFile(path, body, renderFileMode); err != nil {
		return err
	}
	return os.Chmod(path, renderFileMode)
}
