package cli

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/vyto4ka/vpn/internal/buildinfo"
	"github.com/vyto4ka/vpn/internal/panel/app"
	"github.com/vyto4ka/vpn/internal/xray"
)

func panelCmd() *cobra.Command {
	var cfg app.Config
	c := &cobra.Command{
		Use:   "panel",
		Short: "Run the panel (node gateway, reconciler, optional local node)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Version = buildinfo.Version
			cfg.Log = slog.Default()
			return app.Run(cmd.Context(), cfg)
		},
	}
	f := c.Flags()
	f.StringVar(&cfg.DataDir, "data-dir", DefaultPanelDataDir, "data directory (panel.db, ca/, node/)")
	f.StringVar(&cfg.GatewayListen, "gateway-listen", ":9443", "listen address for node connections")
	f.StringVar(&cfg.GatewayAddr, "public-addr", "", "host:port nodes use to reach the gateway (stored; goes into join tokens)")
	f.BoolVar(&cfg.WithNode, "with-node", false, "also run a node inside the panel process")
	f.StringVar(&cfg.LocalNode.Name, "node-name", "", "local node name (first start only)")
	f.StringVar(&cfg.LocalNode.Country, "node-country", "", "local node country code (first start only)")
	f.StringVar(&cfg.LocalNode.Domain, "node-domain", "", "local node domain (first start only)")
	addXrayFlags(c, &cfg.Xray)
	return c
}

func addXrayFlags(c *cobra.Command, b *xray.Binary) {
	c.Flags().StringVar(&b.Path, "xray-bin", "/usr/local/bin/xray", "path to the xray binary")
	c.Flags().StringVar(&b.AssetDir, "xray-assets", "/usr/local/share/xray", "directory with geoip.dat and geosite.dat")
}
