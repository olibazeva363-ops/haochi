package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

const (
	defaultSKImportConcurrent = 10
	defaultSKImportPriority   = 1
)

type SKImportRequest struct {
	Content              string   `json:"content"`
	SKs                  []string `json:"sks"`
	Cookie               string   `json:"cookie"`
	NamePrefix           string   `json:"name_prefix"`
	Concurrency          *int     `json:"concurrency"`
	Priority             *int     `json:"priority"`
	RateMultiplier       *float64 `json:"rate_multiplier"`
	SkipDefaultGroupBind *bool    `json:"skip_default_group_bind"`
}

type SKConvertProbeRequest struct {
	SK     string `json:"sk"`
	Cookie string `json:"cookie"`
}

type SKConvertProbeResult struct {
	OK                 bool   `json:"ok"`
	Kind               string `json:"kind,omitempty"`
	Message            string `json:"message"`
	Details            string `json:"details,omitempty"`
	NeedsCookieRefresh bool   `json:"needs_cookie_refresh"`
	Retryable          bool   `json:"retryable"`
	Email              string `json:"email,omitempty"`
	ExpiresAt          int64  `json:"expires_at,omitempty"`
	SubscriptionType   string `json:"subscription_type,omitempty"`
}

type SKImportResult struct {
	ProxyCreated   int               `json:"proxy_created"`
	ProxyReused    int               `json:"proxy_reused"`
	ProxyFailed    int               `json:"proxy_failed"`
	AccountCreated int               `json:"account_created"`
	AccountFailed  int               `json:"account_failed"`
	Converted      int               `json:"converted"`
	ConvertFailed  int               `json:"convert_failed"`
	Errors         []DataImportError `json:"errors,omitempty"`
}

// ImportFromSK converts Claude sk-ant-sid cookies through the configured converter and
// imports the resulting Anthropic OAuth accounts into the account pool.
func (h *AccountHandler) ImportFromSK(c *gin.Context) {
	var req SKImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	sks := normalizeSKInputs(req.Content, req.SKs)
	if len(sks) == 0 {
		response.BadRequest(c, "at least one sk is required")
		return
	}

	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		cookie = service.ResolveConvertCookie(c.Request.Context())
	}
	if cookie == "" {
		response.BadRequest(c, "converter cookie is required; set SUB2API_CONVERT_URL and SUB2API_CONVERT_COOKIE or provide cookie")
		return
	}

	result, err := h.importFromSK(c.Request.Context(), req, sks, cookie)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) ProbeSKConverter(c *gin.Context) {
	var req SKConvertProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	sk := strings.TrimSpace(req.SK)
	if sk == "" {
		response.BadRequest(c, "请填写一个用于检测的 sk-ant-sid02")
		return
	}
	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		cookie = service.ResolveConvertCookie(c.Request.Context())
	}
	if cookie == "" {
		response.Success(c, SKConvertProbeResult{
			OK:                 false,
			Kind:               service.ClaudeSKConvertKindConfiguration,
			Message:            "服务器未配置转换站 Cookie，请配置 SUB2API_CONVERT_COOKIE 或在检测时临时粘贴 Cookie",
			NeedsCookieRefresh: true,
			Retryable:          false,
		})
		return
	}

	converted, err := service.ConvertClaudeSK(c.Request.Context(), sk, cookie)
	if err != nil {
		result := SKConvertProbeResult{
			OK:        false,
			Kind:      service.ClaudeSKConvertKindTemporary,
			Message:   err.Error(),
			Retryable: true,
		}
		var convertErr *service.ClaudeSKConvertError
		if errors.As(err, &convertErr) {
			result.Kind = convertErr.Kind
			result.Message = convertErr.UserMessage()
			result.Details = convertErr.Error()
			result.NeedsCookieRefresh = convertErr.NeedsCookieRefresh
			result.Retryable = convertErr.Retryable
		}
		response.Success(c, result)
		return
	}

	response.Success(c, SKConvertProbeResult{
		OK:               true,
		Message:          "转换接口可用，Cookie 有效，测试 SK 可以正常转换",
		Email:            converted.Email,
		ExpiresAt:        converted.ExpiresAtSeconds,
		SubscriptionType: converted.SubscriptionType,
	})
}

