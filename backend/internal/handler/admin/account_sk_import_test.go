package admin

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestNormalizeSKInputsSplitsAndDeduplicates(t *testing.T) {
	got := normalizeSKInputs(" sk-a\nsk-b, sk-a;sk-c\t", []string{"sk-d", "sk-b"})
	want := []string{"sk-d", "sk-b", "sk-a", "sk-c"}

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseConvertedClaudeOAuthSupportsNestedCamelCase(t *testing.T) {
	raw := map[string]any{
		"email":              "claude@example.com",
		"subscription_label": "Claude Max 20×",
		"token_json": map[string]any{
			"claudeAiOauth": map[string]any{
				"accessToken":      "access-token",
				"refreshToken":     "refresh-token",
				"expiresAt":        float64(1893456000000),
				"scopes":           []any{"user:chat", "user:profile"},
				"subscriptionType": "pro",
			},
		},
	}

	got, err := service.ParseConvertedClaudeOAuth(raw)
	if err != nil {
		t.Fatalf("parseConvertedClaudeOAuth error = %v", err)
	}
	if got.AccessToken != "access-token" || got.RefreshToken != "refresh-token" {
		t.Fatalf("tokens = %q/%q", got.AccessToken, got.RefreshToken)
	}
	if got.ExpiresAtSeconds != 1893456000 {
		t.Fatalf("expires = %d, want 1893456000", got.ExpiresAtSeconds)
	}
	if got.Email != "claude@example.com" {
		t.Fatalf("email = %q", got.Email)
	}
	if strings.Join(got.Scopes, " ") != "user:chat user:profile" {
		t.Fatalf("scopes = %#v", got.Scopes)
	}
	if got.SubscriptionType != "pro" {
		t.Fatalf("subscription = %q", got.SubscriptionType)
	}
	if got.SubscriptionLabel != "Claude Max 20×" {
		t.Fatalf("subscription label = %q", got.SubscriptionLabel)
	}
}

func TestNormalizeSubscriptionLabel(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"   ":            "",
		"Claude Max 20×": "Max 20x", // the real value observed from the converter
		"Claude Max 5×":  "Max 5x",
		"claude pro":     "pro",    // prefix strip is case-insensitive
		"Claude":         "Claude", // nothing left after the prefix -> keep verbatim
		"Max  20x":       "Max 20x",
		"  Claude Pro  ": "Pro",
	}
	for in, want := range cases {
		if got := normalizeSubscriptionLabel(in); got != want {
			t.Fatalf("normalizeSubscriptionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSKImportedAccountPrefersConverterLabel(t *testing.T) {
	// Mirrors a real converter response: subscription_label alongside the raw type.
	acc := buildSKImportedAccount(service.ConvertedClaudeOAuth{
		SubscriptionType:  "max_20x",
		SubscriptionLabel: "Claude Max 20×",
		Email:             "lor@example.com",
	}, "sk-ant-sid02-z", 1, SKImportRequest{})

	if acc.Name != "Max 20x:lor@example.com" {
		t.Fatalf("name = %q, want %q", acc.Name, "Max 20x:lor@example.com")
	}
	// Extra keeps the converter's label verbatim; only the name is normalized.
	if acc.Extra["subscription_label"] != "Claude Max 20×" {
		t.Fatalf("extra label = %#v, want verbatim", acc.Extra["subscription_label"])
	}
	if acc.Extra["subscription_type"] != "max_20x" {
		t.Fatalf("extra type = %#v", acc.Extra["subscription_type"])
	}
}

func TestBuildSKImportedAccountKeepsMaxTierGranularity(t *testing.T) {
	// The whole point of preferring the label: 20x and 5x differ ~4x in quota, so they
	// must not collapse into one indistinguishable "Max" bucket.
	name := func(typ, label string) string {
		return buildSKImportedAccount(service.ConvertedClaudeOAuth{
			SubscriptionType:  typ,
			SubscriptionLabel: label,
			Email:             "a@example.com",
		}, "sk", 1, SKImportRequest{}).Name
	}
	n20, n5 := name("max_20x", "Claude Max 20×"), name("max_5x", "Claude Max 5×")
	if n20 == n5 {
		t.Fatalf("20x and 5x collapsed to the same name: %q", n20)
	}
}

func TestClampAccountNameStaysWithinColumnLimit(t *testing.T) {
	// accounts.name is MaxLen(100); an over-long name would fail the insert.
	got := clampAccountName("Max 20x:" + strings.Repeat("a", 200) + "@example.com")
	if len(got) > 100 {
		t.Fatalf("len = %d, want <= 100", len(got))
	}
	// A multi-byte label must never be cut mid-rune.
	multi := clampAccountName(strings.Repeat("测", 60))
	if len(multi) > 100 || !utf8.ValidString(multi) {
		t.Fatalf("multibyte clamp: len=%d valid=%v", len(multi), utf8.ValidString(multi))
	}
}

func TestFormatSubscriptionLabel(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"pro":               "Pro",
		"Pro":               "Pro",
		"claude_pro":        "Pro",
		"max":               "Max",
		"max_5x":            "Max",
		"claude_max_20x":    "Max",
		"team":              "Team",
		"enterprise":        "Enterprise",
		"free":              "Free",
		"default_claude_ai": "default_claude_ai", // unknown/generic -> verbatim (diagnostic)
		"something_new":     "something_new",     // unknown -> verbatim
	}
	for in, want := range cases {
		if got := formatSubscriptionLabel(in); got != want {
			t.Fatalf("formatSubscriptionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSKImportedAccountPrefixesNameWithTier(t *testing.T) {
	// Pro tier + email -> "Pro:email"; the whole point of the rename.
	pro := buildSKImportedAccount(service.ConvertedClaudeOAuth{
		SubscriptionType: "pro",
		Email:            "alice@example.com",
	}, "sk-ant-sid02-x", 1, SKImportRequest{})
	if pro.Name != "Pro:alice@example.com" {
		t.Fatalf("pro name = %q, want %q", pro.Name, "Pro:alice@example.com")
	}

	// Empty email still gets the tier prefix on the fallback name.
	noEmail := buildSKImportedAccount(service.ConvertedClaudeOAuth{
		SubscriptionType: "max",
	}, "sk-ant-sid02-y", 3, SKImportRequest{NamePrefix: "pool"})
	if noEmail.Name != "Max:pool-3" {
		t.Fatalf("no-email name = %q, want %q", noEmail.Name, "Max:pool-3")
	}
}

func TestBuildSKImportedAccountUsesAnthropicOAuthFormat(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Unix()
	rate := 2.5
	concurrency := 7
	priority := 3
	account := buildSKImportedAccount(service.ConvertedClaudeOAuth{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		ExpiresAtSeconds: expiresAt,
		Scopes:           []string{"user:chat", "user:inference"},
		SubscriptionType: "team",
		Email:            "claude@example.com",
	}, "sk-ant-sid02-original", 1, SKImportRequest{
		NamePrefix:     "custom",
		Concurrency:    &concurrency,
		Priority:       &priority,
		RateMultiplier: &rate,
	})

	if account.Name != "Team:claude@example.com" {
		t.Fatalf("name = %q", account.Name)
	}
	if account.Platform != service.PlatformAnthropic || account.Type != service.AccountTypeOAuth {
		t.Fatalf("platform/type = %q/%q", account.Platform, account.Type)
	}
	if account.Concurrency != concurrency || account.Priority != priority {
		t.Fatalf("concurrency/priority = %d/%d", account.Concurrency, account.Priority)
	}
	if account.RateMultiplier == nil || *account.RateMultiplier != rate {
		t.Fatalf("rate multiplier = %#v", account.RateMultiplier)
	}
	if account.AutoPauseOnExpired == nil || !*account.AutoPauseOnExpired {
		t.Fatalf("auto pause should be enabled")
	}
	if account.Credentials["access_token"] != "access-token" {
		t.Fatalf("access token not set")
	}
	if account.Credentials["refresh_token"] != "refresh-token" {
		t.Fatalf("refresh token not set")
	}
	if account.Credentials[service.ClaudeSKCookieCredentialKey] != "sk-ant-sid02-original" {
		t.Fatalf("original sk not preserved: %#v", account.Credentials[service.ClaudeSKCookieCredentialKey])
	}
	if account.Credentials["expires_at"] != strconv.FormatInt(expiresAt, 10) {
		t.Fatalf("expires_at = %v, want %d", account.Credentials["expires_at"], expiresAt)
	}
	if account.Credentials["scope"] != "user:chat user:inference" {
		t.Fatalf("scope = %v", account.Credentials["scope"])
	}
	if account.Extra["source"] != "sk_import" || account.Extra["subscription_type"] != "team" {
		t.Fatalf("extra = %#v", account.Extra)
	}
}
