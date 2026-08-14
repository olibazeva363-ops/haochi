package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestGatewayRecoverClaudeSKImportTokenOn401UpdatesCredentials(t *testing.T) {
	oldURL := os.Getenv("SUB2API_CONVERT_URL")
	oldCookie := os.Getenv("SUB2API_CONVERT_COOKIE")
	defer func() {
		_ = os.Setenv("SUB2API_CONVERT_URL", oldURL)
		_ = os.Setenv("SUB2API_CONVERT_COOKIE", oldCookie)
	}()

	expiresAt := time.Now().Add(8 * time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "converter-cookie" {
			t.Fatalf("converter cookie = %q", got)
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"email":"claude@example.com",
			"token_json":{"claudeAiOauth":{
				"accessToken":"new-access",
				"refreshToken":"new-refresh",
				"expiresAt":%d,
				"scopes":["user:chat"],
				"subscriptionType":"pro"
			}}
		}`, expiresAt)))
	}))
	defer server.Close()
	_ = os.Setenv("SUB2API_CONVERT_URL", server.URL)
	_ = os.Setenv("SUB2API_CONVERT_COOKIE", "converter-cookie")

	repo := &skGatewayRecoveryRepo{
		account: &Account{
			ID:       51,
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Extra: map[string]any{
				"source": ClaudeSKImportSource,
			},
			Credentials: map[string]any{
				"access_token":              "old-access",
				"refresh_token":             "old-refresh",
				ClaudeSKCookieCredentialKey: "sk-ant-sid02-original",
				"expires_at":                fmt.Sprint(time.Now().Add(-time.Hour).Unix()),
			},
		},
	}
	svc := &GatewayService{accountRepo: repo}

	token, credentials, err := svc.recoverClaudeSKImportTokenOn401(context.Background(), repo.account, []byte(`{"error":{"message":"OAuth access token has been revoked"}}`))
	if err != nil {
		t.Fatalf("recover error = %v", err)
	}
	if token != "new-access" {
		t.Fatalf("token = %q, want new-access", token)
	}
	if credentials["access_token"] != "new-access" || credentials["refresh_token"] != "new-refresh" {
		t.Fatalf("credentials not updated: %#v", credentials)
	}
	if credentials[ClaudeSKCookieCredentialKey] != "sk-ant-sid02-original" {
		t.Fatalf("original SK not preserved: %#v", credentials[ClaudeSKCookieCredentialKey])
	}
	if repo.updateCredentialsCalls != 1 {
		t.Fatalf("update credentials calls = %d, want 1", repo.updateCredentialsCalls)
	}
}

func TestGatewayRecoverClaudeSKImportTokenOn401PermanentConvertErrorSetsError(t *testing.T) {
	oldURL := os.Getenv("SUB2API_CONVERT_URL")
	oldCookie := os.Getenv("SUB2API_CONVERT_COOKIE")
	defer func() {
		_ = os.Setenv("SUB2API_CONVERT_URL", oldURL)
		_ = os.Setenv("SUB2API_CONVERT_COOKIE", oldCookie)
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid sk"}`))
	}))
	defer server.Close()
	_ = os.Setenv("SUB2API_CONVERT_URL", server.URL)
	_ = os.Setenv("SUB2API_CONVERT_COOKIE", "converter-cookie")

	repo := &skGatewayRecoveryRepo{
		account: &Account{
			ID:       52,
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Extra:    map[string]any{"source": ClaudeSKImportSource},
			Credentials: map[string]any{
				ClaudeSKCookieCredentialKey: "sk-ant-sid02-dead",
			},
		},
	}
	svc := &GatewayService{accountRepo: repo}

	_, _, err := svc.recoverClaudeSKImportTokenOn401(context.Background(), repo.account, []byte(`{"error":{"message":"OAuth access token has been revoked"}}`))

	if err == nil {
		t.Fatal("expected recover error")
	}
	if repo.setErrorCalls != 1 {
		t.Fatalf("set error calls = %d, want 1", repo.setErrorCalls)
	}
	if repo.setTempCalls != 0 {
		t.Fatalf("set temp calls = %d, want 0", repo.setTempCalls)
	}
}

type skGatewayRecoveryRepo struct {
	AccountRepository
	account                *Account
	updateCredentialsCalls int
	setErrorCalls          int
	setTempCalls           int
}

func (r *skGatewayRecoveryRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	if id != r.account.ID {
		return fmt.Errorf("unexpected id %d", id)
	}
	r.updateCredentialsCalls++
	r.account.Credentials = credentials
	return nil
}

func (r *skGatewayRecoveryRepo) ClearTempUnschedulable(context.Context, int64) error { return nil }
func (r *skGatewayRecoveryRepo) ClearError(context.Context, int64) error             { return nil }
func (r *skGatewayRecoveryRepo) SetSchedulable(context.Context, int64, bool) error   { return nil }
func (r *skGatewayRecoveryRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}
func (r *skGatewayRecoveryRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.setTempCalls++
	return nil
}
