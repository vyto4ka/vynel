// Package scripts carries the installer inside the vynel binary: `vynel update` runs it when the
// current one cannot be downloaded (GitHub unreachable).
package scripts

import _ "embed"

// Install is scripts/install.sh as of this build.
//
//go:embed install.sh
var Install []byte
