package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/panel/backup"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func adminBotCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "bot", Short: "Telegram bot: token, binding admins",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if s.BotToken(ctx) == "" {
				fmt.Fprintln(w, "token    not set: vynel admin bot token 123456:AA… (from @BotFather)")
			} else {
				fmt.Fprintln(w, "token    set")
			}
			as, err := s.BotAdmins(ctx)
			if err != nil {
				return err
			}
			for _, a := range as {
				fmt.Fprintf(w, "admin    %d %s @%s (since %s)\n", a.ID, a.Name, a.Username, time.Unix(a.BoundAt, 0).Format("2006-01-02"))
			}
			if len(as) == 0 {
				fmt.Fprintln(w, "admins   none: vynel admin bot code, then send /start CODE to the bot")
			}
			return nil
		}),
	}
	token := &cobra.Command{
		Use: "token TOKEN", Short: "Set the bot token from @BotFather (empty string turns the bot off)", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			if err := s.SetBotToken(ctx, service.ActorCLI, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "saved; the panel starts the bot within a few seconds")
			return nil
		}),
	}
	code := &cobra.Command{
		Use: "code", Short: "Create a one-time code that binds your Telegram account (send /start CODE to the bot)",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			c, err := s.NewBindCode(ctx, service.ActorCLI)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "code %s (valid %s): send the bot\n\n  /start %s\n", c, service.BindCodeTTL, c)
			return nil
		}),
	}
	unbind := &cobra.Command{
		Use: "unbind TELEGRAM_ID", Short: "Remove an admin from the bot", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("bad id %q", args[0])
			}
			return s.RemoveBotAdmin(ctx, service.ActorCLI, id)
		}),
	}
	c.AddCommand(token, code, unbind)
	return c
}

func adminLoginLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use: "login-link", Short: "A one-time link into the web panel without a password (valid 1 minute)",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			link, exp, err := s.LoginLink(ctx, service.ActorCLI)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n(single use, valid until %s)\n", link, exp.Format("15:04:05"))
			return nil
		}),
	}
}

func adminBackupCmd() *cobra.Command {
	var out string
	c := &cobra.Command{
		Use: "backup", Short: "Write a backup (database + internal CA) to a .tar.gz",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			tmp, err := os.CreateTemp(filepath.Dir(absOr(out, ".")), ".vynel-backup-*")
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
			meta, err := backup.Create(ctx, s.Store(), adminDataDir, buildinfo.Version, tmp)
			if err != nil {
				tmp.Close()
				return err
			}
			if err := tmp.Close(); err != nil {
				return err
			}
			dst := out
			if dst == "" {
				dst = backup.FileName(meta, s.BotLocation(ctx))
			}
			if err := os.Rename(tmp.Name(), dst); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %d users, %d nodes\n", dst, meta.Users, meta.Nodes)
			return nil
		}),
	}
	c.Flags().StringVarP(&out, "out", "o", "", "file name (default vynel-backup-<domain>-<date>.tar.gz in the current directory)")
	return c
}

func absOr(p, def string) string {
	if p == "" {
		p = def
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if p == def {
		return filepath.Join(a, "x")
	}
	return a
}

func restoreCmd() *cobra.Command {
	var dataDir string
	var yes bool
	c := &cobra.Command{
		Use:   "restore BACKUP.tar.gz",
		Short: "Restore the panel from a backup (stop the panel first: systemctl stop vynel)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			meta, err := backup.Inspect(f)
			f.Close()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "backup of %s from %s (vynel %s): %d users, %d nodes\n", meta.Domain,
				time.Unix(meta.CreatedAt, 0).Format("2006-01-02 15:04"), meta.Version, meta.Users, meta.Nodes)
			if _, err := os.Stat(filepath.Join(dataDir, "panel.db")); err == nil && !yes {
				return fmt.Errorf("%s already has data; it will be moved aside, not deleted — repeat with --yes", dataDir)
			}
			_, aside, err := backup.Restore(args[0], dataDir)
			if err != nil {
				return err
			}
			// Check that the restored database opens and migrates.
			st, err := store.Open(cmd.Context(), filepath.Join(dataDir, "panel.db"))
			if err != nil {
				return fmt.Errorf("restored database does not open: %w", err)
			}
			st.Close()
			fmt.Fprintf(w, "restored into %s\n", dataDir)
			if aside != "" {
				fmt.Fprintf(w, "the previous data is in %s\n", aside)
			}
			fmt.Fprintln(w, "start the panel: systemctl start vynel (on a new IP, run on every node: vynel node set-panel NEW_IP:9443)")
			return nil
		},
	}
	c.Flags().StringVar(&dataDir, "data-dir", DefaultPanelDataDir, "panel data directory")
	c.Flags().BoolVar(&yes, "yes", false, "replace existing data (it is moved aside)")
	return c
}
