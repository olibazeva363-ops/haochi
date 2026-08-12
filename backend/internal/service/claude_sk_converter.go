package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ClaudeSKImportSource        = "sk_import"
	ClaudeSKCookieCredentialKey = "sk_cookie"

	// ConvertCookieSettingKey is the settings key that stores the converter-station
	// cookie configured from the admin UI. When present it overrides the
	// SUB2API_CONVERT_COOKIE environment variable.
	ConvertCookieSettingKey = "sub2api_convert_cookie"

	defaultClaudeSKConvertTimeout = 15 * time.Second
)

// convertCookieProvider is an optional settings-backed source for the converter
// cookie. It is wired in ProvideSettingService so it stays regen-safe.
var convertCookieProvider SettingRepository

// SetConvertCookieProvider registers the settings repository used to resolve the
// converter cookie from the database. Safe to call with nil to disable DB lookup.
func SetConvertCookieProvider(p SettingRepository) {
	convertCookieProvider = p
}

// ResolveConvertCookie returns the converter-station cookie, preferring the value
// stored in settings (admin UI) and falling back to the SUB2API_CONVERT_COOKIE
// environment variable. Returns an empty string when neither is configured.
func ResolveConvertCookie(ctx context.Context) string {
	if convertCookieProvider != nil {
		if value, err := convertCookieProvider.GetValue(ctx, ConvertCookieSettingKey); err == nil {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return strings.TrimSpace(os.Getenv("SUB2API_CONVERT_COOKIE"))
}

var DefaultClaudeOAuthScopes = []string{"user:chat", "user:inference", "user:profile"}

type ConvertedClaudeOAuth struct {
	AccessToken      string
	RefreshToken     string
	ExpiresAtSeconds int64
	Scopes           []string
	SubscriptionType string
	// SubscriptionLabel is the converter's human-readable plan label
	// (e.g. "Claude Max 20×"). Optional — older/other converters omit it, in
	// which case callers fall back to deriving a label from SubscriptionType.
	SubscriptionLabel string
	Email             string
}

const (
	ClaudeSKConvertKindTemporary        = "sk_converter_temporary"
	ClaudeSKConvertKindCookieInvalid    = "sk_converter_cookie_invalid"
	ClaudeSKConvertKindCloudflare       = "sk_converter_cloudflare"
	ClaudeSKConvertKindSourceSKInvalid  = "sk_source_invalid"
	ClaudeSKConvertKindAccountBlocked   = "claude_account_blocked"
	ClaudeSKConvertKindSubscription     = "claude_subscription_unavailable"
	ClaudeSKConvertKindInvalidResponse  = "sk_converter_invalid_response"
	ClaudeSKConvertKindConfiguration    = "sk_converter_configuration"
	ClaudeSKConvertKindRequestMalformed = "sk_converter_request_malformed"
)

type ClaudeSKConvertError struct {
	Kind               string
	Message            string
	StatusCode         int
	BodySnippet        string
	Retryable          bool
	NeedsCookieRefresh bool
}

func (e *ClaudeSKConvertError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{"Claude SK convert failed", "kind=" + e.Kind}
	if e.StatusCode > 0 {
		parts = append(parts, "status="+strconv.Itoa(e.StatusCode))
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	if e.BodySnippet != "" {
		parts = append(parts, "body="+e.BodySnippet)
	}
	if e.NeedsCookieRefresh {
		parts = append(parts, "action=verify SUB2API_CONVERT_URL and update SUB2API_CONVERT_COOKIE or paste a fresh converter cookie")
	}
	return strings.Join(parts, "; ")
}

func (e *ClaudeSKConvertError) UserMessage() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case ClaudeSKConvertKindCookieInvalid:
		return "转换站 Cookie 已失效或未登录，需要更新 SUB2API_CONVERT_COOKIE / 重新粘贴转换站 Cookie"
	case ClaudeSKConvertKindCloudflare:
		return "转换站被 Cloudflare 拦截，需要更新 cf_clearance 或重新获取转换站 Cookie"
	case ClaudeSKConvertKindSourceSKInvalid:
		return "原始 SK 已失效或格式不正确，需要更换这个账号的 sk-ant-sid02"
	case ClaudeSKConvertKindAccountBlocked:
		return "Claude 账号不可用，可能被封禁、吊销或安全风控，需要更换 SK/账号"
	case ClaudeSKConvertKindSubscription:
		return "Claude 账号订阅或权限不可用，当前 SK 无法转换为可用令牌"
	case ClaudeSKConvertKindTemporary:
		return "转换站临时不可用或网络超时，系统会稍后重试"
	case ClaudeSKConvertKindInvalidResponse:
		return "转换站响应格式异常，没有返回 access_token / refresh_token"
	case ClaudeSKConvertKindConfiguration:
		return "服务器未配置转换站 Cookie，请配置 SUB2API_CONVERT_COOKIE 或临时粘贴 Cookie"
	default:
		return e.Message
	}
}

func ConvertClaudeSKFromEnv(ctx context.Context, sk string) (ConvertedClaudeOAuth, error) {
	cookie := ResolveConvertCookie(ctx)
	if cookie == "" {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:               ClaudeSKConvertKindConfiguration,
			Message:            "converter cookie is required; set SUB2API_CONVERT_URL and SUB2API_CONVERT_COOKIE",
			Retryable:          false,
			NeedsCookieRefresh: true,
		}
	}
	return ConvertClaudeSK(ctx, sk, cookie)
}

