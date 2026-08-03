//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// overageKnownUnavailable 读取标记 + 过期判定。
func TestOverageKnownUnavailable_RoundTripAndExpiry(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformAnthropic}
	require.False(t, overageKnownUnavailable(a), "无标记应为 false")

	a.Extra = map[string]any{overageUnavailableExtraKey: time.Now().Add(time.Hour).Unix()}
	require.True(t, overageKnownUnavailable(a), "标记在未来应为 true")

	a.Extra[overageUnavailableExtraKey] = time.Now().Add(-time.Hour).Unix()
	require.False(t, overageKnownUnavailable(a), "标记已过期应为 false(窗口重置后放行重试积分)")
}

// 真实 5h 账号级 429 → 账号级限流 + 记为无 overage(供下个窗口主动剔除)。
func TestHandleUpstreamError_5hAccount429MarksOverageUnavailable(t *testing.T) {
	repo := &rateLimit429AccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 70, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	body := []byte(fmt.Sprintf(
		`{"error":{"type":"rate_limit_error","message":"{\"type\":\"exceeded_limit\",\"resetsAt\":%d,\"representativeClaim\":\"five_hour\",\"perModelLimit\":false}"}}`,
		reset.Unix()))

	require.False(t, overageKnownUnavailable(account))
	svc.HandleUpstreamError(context.Background(), account, http.StatusTooManyRequests, http.Header{}, body, "claude-opus-4-8")

	require.Equal(t, 1, repo.rateLimitCalls, "5h 账号级 429 应打账号级限流")
	require.True(t, overageKnownUnavailable(account), "5h 账号级 429 后应记为无 overage")
}
