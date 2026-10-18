// Package buildinfo holds values injected at link time.
package buildinfo

// Set via -ldflags "-X github.com/vyto4ka/vynel/internal/buildinfo.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable build description.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
