//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 裸 rate_limit_error 429（无窗口/积分/模型明细）且请求的是 Fable：
// 应只冷却 Fable 模型级，绝不把整个账号拖入账号级限流池。
func TestHandleUpstreamError_PlainRateLimitFableRequestStaysModelLevel(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 61, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`)

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-fable-5")

	require.False(t, shouldDisable)
	require.Zero(t, repo.rateLimitCalls, "Fable 的裸 429 不应把整个账号打成账号级限流")
	require.Equal(t, 1, repo.modelRateLimitCalls, "应只对 Fable 模型级冷却")
	require.Equal(t, anthropicFableRateLimitKey, repo.lastModelRateLimitScope)
}

// 同样的裸 rate_limit_error 429，但请求的是非 Fable 模型：维持账号级兜底冷却。
func TestHandleUpstreamError_PlainRateLimitNonFableStaysAccountLevel(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 62, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`)

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-opus-4-8")

	require.False(t, shouldDisable)
	require.Equal(t, 1, repo.rateLimitCalls, "非 Fable 的裸 429 维持账号级冷却")
	require.Zero(t, repo.modelRateLimitCalls, "非 Fable 不应打模型级")
}