func (h *AccountHandler) importFromSK(ctx context.Context, req SKImportRequest, sks []string, cookie string) (SKImportResult, error) {
	result := SKImportResult{}
	accounts := make([]DataAccount, 0, len(sks))

	for i, sk := range sks {
		converted, err := service.ConvertClaudeSK(ctx, sk, cookie)
		if err != nil {
			result.AccountFailed++
			result.ConvertFailed++
			result.Errors = append(result.Errors, DataImportError{
				Kind:    "account",
				Name:    maskSK(sk),
				Message: skImportErrorMessage(err),
			})
			continue
		}

		result.Converted++
		accounts = append(accounts, buildSKImportedAccount(converted, sk, i+1, req))
	}

	if len(accounts) == 0 {
		return result, nil
	}

	importResult, err := h.importData(ctx, DataImportRequest{
		Data: DataPayload{
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Proxies:    []DataProxy{},
			Accounts:   accounts,
		},
		SkipDefaultGroupBind: req.SkipDefaultGroupBind,
	})
	if err != nil {
		return result, err
	}

	result.ProxyCreated += importResult.ProxyCreated
	result.ProxyReused += importResult.ProxyReused
	result.ProxyFailed += importResult.ProxyFailed
	result.AccountCreated += importResult.AccountCreated
	result.AccountFailed += importResult.AccountFailed
	result.Errors = append(result.Errors, importResult.Errors...)

	return result, nil
}

func skImportErrorMessage(err error) string {
	var convertErr *service.ClaudeSKConvertError
	if errors.As(err, &convertErr) {
		message := convertErr.UserMessage()
		if convertErr.Kind != "" {
			message += " [" + convertErr.Kind + "]"
		}
		if convertErr.BodySnippet != "" {
			message += " details=" + convertErr.BodySnippet
		}
		return logredact.RedactText(message)
	}
	return logredact.RedactText(err.Error())
}

func normalizeSKInputs(content string, sks []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(sks))
	add := func(raw string) {
		value := strings.TrimSpace(raw)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	for _, sk := range sks {
		add(sk)
	}
	for _, part := range strings.FieldsFunc(content, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ',' || r == ';' || r == ' '
	}) {
		add(part)
	}
	return out
}

func buildSKImportedAccount(converted service.ConvertedClaudeOAuth, sk string, index int, req SKImportRequest) DataAccount {
	nowMs := time.Now().UnixMilli()

	name := strings.TrimSpace(converted.Email)
	if name == "" {
		prefix := strings.TrimSpace(req.NamePrefix)
		if prefix == "" {
			prefix = "claude-oauth"
		}
		name = fmt.Sprintf("%s-%d", prefix, index)
	}
	// Prefix the account name with the subscription tier (e.g. "Max 20x:user@x.com")
	// so plan differences are visible at a glance in the pool — the hook for triaging
	// rate-limit pressure, where a Max 20x and a Pro account behave nothing alike.
	if label := skImportTierLabel(converted); label != "" {
		name = clampAccountName(label + ":" + name)
	}

	concurrency := defaultSKImportConcurrent
	if req.Concurrency != nil && *req.Concurrency >= 0 {
		concurrency = *req.Concurrency
	}
	priority := defaultSKImportPriority
	if req.Priority != nil && *req.Priority >= 0 {
		priority = *req.Priority
	}
	rateMultiplier := 1.0
	if req.RateMultiplier != nil && *req.RateMultiplier >= 0 {
		rateMultiplier = *req.RateMultiplier
	}
	autoPause := true

	credentials := service.BuildClaudeCredentialsFromConverted(converted)
	credentials["_token_version"] = nowMs
	credentials[service.ClaudeSKCookieCredentialKey] = strings.TrimSpace(sk)

	extra := map[string]any{
		"source":            service.ClaudeSKImportSource,
		"subscription_type": converted.SubscriptionType,
	}
	// Keep the converter's label verbatim (the account name carries a normalized form).
	if converted.SubscriptionLabel != "" {
		extra["subscription_label"] = converted.SubscriptionLabel
	}
	if converted.Email != "" {
		extra["email_address"] = converted.Email
	}

	return DataAccount{
		Name:               name,
		Platform:           service.PlatformAnthropic,
		Type:               service.AccountTypeOAuth,
		Credentials:        credentials,
		Extra:              extra,
		Concurrency:        concurrency,
		Priority:           priority,
		RateMultiplier:     &rateMultiplier,
		AutoPauseOnExpired: &autoPause,
	}
}

