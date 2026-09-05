package claude

import "testing"

func TestSDKVersionOverride(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", defaultSDKPackageVersion},
		{" 0.95.1 ", "0.95.1"},
		{"0.95", defaultSDKPackageVersion},
		{"0.95.1-local", defaultSDKPackageVersion},
		{"0.95.1\r\nInjected: yes", defaultSDKPackageVersion},
	} {
		if got := resolveSDKPackageVersion(tc.input); got != tc.want {
			t.Errorf("resolveSDKPackageVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := DefaultHeaders["User-Agent"]; got != "claude-cli/"+CLIVersion()+" (external, cli)" {
		t.Fatalf("default user agent %q does not use the resolved CLI version", got)
	}
}
