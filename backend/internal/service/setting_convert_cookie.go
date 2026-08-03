package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
)

// ConvertCookieStatus describes the currently effective converter-station cookie
// without ever exposing the raw cookie value. It is safe to return to the admin UI.
type ConvertCookieStatus struct {
	// Configured reports whether any cookie is effective (DB setting or env var).
	Configured bool `json:"configured"`
	// Source is one of "db" (set from admin UI), "env" (SUB2API_CONVERT_COOKIE
	// fallback) or "none".
	Source string `json:"source"`
	// Length is the character length of the effective cookie (debug aid only).
	Length int `json:"length,omitempty"`
	// Email / UserID / ExpiresAt are parsed from the auth_token JWT inside the
	// cookie when present, purely for display. They are best-effort.
	Email     string `json:"email,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	// HasAuthToken reports whether an auth_token segment was found in the cookie.
	HasAuthToken bool `json:"has_auth_token"`
}

// GetConvertCookieStatus resolves the effective converter cookie and returns a
// sanitized status (never the raw cookie). DB setting wins over the env fallback.
func (s *SettingService) GetConvertCookieStatus(ctx context.Context) ConvertCookieStatus {
	status := ConvertCookieStatus{Source: "none"}

	var cookie string
	if s.settingRepo != nil {
		if value, err := s.settingRepo.GetValue(ctx, ConvertCookieSettingKey); err == nil {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				cookie = trimmed
				status.Source = "db"
			}
		}
	}
	if cookie == "" {
		if envCookie := strings.TrimSpace(os.Getenv("SUB2API_CONVERT_COOKIE")); envCookie != "" {
			cookie = envCookie
			status.Source = "env"
		}
	}
	if cookie == "" {
		return status
	}

	status.Configured = true
	status.Length = len(cookie)
	if email, userID, exp, ok := parseConvertCookieIdentity(cookie); ok {
		status.HasAuthToken = true
		status.Email = email
		status.UserID = userID
		status.ExpiresAt = exp
	}
	return status
}

// SetConvertCookie persists the converter-station cookie to settings. The value
// takes effect immediately for all conversion paths (import, probe, background
// re-convert) with no restart required.
func (s *SettingService) SetConvertCookie(ctx context.Context, cookie string) (ConvertCookieStatus, error) {
	cookie = strings.TrimSpace(cookie)
	if err := s.settingRepo.Set(ctx, ConvertCookieSettingKey, cookie); err != nil {
		return ConvertCookieStatus{}, err
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return s.GetConvertCookieStatus(ctx), nil
}

// ClearConvertCookie removes the stored converter cookie, falling back to the
// SUB2API_CONVERT_COOKIE environment variable (if any).
func (s *SettingService) ClearConvertCookie(ctx context.Context) (ConvertCookieStatus, error) {
	if err := s.settingRepo.Delete(ctx, ConvertCookieSettingKey); err != nil {
		return ConvertCookieStatus{}, err
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return s.GetConvertCookieStatus(ctx), nil
}

// parseConvertCookieIdentity best-effort extracts email / user_id / exp from the
// auth_token JWT embedded in the converter cookie string. Signature is NOT
// verified — this is display metadata only.
func parseConvertCookieIdentity(cookie string) (email, userID string, exp int64, ok bool) {
	token := extractCookieValue(cookie, "auth_token")
	if token == "" {
		return "", "", 0, false
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", "", 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some encoders pad; try standard decoding as a fallback.
		if payload, err = base64.StdEncoding.DecodeString(parts[1]); err != nil {
			return "", "", 0, false
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", 0, false
	}

	email = firstString(
		getString(claims, "email"),
		getString(claims, "user_email"),
	)
	userID = firstString(
		getString(claims, "user_id"),
		getString(claims, "uid"),
		getString(claims, "sub"),
	)
	if v, found := coerceUnixSeconds(claims["exp"]); found {
		exp = v
	}
	return email, userID, exp, true
}

// (asString is defined in ops_system_log_sink.go and reused here.)

// extractCookieValue returns the value of the named cookie from a raw
// "k=v; k2=v2" cookie header string, or "" when absent.
func extractCookieValue(cookie, name string) string {
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(part[:eq]), name) {
			return strings.TrimSpace(part[eq+1:])
		}
	}
	return ""
}