// skImportTierLabel resolves the tier prefix used in an imported account's name.
// The converter's own subscription_label ("Claude Max 20×") is preferred because it
// keeps distinctions that a raw-type mapping collapses — max_20x and max_5x differ ~4x
// in quota, which is exactly the axis that matters when triaging rate limits. Falls
// back to deriving a label from subscription_type when the converter omits it.
func skImportTierLabel(converted service.ConvertedClaudeOAuth) string {
	if label := normalizeSubscriptionLabel(converted.SubscriptionLabel); label != "" {
		return label
	}
	return formatSubscriptionLabel(converted.SubscriptionType)
}

// normalizeSubscriptionLabel turns the converter's display label into something usable
// as a name prefix. Two adjustments: the leading "Claude" is dropped (redundant — every
// account in this pool is Claude), and U+00D7 (×) becomes "x" since the multiplication
// sign cannot be typed into the admin search box, defeating the point of the prefix.
func normalizeSubscriptionLabel(label string) string {
	s := strings.TrimSpace(label)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "×", "x")
	if len(s) >= 6 && strings.EqualFold(s[:6], "claude") {
		if rest := strings.TrimSpace(s[6:]); rest != "" {
			s = rest
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// clampAccountName keeps a generated name inside the accounts.name limit
// (MaxLen(100) in the ent schema). An over-long name fails the insert outright, so
// truncating is strictly better than losing the account at import time. Trimming is
// rune-aware so a multi-byte label can never be cut mid-character.
func clampAccountName(name string) string {
	const maxAccountNameLen = 100
	if len(name) <= maxAccountNameLen {
		return name
	}
	out := name
	for len(out) > maxAccountNameLen {
		_, size := utf8.DecodeLastRuneInString(out)
		if size == 0 {
			break
		}
		out = out[:len(out)-size]
	}
	return strings.TrimSpace(out)
}

// formatSubscriptionLabel maps the raw upstream subscription type to a short, human
// friendly tier label used as an account-name prefix (e.g. "Pro" in "Pro:user@x.com").
// Known tiers are normalized; any unrecognized value — including the generic
// "default_claude_ai" default emitted when the converter can't tell — is returned
// trimmed-but-verbatim so no information is lost. That verbatim fallback doubles as a
// diagnostic: if imported names come out "default_claude_ai:..." instead of "Pro:.."/
// "Max:..", the converter isn't distinguishing plans and the name-based triage won't help.
func formatSubscriptionLabel(subscriptionType string) string {
	raw := strings.TrimSpace(subscriptionType)
	if raw == "" {
		return ""
	}
	key := strings.TrimPrefix(strings.ToLower(raw), "claude_")
	switch {
	case strings.HasPrefix(key, "pro"):
		return "Pro"
	case strings.HasPrefix(key, "max"):
		return "Max"
	case strings.HasPrefix(key, "team"):
		return "Team"
	case strings.HasPrefix(key, "enterprise"):
		return "Enterprise"
	case key == "free":
		return "Free"
	default:
		return raw
	}
}

func maskSK(sk string) string {
	if len(sk) <= 12 {
		return sk
	}
	return sk[:8] + "..." + sk[len(sk)-4:]
}
