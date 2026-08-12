package repository

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClaudeOAuthServiceSuite struct {
	suite.Suite
	client *claudeOAuthService
}

// requestCapture holds captured request data for assertions in the main goroutine.
type requestCapture struct {
	path        string
	method      string
	cookies     []*http.Cookie
	body        []byte
	bodyJSON    map[string]any
	contentType string
}

func newTestReqClient(rt http.RoundTripper) *req.Client {
	c := req.C()
	c.GetClient().Transport = rt
	return c
}

func (s *ClaudeOAuthServiceSuite) TestGetOrganizationUUID() {
	tests := []struct {
		name         string
		sessionInput string
		handler      http.HandlerFunc
		wantErr      bool
		errContain   string
		wantUUID     string
		validate     func(captured requestCapture)
	}{
		{
			name: "success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"uuid":"org-1"}]`))
			},
			wantUUID: "org-1",
			validate: func(captured requestCapture) {
				require.Equal(s.T(), "/api/organizations", captured.path, "unexpected path")
				require.Len(s.T(), captured.cookies, 1, "expected 1 cookie")
				require.Equal(s.T(), "sessionKey", captured.cookies[0].Name)
				require.Equal(s.T(), "sess", captured.cookies[0].Value)
			},
		},
		{
			name:         "forwards full cookie header",
			sessionInput: "sessionKey=sess; cf_clearance=clearance",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"uuid":"org-1"}]`))
			},
			wantUUID: "org-1",
			validate: func(captured requestCapture) {
				cookies := make(map[string]string, len(captured.cookies))
				for _, cookie := range captured.cookies {
					cookies[cookie.Name] = cookie.Value
				}
				require.Equal(s.T(), map[string]string{
					"sessionKey":   "sess",
					"cf_clearance": "clearance",
				}, cookies)
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
			},
			wantErr:    true,
			errContain: "CLAUDE_SESSION_COOKIE_INVALID",
		},
		{
			name: "reports_stale_session",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"details":{"error_code":"session_stale_relogin"}}}`))
			},
			wantErr:    true,
			errContain: "CLAUDE_SESSION_RELOGIN_REQUIRED",
		},
		{
			name: "invalid_json_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("not-json"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.path = r.URL.Path
				captured.cookies = r.Cookies()
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.baseURL = "http://in-process"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			sessionInput := tt.sessionInput
			if sessionInput == "" {
				sessionInput = "sess"
			}
			got, err := s.client.GetOrganizationUUID(context.Background(), sessionInput, "")

			if tt.wantErr {
				require.Error(s.T(), err)
				if tt.errContain != "" {
					require.ErrorContains(s.T(), err, tt.errContain)
				}
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantUUID, got)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestGetAuthorizationCode() {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    bool
		errContain string
		wantCode   string
		validate   func(captured requestCapture)
	}{
		{
			name: "parses_redirect_uri",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"redirect_uri": oauth.RedirectURI + "?code=AUTH&state=st",
				})
			},
			wantCode: "AUTH#st",
			validate: func(captured requestCapture) {
				require.True(s.T(), strings.HasPrefix(captured.path, "/v1/oauth/") && strings.HasSuffix(captured.path, "/authorize"), "unexpected path: %s", captured.path)
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				require.Len(s.T(), captured.cookies, 1, "expected 1 cookie")
				require.Equal(s.T(), "sess", captured.cookies[0].Value)
				require.Equal(s.T(), "org-1", captured.bodyJSON["organization_uuid"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
				require.Equal(s.T(), oauth.RedirectURI, captured.bodyJSON["redirect_uri"])
				require.Equal(s.T(), "st", captured.bodyJSON["state"])
			},
		},
		{
			name: "missing_code_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"redirect_uri": oauth.RedirectURI + "?state=STATE", // no code
				})
			},
			wantErr: true,
		},
		{
			name: "rejects_mismatched_state",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{
					"redirect_uri": oauth.RedirectURI + "?code=AUTH&state=OTHER",
				})
			},
			wantErr: true,
		},
		{
			name: "reports stale session",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"details":{"error_code":"session_stale_relogin"}}}`))
			},
			wantErr:    true,
			errContain: "CLAUDE_SESSION_RELOGIN_REQUIRED",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.path = r.URL.Path
				captured.method = r.Method
				captured.cookies = r.Cookies()
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.baseURL = "http://in-process"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			code, err := s.client.GetAuthorizationCode(context.Background(), "sess", "org-1", oauth.ScopeInference, "cc", "st", "")

			if tt.wantErr {
				require.Error(s.T(), err)
				if tt.errContain != "" {
					require.ErrorContains(s.T(), err, tt.errContain)
				}
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantCode, code)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func TestParseClaudeSessionCookies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantCookies map[string]string
		wantErr     bool
	}{
		{
			name:        "raw session key",
			input:       "sk-ant-sid02-value",
			wantCookies: map[string]string{"sessionKey": "sk-ant-sid02-value"},
		},
		{
			name:        "session key assignment",
			input:       "sessionKey=sk-ant-sid01-value",
			wantCookies: map[string]string{"sessionKey": "sk-ant-sid01-value"},
		},
		{
			name:  "full cookie header",
			input: "Cookie: sessionKey=sk-ant-sid02-value; cf_clearance=clearance; __cf_bm=bm",
			wantCookies: map[string]string{
				"sessionKey":   "sk-ant-sid02-value",
				"cf_clearance": "clearance",
				"__cf_bm":      "bm",
			},
		},
		{name: "missing session key", input: "cf_clearance=clearance", wantErr: true},
		{name: "empty quoted value", input: `""`, wantErr: true},
		{name: "rejects multiline header", input: "sessionKey=value\r\nInjected: true", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cookies, err := parseClaudeSessionCookies(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got := make(map[string]string, len(cookies))
			for _, cookie := range cookies {
				got[cookie.Name] = cookie.Value
			}
			require.Equal(t, tt.wantCookies, got)
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestExchangeCodeForToken() {
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		code         string
		isSetupToken bool
		wantErr      bool
		wantResp     *oauth.TokenResponse
		validate     func(captured requestCapture)
	}{
		{
			name: "sends_state_when_embedded",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "at",
					TokenType:    "bearer",
					ExpiresIn:    3600,
					RefreshToken: "rt",
					Scope:        "s",
				})
			},
			code:         "AUTH#STATE2",
			isSetupToken: false,
			wantResp: &oauth.TokenResponse{
				AccessToken:  "at",
				RefreshToken: "rt",
			},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				require.True(s.T(), strings.HasPrefix(captured.contentType, "application/json"), "unexpected content-type")
				require.Equal(s.T(), "AUTH", captured.bodyJSON["code"])
				require.Equal(s.T(), "STATE2", captured.bodyJSON["state"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
				require.Equal(s.T(), oauth.RedirectURI, captured.bodyJSON["redirect_uri"])
				require.Equal(s.T(), "ver", captured.bodyJSON["code_verifier"])
				// Regular OAuth should not include expires_in
				require.Nil(s.T(), captured.bodyJSON["expires_in"], "regular OAuth should not include expires_in")
			},
		},
		{
			name: "setup_token_omits_expires_in",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken: "at",
					TokenType:   "bearer",
					ExpiresIn:   31536000,
				})
			},
			code:         "AUTH",
			isSetupToken: true,
			wantResp: &oauth.TokenResponse{
				AccessToken: "at",
			},
			validate: func(captured requestCapture) {
				require.Nil(s.T(), captured.bodyJSON["expires_in"], "setup token should not include expires_in")
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("bad request"))
			},
			code:         "AUTH",
			isSetupToken: false,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.method = r.Method
				captured.contentType = r.Header.Get("Content-Type")
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.tokenURL = "http://in-process/token"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			resp, err := s.client.ExchangeCodeForToken(context.Background(), tt.code, "ver", "", "", tt.isSetupToken)

			if tt.wantErr {
				require.Error(s.T(), err)
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantResp.AccessToken, resp.AccessToken)
			require.Equal(s.T(), tt.wantResp.RefreshToken, resp.RefreshToken)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func (s *ClaudeOAuthServiceSuite) TestRefreshToken() {
	tests := []struct {
		name     string
		handler  http.HandlerFunc
		wantErr  bool
		wantResp *oauth.TokenResponse
		validate func(captured requestCapture)
	}{
		{
			name: "sends_json_format",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "new_access_token",
					TokenType:    "bearer",
					ExpiresIn:    28800,
					RefreshToken: "new_refresh_token",
					Scope:        "user:profile user:inference",
				})
			},
			wantResp: &oauth.TokenResponse{
				AccessToken:  "new_access_token",
				RefreshToken: "new_refresh_token",
			},
			validate: func(captured requestCapture) {
				require.Equal(s.T(), http.MethodPost, captured.method, "expected POST")
				// 验证使用 JSON 格式（不是 form 格式）
				require.True(s.T(), strings.HasPrefix(captured.contentType, "application/json"),
					"expected JSON content-type, got: %s", captured.contentType)
				// 验证 JSON body 内容
				require.Equal(s.T(), "refresh_token", captured.bodyJSON["grant_type"])
				require.Equal(s.T(), "rt", captured.bodyJSON["refresh_token"])
				require.Equal(s.T(), oauth.ClientID, captured.bodyJSON["client_id"])
			},
		},
		{
			name: "returns_new_refresh_token",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(oauth.TokenResponse{
					AccessToken:  "at",
					TokenType:    "bearer",
					ExpiresIn:    28800,
					RefreshToken: "rotated_rt", // Anthropic rotates refresh tokens
				})
			},
			wantResp: &oauth.TokenResponse{
				AccessToken:  "at",
				RefreshToken: "rotated_rt",
			},
		},
		{
			name: "non_200_returns_error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			var captured requestCapture

			rt := newInProcessTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured.method = r.Method
				captured.contentType = r.Header.Get("Content-Type")
				captured.body, _ = io.ReadAll(r.Body)
				_ = json.Unmarshal(captured.body, &captured.bodyJSON)
				tt.handler(w, r)
			}), nil)

			client, ok := NewClaudeOAuthClient().(*claudeOAuthService)
			require.True(s.T(), ok, "type assertion failed")
			s.client = client
			s.client.tokenURL = "http://in-process/token"
			s.client.clientFactory = func(string) (*req.Client, error) { return newTestReqClient(rt), nil }

			resp, err := s.client.RefreshToken(context.Background(), "rt", "")

			if tt.wantErr {
				require.Error(s.T(), err)
				return
			}

			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantResp.AccessToken, resp.AccessToken)
			require.Equal(s.T(), tt.wantResp.RefreshToken, resp.RefreshToken)
			if tt.validate != nil {
				tt.validate(captured)
			}
		})
	}
}

func TestClaudeOAuthServiceSuite(t *testing.T) {
	suite.Run(t, new(ClaudeOAuthServiceSuite))
}
