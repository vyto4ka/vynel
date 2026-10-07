// Command vynel is the single binary for the panel, the node agent and the admin tooling.
package main

import (
	"os"
	_ "time/tzdata" // timezones for the bot on servers without tzdata

	"github.com/vyto4ka/vynel/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
