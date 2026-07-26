package cli

import (
	"github.com/spf13/cobra"
)

// barf's every other command reads Vault only (see wireSecrets); minting a
// brand new host's or WireGuard link's first secret is a deliberate,
// separate opt-in, not something generate/diff/deploy do implicitly. See
// mintHostSecret (deps.go) / wireMintHostSecret (wire.go).

func newSecretsCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Vault secret maintenance (the one place barf writes to Vault)",
		Long: "secrets is barf's only Vault write path. Every other command —\n" +
			"generate, diff, deploy included — only ever reads secrets, and\n" +
			"refuses rather than silently minting one a new host or WireGuard\n" +
			"link needs but does not have yet.",
	}
	cmd.AddCommand(newSecretsMintCmd(o))
	return cmd
}

func newSecretsMintCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mint <host> <key>",
		Short: "Create a per-host secret if it does not already exist yet",
		Long: "mint fetches host-<host>'s <key> field from Vault, creating it with\n" +
			"a fresh random value (Python's secrets.token_urlsafe(16) equivalent)\n" +
			"if the field is missing. An existing value is returned unchanged,\n" +
			"never overwritten. The value is printed to stdout: treat the output\n" +
			"like any other secret material.\n\n" +
			"This is the fix for a fresh `network.yml` host or WireGuard link\n" +
			"failing every command until its secret is hand-minted: run this\n" +
			"once (e.g. `barf secrets mint sea1-vpn-9 admin-password`), then\n" +
			"generate/diff/deploy read it like any other host's.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecretsMint(o, args[0], args[1])
		},
	}
	return cmd
}

func runSecretsMint(o *Options, hostname, key string) error {
	value, err := mintHostSecret(hostname, key)
	if err != nil {
		return err
	}
	o.printf("%s\n", value)
	return nil
}