func ConvertClaudeSK(ctx context.Context, sk, cookie string) (ConvertedClaudeOAuth, error) {
	sk = strings.TrimSpace(sk)
	if sk == "" {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:      ClaudeSKConvertKindRequestMalformed,
			Message:   "sk is required",
			Retryable: false,
		}
	}
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:               ClaudeSKConvertKindConfiguration,
			Message:            "converter cookie is required; set SUB2API_CONVERT_URL and SUB2API_CONVERT_COOKIE",
			Retryable:          false,
			NeedsCookieRefresh: true,
		}
	}

	convertURL := strings.TrimSpace(os.Getenv("SUB2API_CONVERT_URL"))
	if convertURL == "" {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:               ClaudeSKConvertKindConfiguration,
			Message:            "converter URL is required; set SUB2API_CONVERT_URL to your own trusted converter endpoint",
			Retryable:          false,
			NeedsCookieRefresh: true,
		}
	}
	parsedConvertURL, err := url.Parse(convertURL)
	if err != nil || parsedConvertURL.Scheme == "" || parsedConvertURL.Host == "" || (parsedConvertURL.Scheme != "http" && parsedConvertURL.Scheme != "https") {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:      ClaudeSKConvertKindConfiguration,
			Message:   "converter URL is invalid; set SUB2API_CONVERT_URL to http(s)://HOST/PATH",
			Retryable: false,
		}
	}
	timeout := defaultClaudeSKConvertTimeout
	if raw := strings.TrimSpace(os.Getenv("SUB2API_CONVERT_TIMEOUT_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	body, _ := json.Marshal(map[string]string{"cookie": sk})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedConvertURL.String(), bytes.NewReader(body))
	if err != nil {
		return ConvertedClaudeOAuth{}, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	converterOrigin := parsedConvertURL.Scheme + "://" + parsedConvertURL.Host
	req.Header.Set("Origin", converterOrigin)
	req.Header.Set("Referer", converterOrigin+"/dashboard/convert")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:      ClaudeSKConvertKindTemporary,
			Message:   "request converter failed: " + err.Error(),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ConvertedClaudeOAuth{}, classifyClaudeSKConvertHTTPError(resp.StatusCode, string(respBody))
	}

	var raw map[string]any
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return ConvertedClaudeOAuth{}, &ClaudeSKConvertError{
			Kind:        ClaudeSKConvertKindInvalidResponse,
			Message:     "parse convert response: " + err.Error(),
			BodySnippet: truncateForError(string(respBody), 240),
			Retryable:   true,
		}
	}

	converted, err := ParseConvertedClaudeOAuth(raw)
	if err != nil {
		return ConvertedClaudeOAuth{}, classifyClaudeSKConvertParseError(raw, string(respBody), err)
	}
	return converted, nil
}

func BuildClaudeCredentialsFromConverted(converted ConvertedClaudeOAuth) map[string]any {
	nowSeconds := time.Now().Unix()
	expiresAt := converted.ExpiresAtSeconds
	expiresIn := int64(0)
	if expiresAt > 0 && expiresAt > nowSeconds {
		expiresIn = expiresAt - nowSeconds
	}
	scopes := converted.Scopes
	if len(scopes) == 0 {
		scopes = DefaultClaudeOAuthScopes
	}

	credentials := map[string]any{
		"access_token":  converted.AccessToken,
		"expires_at":    strconv.FormatInt(expiresAt, 10),
		"expires_in":    strconv.FormatInt(expiresIn, 10),
		"refresh_token": converted.RefreshToken,
		"scope":         strings.Join(scopes, " "),
		"token_type":    "Bearer",
	}
	if converted.Email != "" {
		credentials["email_address"] = converted.Email
	}
	return credentials
}

