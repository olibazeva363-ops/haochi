//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 无窗口证据时不能仅凭请求模型推断限流范围；使用可配置的账号级短冷却。
func TestHandleUpstreamError_PlainRateLimitFableRequestUsesAccountFallback(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 61, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`)

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-fable-5")

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitCalls, "无窗口证据的 429 应短暂冷却账号，防止反复调度")
	require.Zero(t, repo.modelRateLimitCalls, "模型级冷却必须有明确的 7d_oi 窗口证据")
}

// 同样的裸 rate_limit_error 429，但请求的是非 Fable 模型：维持账号级兜底冷却。
func TestHandleUpstreamError_PlainRateLimitNonFableStaysAccountLevel(t *testing.T) {
	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 62, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`)

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-opus-4-8")

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitCalls, "非 Fable 的裸 429 维持账号级冷却")
	require.Zero(t, repo.modelRateLimitCalls, "非 Fable 不应打模型级")
}
