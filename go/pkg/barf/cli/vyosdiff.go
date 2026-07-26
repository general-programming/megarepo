package cli

import (
	"encoding/json"
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/vyosconfig"
)

// VyOS is diffed as a PATH SET, not a line set: `/retrieve` returns a JSON
// tree, so the two sides only line up once flattened into `set` path tuples.
// Path-set semantics also make a deploy safe — hashed passwords reconcile,
// ignored prefixes (hw-id) drop out, removals collapse into the fewest
// whole-node deletes. `barf diff` and `barf deploy` both come through here.
// Mirrors VyOSHost._config_diff / diff_config in barf/vendors/vyos.py.

// vyosPlan is a computed VyOS deploy: the diff, plus the reconciled running
// path set the delete collapse needs.
type vyosPlan struct {
	diff    vyosconfig.ConfigDiff
	running vyosconfig.Set
}

// planVyOS diffs a rendered config against running, normally a `/retrieve`
// JSON dump; a flat `set ...` listing is also accepted, so a config captured
// to a file can be planned against without a device.
func planVyOS(rendered, running string) (*vyosPlan, error) {
	runningPaths, err := parseVyOSRunning(running)
	if err != nil {
		return nil, err
	}

	// The device stores passwords hashed; verify the plaintext against the
	// hash so unchanged passwords are neither drift nor rewritten.
	runningPaths, candidate := vyosconfig.ReconcileHashedPasswords(
		runningPaths,
		vyosconfig.ParseSetCommands(rendered),
	)
	return &vyosPlan{
		diff:    vyosconfig.DiffPaths(runningPaths, candidate, vyosconfig.IgnoredPaths),
		running: runningPaths,
	}, nil
}

// parseVyOSRunning flattens a running config into path tuples, accepting
// either representation. An unparseable config must error, never yield an
// empty set: empty would turn every rendered path into an addition.
func parseVyOSRunning(running string) (vyosconfig.Set, error) {
	var tree any
	if err := json.Unmarshal([]byte(running), &tree); err == nil {
		return vyosconfig.PathsFromAPIJSON(tree), nil
	}

	paths := vyosconfig.ParseSetCommands(running)
	if len(paths) == 0 {
		return nil, fmt.Errorf("running config is neither a /retrieve JSON tree nor a `set ...` listing")
	}
	return paths, nil
}

// configDiff renders the plan into the shape the CLI and TUI print.
// DiffOptions.ShowDeviceOnly is ignored, as in Python: barf owns the whole
// VyOS tree, so removals are always real deletions and always shown.
func (p *vyosPlan) configDiff(opts DiffOptions) ConfigDiff {
	// Redact the PATHS here rather than leaving it to FormatDiff's node-name
	// test, which misses the fleet API key and the SNMP community.
	shown := p.diff
	if !opts.ShowSecrets {
		shown = redactVyOSDiff(p.diff)
	}
	d := ConfigDiff{
		HasChanges: p.diff.HasChanges(),
		Text:       vyosconfig.FormatDiff(shown, false),
		Summary:    vyosconfig.SummarizeDiff(p.diff),
	}
	for _, path := range p.diff.Added {
		d.Added = append(d.Added, "set "+path.String())
	}
	for _, path := range p.diff.Removed {
		d.Removed = append(d.Removed, "set "+path.String())
	}
	return d
}

// ops is the ordered operation list a deploy would send: deletes first
// (collapsed to whole-node deletes where possible), then `set` ops. Mirrors
// VyOSHost.push_rendered_config.
func (p *vyosPlan) ops() []ConfigOp {
	if !p.diff.HasChanges() {
		return nil
	}
	deletes := vyosconfig.MinimalDeletePaths(p.diff.Removed, p.running)

	ops := make([]ConfigOp, 0, len(deletes)+len(p.diff.Added))
	for _, path := range deletes {
		ops = append(ops, ConfigOp{Verb: opDelete, Path: []string(path)})
	}
	for _, path := range p.diff.Added {
		ops = append(ops, ConfigOp{Verb: opSet, Path: []string(path)})
	}
	return ops
}

// describeOps renders the ops a deploy would send, redacted unless
// opts.ShowSecrets. A dry run prints these literal commands.
func describeOps(ops []ConfigOp, opts DiffOptions) []string {
	lines := make([]string, 0, len(ops))
	for _, op := range ops {
		path := vyosconfig.Path(op.Path)
		if !opts.ShowSecrets {
			path = redactVyOSPath(path)
		}
		verb := op.Verb
		if verb == "" {
			verb = op.Command
		}
		lines = append(lines, verb+" "+path.String())
	}
	return lines
}
