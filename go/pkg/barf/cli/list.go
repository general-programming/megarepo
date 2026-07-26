package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newListCmd(o *Options) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "list [hosts...]",
		Short: "List the hosts in network.yml",
		Long:  "list prints every host network.yml describes, with its device type, role and site.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(o, args, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a table")
	return cmd
}

// listJSON is one host in `barf list --json` output.
type listJSON struct {
	Device string `json:"device"`
	Type   string `json:"type"`
	Role   string `json:"role"`
	Site   string `json:"site"`
}

func runList(o *Options, targets []string, jsonOut bool) error {
	// `list` has no Python original; it keeps the historical "no args means
	// the whole fleet" behaviour, but opts in explicitly rather than relying
	// on resolveTargets to default an empty selection.
	_, hosts, err := o.loadTargets(defaultAllTargets(targets))
	if err != nil {
		return err
	}

	if jsonOut {
		out := make([]listJSON, len(hosts))
		for i, h := range hosts {
			out[i] = listJSON{Device: h.Hostname, Type: h.DeviceType, Role: h.Role, Site: h.Site}
		}
		enc := json.NewEncoder(o.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	rows := make([][]string, len(hosts))
	for i, h := range hosts {
		rows[i] = []string{h.Hostname, h.DeviceType, h.Role, h.Site}
	}
	printTable(o.Out, []string{"DEVICE", "TYPE", "ROLE", "SITE"}, rows)
	return nil
}
