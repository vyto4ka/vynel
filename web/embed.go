// Package web embeds the built single-page UI (web/dist) into the binary.
package web

import "embed"

// Dist holds the Vite build output. Run `make web` to populate it; without a build only .gitkeep is embedded.
//
//go:embed all:dist
var Dist embed.FS
