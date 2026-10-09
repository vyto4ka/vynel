package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/setup"
)

// setupTUICmd is the installer's form (scripts/install.sh runs it before installing).
func setupTUICmd() *cobra.Command {
	var o setup.Options
	var out string
	c := &cobra.Command{
		Use: "setup-tui", Short: "Installer form: answers are written as shell variables", Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := setup.Run(o)
			if errors.Is(err, setup.ErrAborted) {
				fmt.Fprintln(os.Stderr, "отменено")
				os.Exit(2)
			}
			if err != nil {
				return err
			}
			f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer f.Close()
			return a.Shell(f)
		},
	}
	c.Flags().StringVar(&out, "out", "", "file for the answers")
	c.Flags().StringVar(&o.Installed, "installed", "", "what is installed: aio, panel, node or empty")
	c.Flags().StringVar(&o.Version, "version", "", "installed version")
	c.Flags().StringVar(&o.PublicIP, "public-ip", "", "detected public IP")
	c.Flags().StringVar(&o.Country, "country", "", "detected country code")
	c.Flags().StringVar(&o.Domain, "domain", "", "prefilled domain")
	c.Flags().StringVar(&o.Email, "email", "", "prefilled email")
	c.Flags().StringVar(&o.Token, "token", "", "prefilled node token")
	_ = c.MarkFlagRequired("out")
	return c
}
