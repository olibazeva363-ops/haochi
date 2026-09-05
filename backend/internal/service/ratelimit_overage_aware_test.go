//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 成功响应即使显示基础窗口耗尽，也可能正在使用 overage。
// 旧版本遗留的 overage 标记不应把仍然成功响应的账号重新冷却。
func TestUpdateSessionWindow_ExhaustedSuccessDoesNotDisableOverage(t *testing.T) {
	for _, legacyUntil := range []int64{0, time.Now().Add(time.Hour).Unix(), time.Now().Add(-time.Hour).Unix()} {
		t.Run(strconv.FormatInt(legacyUntil, 10), func(t *testing.T) {
			repo := &anthropicWindowLimitRepo{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{ID: 70, Platform: PlatformAnthropic, Type: AccountTypeOAuth,
				Extra: map[string]any{"overage_unavailable_until": legacyUntil}}
			headers := http.Header{}
			headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
			headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
			headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))

			svc.UpdateSessionWindow(context.Background(), account, headers)

			require.Zero(t, repo.rateLimitCalls)
			require.Zero(t, repo.modelRateLimitCalls)
			require.Equal(t, 1, repo.sessionWindowCalls)
			require.Equal(t, 1.0, repo.lastExtraUpdates["session_window_utilization"])
		})
	}
}

// 真实 429 的明确 5h 窗口证据使用统一账号限流状态，并在窗口重置后自然失效。
func TestHandleUpstreamError_5hAccount429UsesExpiringAccountLimit(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 70, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(reset.Unix(), 10))

	svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, nil, "claude-opus-4-8")

	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, reset, repo.lastRateLimitReset)
	require.Zero(t, repo.modelRateLimitCalls)
	account.RateLimitResetAt = &repo.lastRateLimitReset
	require.True(t, account.IsRateLimited())
	expired := time.Now().Add(-time.Second)
	account.RateLimitResetAt = &expired
	require.False(t, account.IsRateLimited())
}

// 缺少官方窗口头的旧 claude.ai 错误正文仍必须冷却，防止同账号反复 429。
func TestHandleUpstreamError_5hBodyWithoutWindowHeadersUsesBoundedFallback(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 70, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	body := []byte(fmt.Sprintf(
		`{"error":{"type":"rate_limit_error","message":"{\"type\":\"exceeded_limit\",\"resetsAt\":%d,\"representativeClaim\":\"five_hour\",\"perModelLimit\":false}"}}`,
		reset.Unix()))

	before := time.Now()
	svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-opus-4-8")

	require.Equal(t, 1, repo.rateLimitCalls)
	require.True(t, repo.lastRateLimitReset.After(before))
	require.True(t, repo.lastRateLimitReset.Before(before.Add(time.Minute)))
	require.Zero(t, repo.modelRateLimitCalls)
}
