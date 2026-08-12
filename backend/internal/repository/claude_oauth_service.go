package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"

	"github.com/imroc/req/v3"
)

func NewClaudeOAuthClient() service.ClaudeOAuthClient {
	return &claudeOAuthService{
		baseURL:       "https://claude.ai",
		tokenURL:      oauth.TokenURL,
		clientFactory: createReqClient,
	}
}

type claudeOAuthService struct {
	baseURL       string
	tokenURL      string
	clientFactory func(proxyURL string) (*req.Client, error)
}

func (s *claudeOAuthService) GetOrganizationUUID(ctx context.Context, sessionKey, proxyURL string) (string, error) {
	cookies, err := parseClaudeSessionCookies(sessionKey)
	if err != nil {
		return "", err
	}

	client, err := s.clientFactory(proxyURL)
	if err != nil {
		return "", claudeSessionRequestFailed("organization lookup", err)
	}

	var orgs []struct {
		UUID      string  `json:"uuid"`
		Name      string  `json:"name"`
		RavenType *string `json:"raven_type"` // nil for personal, "team" for team organization
	}

	targetURL := s.baseURL + "/api/organizations"
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1: Getting organization UUID from %s", targetURL)

	resp, err := client.R().
		SetContext(ctx).
		SetCookies(cookies...).
		SetSuccessResult(&orgs).
		Get(targetURL)

	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 FAILED - Request error: %v", err)
		return "", claudeSessionRequestFailed("organization lookup", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 Response - Status: %d", resp.StatusCode)

	if !resp.IsSuccessState() {
		return "", claudeSessionResponseError("organization lookup", resp.StatusCode)
	}

	if len(orgs) == 0 {
		return "", infraerrors.BadRequest(
			"CLAUDE_SESSION_ORGANIZATION_MISSING",
			"No Claude organization is available for this browser session.",
		)
	}

	// 如果只有一个组织，直接使用
	if len(orgs) == 1 {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - Single org found, UUID: %s, Name: %s", orgs[0].UUID, orgs[0].Name)
		return orgs[0].UUID, nil
	}

	// 如果有多个组织，优先选择 raven_type 为 "team" 的组织
	for _, org := range orgs {
		if org.RavenType != nil && *org.RavenType == "team" {
			logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - Selected team org, UUID: %s, Name: %s, RavenType: %s",
				org.UUID, org.Name, *org.RavenType)
			return org.UUID, nil
		}
	}

	// 如果没有 team 类型的组织，使用第一个
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - No team org found, using first org, UUID: %s, Name: %s", orgs[0].UUID, orgs[0].Name)
	return orgs[0].UUID, nil
}

