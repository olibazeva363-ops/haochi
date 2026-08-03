//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTestServiceClaudeSKImport401RefreshesAndRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldURL := os.Getenv("SUB2API_CONVERT_URL")
	oldCookie := os.Getenv("SUB2API_CONVERT_COOKIE")
	defer func() {
		_ = os.Setenv("SUB2API_CONVERT_URL", oldURL)
		_ = os.Setenv("SUB2API_CONVERT_COOKIE", oldCookie)
	}()

	expiresAt := time.Now().Add(8 * time.Hour).Unix()
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "converter-cookie", r.Header.Get("Cookie"))
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"email":"claude@example.com",
			"token_json":{"claudeAiOauth":{
				"accessToken":"new-access",
				"refreshToken":"new-refresh",
				"expiresAt":%d
			}}
		}`, expiresAt)))
	}))
	defer converter.Close()
	_ = os.Setenv("SUB2API_CONVERT_URL", converter.URL)
	_ = os.Setenv("SUB2API_CONVERT_COOKIE", "converter-cookie")

	account := &Account{
		ID:          77,
		Name:        "claude-sk",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Status:      StatusError,
		Schedulable: false,
		Concurrency: 1,
		Extra: map[string]any{
			"source": ClaudeSKImportSource,
		},
		Credentials: map[string]any{
			"access_token":              "old-access",
			"refresh_token":             "old-refresh",
			ClaudeSKCookieCredentialKey: "sk-ant-sid02-original",
		},
	}
	repo := &claudeSKAccountTestRecoveryRepo{account: account}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"OAuth access token has been revoked."}}`),
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n")),
		},
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/77/test", nil)

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-5", "", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "Bearer old-access", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "Bearer new-access", upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "new-access", account.Credentials["access_token"])
	require.Equal(t, "new-refresh", account.Credentials["refresh_token"])
	require.Equal(t, "sk-ant-sid02-original", account.Credentials[ClaudeSKCookieCredentialKey])
	require.Equal(t, 1, repo.clearErrorCalls)
	require.Equal(t, 1, repo.setSchedulableCalls)
	require.Contains(t, rec.Body.String(), "SK token refreshed after 401")
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestServiceClaudeSKImport401PermanentConvertErrorSetsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldURL := os.Getenv("SUB2API_CONVERT_URL")
	oldCookie := os.Getenv("SUB2API_CONVERT_COOKIE")
	defer func() {
		_ = os.Setenv("SUB2API_CONVERT_URL", oldURL)
		_ = os.Setenv("SUB2API_CONVERT_COOKIE", oldCookie)
	}()

	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid sk"}`))
	}))
	defer converter.Close()
	_ = os.Setenv("SUB2API_CONVERT_URL", converter.URL)
	_ = os.Setenv("SUB2API_CONVERT_COOKIE", "converter-cookie")

	account := &Account{
		ID:          78,
		Name:        "claude-sk-dead",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra:       map[string]any{"source": ClaudeSKImportSource},
		Credentials: map[string]any{
			"access_token":              "old-access",
			ClaudeSKCookieCredentialKey: "sk-ant-sid02-dead",
		},
	}
	repo := &claudeSKAccountTestRecoveryRepo{account: account}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"OAuth access token has been revoked."}}`),
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/78/test", nil)

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-5", "", AccountTestModeDefault)

	require.Error(t, err)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Zero(t, repo.setTempCalls)
	require.Contains(t, rec.Body.String(), "SK auto-refresh failed")
}

type claudeSKAccountTestRecoveryRepo struct {
	mockAccountRepoForGemini
	account                *Account
	updateCredentialsCalls int
	clearTempCalls         int
	clearErrorCalls        int
	setSchedulableCalls    int
	setErrorCalls          int
	setTempCalls           int
}

func (r *claudeSKAccountTestRecoveryRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account != nil && r.account.ID == id {
		return r.account, nil
	}
	return nil, fmt.Errorf("account not found")
}

func (r *claudeSKAccountTestRecoveryRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	if r.account == nil || r.account.ID != id {
		return fmt.Errorf("unexpected account id %d", id)
	}
	r.updateCredentialsCalls++
	r.account.Credentials = credentials
	return nil
}

func (r *claudeSKAccountTestRecoveryRepo) ClearTempUnschedulable(context.Context, int64) error {
	r.clearTempCalls++
	return nil
}

func (r *claudeSKAccountTestRecoveryRepo) ClearError(context.Context, int64) error {
	r.clearErrorCalls++
	return nil
}

func (r *claudeSKAccountTestRecoveryRepo) SetSchedulable(context.Context, int64, bool) error {
	r.setSchedulableCalls++
	return nil
}

func (r *claudeSKAccountTestRecoveryRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

func (r *claudeSKAccountTestRecoveryRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.setTempCalls++
	return nil
}
