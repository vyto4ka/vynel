package update

import (
	"fmt"
	"regexp"
	"strings"
)

// CapXrayRelease identifies agents that accept an explicit Xray release in update commands.
const CapXrayRelease = "xray-release"

var xrayReleaseRE = regexp.MustCompile(`^v?[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// NormalizeXrayRelease accepts the syntax of official release tags, or ""/"latest" to follow the latest.
// Only a tag is accepted: never an arbitrary download URL or shell fragment.
func NormalizeXrayRelease(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "latest" {
		return "", nil
	}
	if !xrayReleaseRE.MatchString(value) {
		return "", fmt.Errorf("invalid Xray release %q: use a tag such as v26.10.31 or latest", value)
	}
	return "v" + strings.TrimPrefix(value, "v"), nil
}
