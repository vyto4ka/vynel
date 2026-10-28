package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// adminAPITokenCmd manages the tokens of the public API (docs/API.md) from the terminal, for
// scripts and installers that set up an integration without the web panel.
func adminAPITokenCmd() *cobra.Command {
	c := &cobra.Command{Use: "api-token", Short: "Tokens of the public API (docs/API.md)"}
	var scopes, allow []string
	var rate int
	var expires string
	create := &cobra.Command{
		Use: "create NAME", Short: "Create a token; it is printed once and cannot be shown again", Args: cobra.ExactArgs(1),
		Example: "  vynel admin api-token create billing --scope users:read,users:write --allow 203.0.113.5 --expires 90d",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, args []string) error {
			in := service.APITokenInput{Name: args[0], Scopes: scopes, CIDRs: allow, RateLimit: rate}
			if expires != "" && expires != "never" {
				d, err := parseSpan(expires)
				if err != nil {
					return err
				}
				at := time.Now().Add(d)
				in.ExpiresAt = &at
			}
			t, secret, err := s.CreateAPIToken(ctx, service.ActorCLI, in)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id       %d\nname     %s\nscopes   %s\n", t.ID, t.Name, strings.Join(t.Scopes, ","))
			fmt.Fprintf(out, "token    %s\n", secret)
			fmt.Fprintln(out, "\nCopy the token now: the panel keeps only its hash and cannot show it again.")
			return nil
		}),
	}
	create.Flags().StringSliceVar(&scopes, "scope", nil, "permissions, comma-separated: "+strings.Join(service.APIScopes, ", "))
	create.Flags().StringSliceVar(&allow, "allow", nil, "addresses or networks the token may come from (default: anywhere)")
	create.Flags().IntVar(&rate, "rate", 0, fmt.Sprintf("requests per minute (default %d)", service.DefaultAPIRate))
	create.Flags().StringVar(&expires, "expires", "", "how long the token lives: 90d, 12h… (default: never)")
	_ = create.MarkFlagRequired("scope")

	list := &cobra.Command{
		Use: "list", Short: "List tokens",
		RunE: withService(func(ctx context.Context, s *service.Service, cmd *cobra.Command, _ []string) error {
			ts, err := s.APITokens(ctx)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, t := range ts {
				state := "active"
				switch {
				case t.RevokedAt != nil:
					state = "revoked"
				case t.ExpiresAt != nil && *t.ExpiresAt <= time.Now().Unix():
					state = "expired"
				}
				from := strings.Join(t.CIDRs, ",")
				if from == "" {
					from = "anywhere"
				}
				rows = append(rows, []string{strconv.FormatInt(t.ID, 10), t.Name, t.Prefix + "…", state, strings.Join(t.Scopes, ","), from, ts2(t.ExpiresAt), ts2(t.LastUsedAt)})
			}
			table(cmd.OutOrStdout(), "ID\tNAME\tTOKEN\tSTATE\tSCOPES\tFROM\tEXPIRES\tLAST USED", rows)
			return nil
		}),
	}
	revoke := &cobra.Command{
		Use: "revoke ID", Short: "Switch a token off at once", Args: cobra.ExactArgs(1),
		RunE: withService(func(ctx context.Context, s *service.Service, _ *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("ID is a number from `api-token list`")
			}
			return s.RevokeAPIToken(ctx, service.ActorCLI, id)
		}),
	}
	c.AddCommand(create, list, revoke)
	return c
}

// ts2 is ts() that says "never" for a missing time.
func ts2(p *int64) string {
	if p == nil {
		return "never"
	}
	return ts(p)
}

// parseSpan reads 90d, 12h, 30m… (a day is 24 hours).
func parseSpan(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("--expires: %q is not a duration like 90d or 12h", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--expires: %q is not a duration like 90d or 12h", s)
	}
	return d, nil
}
