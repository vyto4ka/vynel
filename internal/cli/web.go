package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func adminWebCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "web", Short: "Web panel address and admin login",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			return printWeb(ctx, cmd.OutOrStdout(), s, "")
		}),
	}
	var login string
	password := &cobra.Command{
		Use: "password", Short: "Generate a new admin password (and optionally change the login)",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			pw, err := s.SetAdminCredentials(ctx, service.ActorCLI, login)
			if err != nil {
				return err
			}
			return printWeb(ctx, cmd.OutOrStdout(), s, pw)
		}),
	}
	password.Flags().StringVar(&login, "login", "", "new login (default: keep)")
	var initLogin string
	initCmd := &cobra.Command{
		Use: "init", Short: "Create the admin with a generated password unless one exists (used by the installer)",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			if err := s.EnsureWebDefaults(ctx); err != nil {
				return err
			}
			pw := ""
			if !s.HasAdmin(ctx) {
				var err error
				if pw, err = s.SetAdminCredentials(ctx, service.ActorCLI, initLogin); err != nil {
					return err
				}
			}
			return printWeb(ctx, cmd.OutOrStdout(), s, pw)
		}),
	}
	initCmd.Flags().StringVar(&initLogin, "login", "admin", "admin login")
	c.AddCommand(password, initCmd)
	return c
}

// printWeb prints "key value" lines; the installer parses them.
func printWeb(ctx context.Context, w io.Writer, s *service.Service, password string) error {
	if err := s.EnsureWebDefaults(ctx); err != nil {
		return err
	}
	url, err := s.WebURL(ctx)
	if err != nil {
		return err
	}
	if url == "" {
		path, _ := s.WebPath(ctx)
		listen, _ := s.Setting(ctx, service.SettingWebListen, service.DefaultWebListen)
		url = fmt.Sprintf("http://%s%s (no domain: open through an SSH tunnel, ssh -L %s:%s root@server)", listen, path, "2097", listen)
	}
	login, _ := s.Setting(ctx, service.SettingWebLogin, "")
	fmt.Fprintf(w, "url      %s\nlogin    %s\n", url, login)
	switch {
	case password != "":
		fmt.Fprintf(w, "password %s\n", password)
	case !s.HasAdmin(ctx):
		fmt.Fprintln(w, "password — not set: run `vynel admin web password`")
	default:
		fmt.Fprintln(w, "password (unchanged; new one: vynel admin web password)")
	}
	return nil
}
