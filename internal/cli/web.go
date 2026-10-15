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
	var sessionPaths bool
	mode := &cobra.Command{
		Use:   "mode [password|link|miniapp]",
		Short: "How admins sign in: password form, one-time links only (bot, `vynel admin login-link`), Telegram Mini App only",
		Args:  cobra.MaximumNArgs(1), ValidArgs: service.LoginModes,
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if err := s.SetLoginMode(ctx, service.ActorCLI, args[0]); err != nil {
					return err
				}
				if args[0] == service.ModeLink || !sessionPaths {
					if err := s.SetSessionPaths(ctx, service.ActorCLI, sessionPaths && args[0] == service.ModeLink); err != nil {
						return err
					}
				}
			}
			return printMode(ctx, cmd.OutOrStdout(), s)
		}),
	}
	mode.Flags().BoolVar(&sessionPaths, "session-paths", false, "link mode: every login link opens its own random path, the panel has no fixed address")
	c.AddCommand(password, initCmd, mode)
	return c
}

// printMode prints the login mode and how to get in.
func printMode(ctx context.Context, w io.Writer, s *service.Service) error {
	m := s.LoginMode(ctx)
	fmt.Fprintf(w, "mode     %s\n", m)
	switch m {
	case service.ModeLink:
		if s.SessionPaths(ctx) {
			fmt.Fprintln(w, "paths    random per session (each link opens its own address)")
		}
		fmt.Fprintln(w, "sign in  a one-time link: /login in the Telegram bot or `vynel admin login-link` here")
	case service.ModeMiniApp:
		fmt.Fprintln(w, "sign in  the «Панель» button in the Telegram bot (back to links: vynel admin web mode link)")
	}
	return nil
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
	if m := s.LoginMode(ctx); m != service.ModePassword {
		fmt.Fprintf(w, "url      %s\n", url)
		return printMode(ctx, w, s)
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