func IsClaudeSKImportAccount(account *Account) bool {
	if account == nil {
		return false
	}
	return account.Platform == PlatformAnthropic &&
		account.Type == AccountTypeOAuth &&
		account.GetExtraString("source") == ClaudeSKImportSource &&
		strings.TrimSpace(account.GetCredential(ClaudeSKCookieCredentialKey)) != ""
}

func IsClaudeSKConvertTemporaryError(err error) bool {
	var convertErr *ClaudeSKConvertError
	return errors.As(err, &convertErr) && convertErr.Retryable
}

func IsClaudeSKConvertCookieError(err error) bool {
	var convertErr *ClaudeSKConvertError
	return errors.As(err, &convertErr) && convertErr.NeedsCookieRefresh
}

func classifyClaudeSKConvertHTTPError(statusCode int, body string) *ClaudeSKConvertError {
	bodySnippet := truncateForError(body, 500)
	lower := strings.ToLower(body)
	kind := ClaudeSKConvertKindTemporary
	message := "转换站临时不可用"
	retryable := true
	needsCookie := false

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		kind = ClaudeSKConvertKindCookieInvalid
		message = "转换站 Cookie 无效、过期或未登录"
		retryable = false
		needsCookie = true
	case statusCode == http.StatusTooManyRequests:
		kind = ClaudeSKConvertKindTemporary
		message = "转换站限流，稍后会自动重试"
		retryable = true
	case statusCode >= 500:
		kind = ClaudeSKConvertKindTemporary
		message = "转换站服务异常，稍后会自动重试"
		retryable = true
	case strings.Contains(lower, "cloudflare") || strings.Contains(lower, "cf_clearance") || strings.Contains(lower, "captcha") || strings.Contains(lower, "just a moment"):
		kind = ClaudeSKConvertKindCloudflare
		message = "转换站 Cloudflare 校验失败"
		retryable = false
		needsCookie = true
	case skContainsAny(lower, "invalid cookie", "cookie invalid", "not login", "not logged", "unauthorized", "未登录", "登录过期", "cookie过期"):
		kind = ClaudeSKConvertKindCookieInvalid
		message = "转换站 Cookie 无效、过期或未登录"
		retryable = false
		needsCookie = true
	case skContainsAny(lower, "invalid sk", "invalid sid", "sk invalid", "sid invalid", "cookie format", "无效sk", "sk无效", "sid无效", "账号凭据无效"):
		kind = ClaudeSKConvertKindSourceSKInvalid
		message = "原始 SK 无效或已过期"
		retryable = false
	case skContainsAny(lower, "banned", "suspended", "disabled", "revoked", "deactivated", "封禁", "被封", "吊销", "停用"):
		kind = ClaudeSKConvertKindAccountBlocked
		message = "Claude 账号不可用，可能被封禁或吊销"
		retryable = false
	case skContainsAny(lower, "subscription", "entitlement", "not allowed", "no active", "plan", "订阅", "权限", "无权限"):
		kind = ClaudeSKConvertKindSubscription
		message = "Claude 账号订阅或权限不可用"
		retryable = false
	case statusCode >= 400 && statusCode < 500:
		kind = ClaudeSKConvertKindSourceSKInvalid
		message = "转换站拒绝该 SK，可能原始 SK 已失效"
		retryable = false
	}

	return &ClaudeSKConvertError{
		Kind:               kind,
		Message:            message,
		StatusCode:         statusCode,
		BodySnippet:        bodySnippet,
		Retryable:          retryable,
		NeedsCookieRefresh: needsCookie,
	}
}

