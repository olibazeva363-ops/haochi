package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClaudeProtectionClientAbortClassification(t *testing.T) {
	require.True(t, isClientAbort(context.Background(), context.Canceled))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.True(t, isClientAbort(ctx, errors.New("connection reset")))
	require.False(t, isClientAbort(context.Background(), context.DeadlineExceeded))
}

func TestCloneFailoverResponseHeadersAddsGentleRetryAfter(t *testing.T) {
	svc := &GatewayService{rateLimitService: &RateLimitService{}}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}

	headers := svc.cloneFailoverResponseHeaders(context.Background(), resp, account)
	require.Equal(t, "5", headers.Get("Retry-After"))
	require.Empty(t, resp.Header.Get("Retry-After"), "the upstream response must not be mutated")
}

func TestAnthropicAccessCooldownEscalatesWithoutPermanentDisable(t *testing.T) {
	require.Equal(t, time.Minute, anthropicAccessCooldown(1))
	require.Equal(t, 3*time.Minute, anthropicAccessCooldown(2))
	require.Equal(t, 10*time.Minute, anthropicAccessCooldown(3))
	require.Equal(t, 10*time.Minute, anthropicAccessCooldown(20))
	require.False(t, isPermanentAnthropicAccessDenial("temporary access rejection", nil))
	require.True(t, isPermanentAnthropicAccessDenial("account has been disabled", nil))
}
