package claude

import (
	"os"
	"strings"

	"golang.org/x/mod/semver"
)

const defaultSDKPackageVersion = "0.94.0"

// SDKPackageVersion preserves the deployment override alongside the upstream
// CLI version resolver. Resolve once so requests keep a consistent identity.
var SDKPackageVersion = resolveSDKPackageVersion(os.Getenv("SUB2API_CLAUDE_SDK_VERSION"))

func resolveSDKPackageVersion(raw string) string {
	version := strings.TrimSpace(raw)
	canonical := "v" + version
	if !semver.IsValid(canonical) || semver.Canonical(canonical) != canonical ||
		semver.Prerelease(canonical) != "" || semver.Build(canonical) != "" {
		return defaultSDKPackageVersion
	}
	return version
}