func (s *claudeOAuthService) GetAuthorizationCode(ctx context.Context, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL string) (string, error) {
	cookies, err := parseClaudeSessionCookies(sessionKey)
	if err != nil {
		return "", err
	}

	client, err := s.clientFactory(proxyURL)
	if err != nil {
		return "", claudeSessionRequestFailed("authorization", err)
	}

	authURL := fmt.Sprintf("%s/v1/oauth/%s/authorize", s.baseURL, orgUUID)

	reqBody := map[string]any{
		"response_type":         "code",
		"client_id":             oauth.ClientID,
		"organization_uuid":     orgUUID,
		"redirect_uri":          oauth.RedirectURI,
		"scope":                 scope,
		"state":                 state,
		"code_challenge":        codeChallenge,
		"code_challenge_method": "S256",
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2: Getting authorization code from %s", authURL)
	reqBodyJSON, _ := json.Marshal(logredact.RedactMap(reqBody))
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 Request Body: %s", string(reqBodyJSON))

	var result struct {
		RedirectURI string `json:"redirect_uri"`
	}

	resp, err := client.R().
		SetContext(ctx).
		SetCookies(cookies...).
		SetHeader("Accept", "application/json").
		SetHeader("Accept-Language", "en-US,en;q=0.9").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("Origin", "https://claude.ai").
		SetHeader("Referer", "https://claude.ai/new").
		SetHeader("Content-Type", "application/json").
		SetBody(reqBody).
		SetSuccessResult(&result).
		Post(authURL)

	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 FAILED - Request error: %v", err)
		return "", claudeSessionRequestFailed("authorization", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 Response - Status: %d, Body: %s", resp.StatusCode, logredact.RedactJSON(resp.Bytes()))

	if !resp.IsSuccessState() {
		return "", claudeSessionResponseError("authorization", resp.StatusCode)
	}

	if result.RedirectURI == "" {
		return "", claudeSessionInvalidResponse("authorization response did not include redirect_uri")
	}

	parsedURL, err := url.Parse(result.RedirectURI)
	if err != nil {
		return "", claudeSessionInvalidResponse("authorization response contained an invalid redirect_uri").WithCause(err)
	}

	queryParams := parsedURL.Query()
	authCode := queryParams.Get("code")
	responseState := queryParams.Get("state")

	if authCode == "" {
		return "", claudeSessionInvalidResponse("authorization response did not include a code")
	}
	if responseState != "" && responseState != state {
		return "", infraerrors.BadRequest(
			"CLAUDE_SESSION_STATE_MISMATCH",
			"Claude returned an OAuth state that does not match this authorization attempt.",
		)
	}

	fullCode := authCode
	if responseState != "" {
		fullCode = authCode + "#" + responseState
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 SUCCESS - Got authorization code")
	return fullCode, nil
}

func parseClaudeSessionCookies(input string) ([]*http.Cookie, error) {
	raw := strings.TrimSpace(input)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return nil, invalidClaudeSessionCookieFormat()
	}

	if len(raw) >= len("cookie:") && strings.EqualFold(raw[:len("cookie:")], "cookie:") {
		raw = strings.TrimSpace(raw[len("cookie:"):])
	}

	// Copying the Value column remains the common path. Header-style input is
	// also accepted so Cloudflare cookies can accompany sessionKey when needed.
	if !strings.Contains(raw, "=") || strings.HasPrefix(raw, "sk-ant-sid") {
		value := strings.TrimSpace(strings.Trim(raw, `"`))
		if value == "" {
			return nil, invalidClaudeSessionCookieFormat()
		}
		return []*http.Cookie{{Name: "sessionKey", Value: value}}, nil
	}

	dummy := &http.Request{Header: make(http.Header)}
	dummy.Header.Set("Cookie", raw)
	cookies := dummy.Cookies()
	foundSessionKey := false
	for _, cookie := range cookies {
		if strings.EqualFold(cookie.Name, "sessionKey") && strings.TrimSpace(cookie.Value) != "" {
			cookie.Name = "sessionKey"
			foundSessionKey = true
		}
	}
	if !foundSessionKey {
		return nil, invalidClaudeSessionCookieFormat()
	}
	return cookies, nil
}

func invalidClaudeSessionCookieFormat() error {
	return infraerrors.BadRequest(
		"CLAUDE_SESSION_COOKIE_FORMAT_INVALID",
		"Paste the sessionKey value, sessionKey=VALUE, or a Cookie header containing sessionKey.",
	)
}

func claudeSessionRequestFailed(stage string, cause error) error {
	return infraerrors.ServiceUnavailable(
		"CLAUDE_SESSION_REQUEST_FAILED",
		"Claude browser-session "+stage+" failed. Check the account proxy and try again.",
	).WithCause(cause)
}

func claudeSessionInvalidResponse(detail string) *infraerrors.ApplicationError {
	return infraerrors.ServiceUnavailable(
		"CLAUDE_SESSION_UPSTREAM_ERROR",
		"Claude returned an invalid browser-session response.",
	).WithCause(fmt.Errorf("%s", detail))
}

func claudeSessionResponseError(stage string, statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized:
		// A 401 here belongs to Claude, not to the admin session. Keep it a 400
		// so the frontend auth interceptor does not log the administrator out.
		return infraerrors.BadRequest(
			"CLAUDE_SESSION_COOKIE_INVALID",
			"The Claude sessionKey is invalid or expired. Log in to claude.ai again and copy the latest value.",
		)
	case http.StatusForbidden:
		return infraerrors.Forbidden(
			"CLAUDE_SESSION_REQUEST_BLOCKED",
			"Claude rejected the browser session. Use the same proxy exit as the browser, or paste the full Cookie header.",
		)
	default:
		return infraerrors.ServiceUnavailable(
			"CLAUDE_SESSION_UPSTREAM_ERROR",
			fmt.Sprintf("Claude browser-session %s returned HTTP %d.", stage, statusCode),
		)
	}
}

func (s *claudeOAuthService) ExchangeCodeForToken(ctx context.Context, code, codeVerifier, state, proxyURL string, isSetupToken bool) (*oauth.TokenResponse, error) {
	client, err := s.clientFactory(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}

	// Parse code which may contain state in format "authCode#state"
	authCode := code
	codeState := ""
	if idx := strings.Index(code, "#"); idx != -1 {
		authCode = code[:idx]
		codeState = code[idx+1:]
	}

	reqBody := map[string]any{
		"code":          authCode,
		"grant_type":    "authorization_code",
		"client_id":     oauth.ClientID,
		"redirect_uri":  oauth.RedirectURI,
		"code_verifier": codeVerifier,
	}

	if codeState != "" {
		reqBody["state"] = codeState
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3: Exchanging code for token at %s", s.tokenURL)
	reqBodyJSON, _ := json.Marshal(logredact.RedactMap(reqBody))
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 Request Body: %s", string(reqBodyJSON))

	var tokenResp oauth.TokenResponse

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "axios/1.13.6").
		SetBody(reqBody).
		SetSuccessResult(&tokenResp).
		Post(s.tokenURL)

	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 FAILED - Request error: %v", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 Response - Status: %d, Body: %s", resp.StatusCode, logredact.RedactJSON(resp.Bytes()))

	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("token exchange failed: status %d, body: %s", resp.StatusCode, resp.String())
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 SUCCESS - Got access token")
	return &tokenResp, nil
}

func (s *claudeOAuthService) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*oauth.TokenResponse, error) {
	client, err := s.clientFactory(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}

	reqBody := map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     oauth.ClientID,
	}

	var tokenResp oauth.TokenResponse

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "axios/1.13.6").
		SetBody(reqBody).
		SetSuccessResult(&tokenResp).
		Post(s.tokenURL)

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("token refresh failed: status %d, body: %s", resp.StatusCode, resp.String())
	}

	return &tokenResp, nil
}

func createReqClient(proxyURL string) (*req.Client, error) {
	// 禁用 CookieJar，确保每次授权都是干净的会话
	client := req.C().
		SetTimeout(60 * time.Second).
		ImpersonateChrome().
		SetCookieJar(nil) // 禁用 CookieJar

	trimmed, _, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if trimmed != "" {
		client.SetProxyURL(trimmed)
	}

	return instrumentReqClient(client), nil
}
