package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/panel/usertransfer"
)

// userTransferCmd is `vynel admin user transfer export|import`: moving users between
// installations (vynel panels, Remnawave, 3x-ui) without a full backup.
func userTransferCmd() *cobra.Command {
	c := &cobra.Command{Use: "transfer", Short: "Move users between panels (export and import without a backup)"}

	var status, search, password string
	export := &cobra.Command{
		Use: "export [FILE]", Short: "Write the users as a JSON file (default: users-export.json)", Args: cobra.MaximumNArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			path := "users-export.json"
			if len(args) == 1 {
				path = args[0]
			}
			f, err := usertransfer.Export(ctx, s.Store(), usertransfer.ExportOptions{Filter: store.UserFilter{Status: status, Search: search}})
			if err != nil {
				return err
			}
			b, err := f.Marshal()
			if err != nil {
				return err
			}
			if password != "" {
				if b, err = usertransfer.Encrypt(b, password); err != nil {
					return err
				}
			}
			if err := os.WriteFile(path, b, 0o600); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "exported %d users to %s\n", f.Meta.Users, path)
			return nil
		}),
	}
	export.Flags().StringVar(&status, "status", "", "only this status: active|disabled|limited|expired")
	export.Flags().StringVar(&search, "search", "", "substring of username or note")
	export.Flags().StringVar(&password, "password", "", "encrypt the file with this password (age; the file has tokens, so protect it)")

	var mode, importPassword string
	var dryRun, reissue bool
	importCmd := &cobra.Command{
		Use: "import FILE", Short: "Read users from a vynel export file", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			if b, err = usertransfer.Decrypt(b, importPassword); err != nil {
				return err
			}
			f, err := usertransfer.Unmarshal(b)
			if err != nil {
				return err
			}
			rep, err := importFile(ctx, s, f, usertransfer.ImportOptions{Mode: mode, DryRun: dryRun, ReissueTokens: reissue, Actor: "cli"})
			if err != nil {
				return err
			}
			printImportReport(cmd.OutOrStdout(), f, rep, dryRun)
			return nil
		}),
	}
	importCmd.Flags().StringVar(&mode, "mode", "skip", "what to do with users already here: skip, upsert or rename")
	importCmd.Flags().StringVar(&importPassword, "password", "", "password of an encrypted export file")
	importCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only report what would happen")
	importCmd.Flags().BoolVar(&reissue, "reissue-tokens", false, "give imported users new subscription links and print them")

	var rnURL, rnToken string
	var rnMode string
	var rnDryRun, rnReissue bool
	importRn := &cobra.Command{
		Use: "import-remnawave", Short: "Pull users from a Remnawave panel over its API", Args: cobra.NoArgs,
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			restore := usertransfer.RnSetPrivate(true) // the panel may live in the admin's own network
			defer restore()
			f, err := usertransfer.FetchRemnawave(ctx, usertransfer.RemnawavePanel{BaseURL: rnURL, Token: rnToken})
			if err != nil {
				return err
			}
			// Remnawave tokens are not vynel's: everyone gets a fresh link, listed in the report.
			rnReissue = true
			rep, err := importFile(ctx, s, f, usertransfer.ImportOptions{Mode: rnMode, DryRun: rnDryRun, ReissueTokens: rnReissue, Actor: "cli"})
			if err != nil {
				return err
			}
			printImportReport(cmd.OutOrStdout(), f, rep, rnDryRun)
			return nil
		}),
	}
	importRn.Flags().StringVar(&rnURL, "url", "", "Remnawave panel address, e.g. https://panel.example.com")
	importRn.Flags().StringVar(&rnToken, "token", "", "Remnawave API token (Panel → API tokens)")
	importRn.Flags().StringVar(&rnMode, "mode", "skip", "what to do with users already here: skip, upsert or rename")
	importRn.Flags().BoolVar(&rnDryRun, "dry-run", false, "only report what would happen")
	importRn.Flags().BoolVar(&rnReissue, "reissue-tokens", true, "give imported users new subscription links and print them")

	var xuiMode string
	var xuiDryRun, xuiReissue bool
	importXUI := &cobra.Command{
		Use: "import-3xui DB", Short: "Pull users from a 3x-ui database file", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			f, err := usertransfer.FetchXUI(ctx, args[0])
			if err != nil {
				return err
			}
			// 3x-ui subscription ids are not vynel tokens: everyone gets a fresh link.
			xuiReissue = true
			rep, err := importFile(ctx, s, f, usertransfer.ImportOptions{Mode: xuiMode, DryRun: xuiDryRun, ReissueTokens: xuiReissue, Actor: "cli"})
			if err != nil {
				return err
			}
			printImportReport(cmd.OutOrStdout(), f, rep, xuiDryRun)
			return nil
		}),
	}
	importXUI.Flags().StringVar(&xuiMode, "mode", "skip", "what to do with users already here: skip, upsert or rename")
	importXUI.Flags().BoolVar(&xuiDryRun, "dry-run", false, "only report what would happen")
	importXUI.Flags().BoolVar(&xuiReissue, "reissue-tokens", true, "give imported users new subscription links and print them")

	c.AddCommand(export, importCmd, importRn, importXUI)
	return c
}

// importFile runs an import through the service so tokens and links come from it.
func importFile(ctx context.Context, s *service.Service, f *usertransfer.ExportFile, opt usertransfer.ImportOptions) (*usertransfer.Report, error) {
	im := usertransfer.NewImporter(s.Store(), opt)
	im.NewTokenFn = service.NewSubToken
	im.LinkFn = func(u *store.User) (string, error) { return s.SubscriptionURL(ctx, u) }
	return im.Import(ctx, f)
}

func printImportReport(w io.Writer, f *usertransfer.ExportFile, rep *usertransfer.Report, dryRun bool) {
	title := "imported"
	if dryRun {
		title = "would import"
	}
	fmt.Fprintf(w, "%s %d of %d users: created %d, updated %d, skipped %d, renamed %d, failed %d\n",
		title, rep.Created+rep.Updated, f.Meta.Users, rep.Created, rep.Updated, rep.Skipped, rep.Renamed, rep.Failed)
	for _, p := range rep.Problems {
		fmt.Fprintf(w, "  problem: %s\n", p)
	}
	if missing := rep.MissingGroups(); len(missing) > 0 {
		fmt.Fprintf(w, "groups not on this panel (users landed without them): %v\n", missing)
	}
	for _, l := range rep.Links {
		fmt.Fprintf(w, "  new link: %s  %s\n", l.Username, l.Link)
	}
}
