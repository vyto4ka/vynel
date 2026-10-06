// Command vpn is the single binary for the panel, the node agent and the admin tooling.
package main

import (
	"os"

	"github.com/vyto4ka/vpn/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
