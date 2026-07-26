// Command barf is the read-only network config tool: it renders device
// configs from network.yml and compares them against what the fleet is
// running. It never writes to a device.
package main

import (
	"os"

	"github.com/general-programming/megarepo/go/pkg/barf/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