func classifyClaudeSKConvertParseError(raw map[string]any, body string, cause error) *ClaudeSKConvertError {
	bodySnippet := truncateForError(body, 500)
	text := strings.ToLower(body)
	message := strings.TrimSpace(firstString(
		getString(raw, "message"),
		getString(raw, "error"),
		getString(raw, "detail"),
		getString(raw, "msg"),
	))
	if message != "" {
		text += " " + strings.ToLower(message)
	}

	kind := ClaudeSKConvertKindInvalidResponse
	retryable := true
	needsCookie := false
	userMessage := "转换站响应格式异常，没有返回 access_token / refresh_token"
	switch {
	case skContainsAny(text, "cloudflare", "cf_clearance", "captcha", "just a moment"):
		kind = ClaudeSKConvertKindCloudflare
		retryable = false
		needsCookie = true
		userMessage = "转换站 Cloudflare 校验失败"
	case skContainsAny(text, "invalid cookie", "cookie invalid", "not login", "not logged", "unauthorized", "未登录", "登录过期", "cookie过期"):
		kind = ClaudeSKConvertKindCookieInvalid
		retryable = false
		needsCookie = true
		userMessage = "转换站 Cookie 无效、过期或未登录"
	case skContainsAny(text, "invalid sk", "invalid sid", "sk invalid", "sid invalid", "无效sk", "sk无效", "sid无效"):
		kind = ClaudeSKConvertKindSourceSKInvalid
		retryable = false
		userMessage = "原始 SK 无效或已过期"
	case skContainsAny(text, "banned", "suspended", "disabled", "revoked", "deactivated", "封禁", "被封", "吊销", "停用"):
		kind = ClaudeSKConvertKindAccountBlocked
		retryable = false
		userMessage = "Claude 账号不可用，可能被封禁或吊销"
	case skContainsAny(text, "subscription", "entitlement", "not allowed", "no active", "plan", "订阅", "权限", "无权限"):
		kind = ClaudeSKConvertKindSubscription
		retryable = false
		userMessage = "Claude 账号订阅或权限不可用"
	}
	if message == "" && cause != nil {
		message = cause.Error()
	}
	if userMessage != "" {
		message = userMessage + ": " + message
	}
	return &ClaudeSKConvertError{
		Kind:               kind,
		Message:            strings.TrimSpace(message),
		BodySnippet:        bodySnippet,
		Retryable:          retryable,
		NeedsCookieRefresh: needsCookie,
	}
}

func ParseConvertedClaudeOAuth(raw map[string]any) (ConvertedClaudeOAuth, error) {
	token := getMap(raw, "token_json", "claudeAiOauth")
	accessToken := firstString(
		getString(token, "accessToken"),
		getString(token, "access_token"),
		getString(raw, "access_token"),
	)
	refreshToken := firstString(
		getString(token, "refreshToken"),
		getString(token, "refresh_token"),
		getString(raw, "refresh_token"),
	)
	if accessToken == "" || refreshToken == "" {
		return ConvertedClaudeOAuth{}, errors.New("convert response missing access_token or refresh_token")
	}

	expiresAt, _ := coerceUnixSeconds(firstAny(token["expiresAt"], token["expires_at"], raw["expires_at"]))
	scopes := coerceStringSlice(token["scopes"])
	if len(scopes) == 0 {
		scopes = DefaultClaudeOAuthScopes
	}

	return ConvertedClaudeOAuth{
		AccessToken:       accessToken,
		RefreshToken:      refreshToken,
		ExpiresAtSeconds:  expiresAt,
		Scopes:            scopes,
		SubscriptionType:  firstString(getString(token, "subscriptionType"), getString(raw, "subscription_type"), "default_claude_ai"),
		SubscriptionLabel: getString(raw, "subscription_label"),
		Email:             strings.TrimSpace(getString(raw, "email")),
	}, nil
}

func getMap(raw map[string]any, path ...string) map[string]any {
	current := raw
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func getString(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	switch v := raw[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		return ""
	}
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func coerceUnixSeconds(value any) (int64, bool) {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return normalizeUnixSeconds(parsed), true
		}
		if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return t.Unix(), true
		}
	case json.Number:
		if parsed, err := v.Int64(); err == nil {
			return normalizeUnixSeconds(parsed), true
		}
	case float64:
		return normalizeUnixSeconds(int64(v)), true
	case int64:
		return normalizeUnixSeconds(v), true
	case int:
		return normalizeUnixSeconds(int64(v)), true
	}
	return 0, false
}

func normalizeUnixSeconds(value int64) int64 {
	if value > 99999999999 {
		return value / 1000
	}
	return value
}

func coerceStringSlice(value any) []string {
	switch v := value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return v
	case string:
		return strings.Fields(v)
	default:
		return nil
	}
}

func skContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func truncateForError(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}
