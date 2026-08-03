//go:build unit

package service

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 辅助：把账号标记为"已确认无可用 overage"(有效期未来)。
func withNoOverage(a *Account) *Account {
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	a.Extra[overageUnavailableExtraKey] = time.Now().Add(2 * time.Hour).Unix()
	return a
}

// 已知无 overage + 5h 利用率≥100% 的成功响应 → 主动打账号级限流。
func TestUpdateSessionWindow_5hExhausted_KnownNoOverage_SetsRateLimit(t *testing.T) {
	resetAt := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 101, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Equal(t, 1, repo.rateLimitCalls, "已知无 overage 的号 5h 满额应主动限流")
	require.Equal(t, resetAt.Unix(), repo.lastRateLimitReset.Unix())
}

// 5h 利用率≥100% 但未知/有 overage → 不主动限流，留池里用积分续。
func TestUpdateSessionWindow_5hExhausted_OverageMaybeAvailable_NoRateLimit(t *testing.T) {
	resetAt := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := &Account{ID: 111, Type: AccountTypeOAuth, Platform: PlatformAnthropic} // 无 overage 标记

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Zero(t, repo.rateLimitCalls, "overage 未知/可用的号不应被主动踢出，应留池用积分续")
}

// 已知无 overage + 5h rejected → 主动限流。
func TestUpdateSessionWindow_5hRejected_KnownNoOverage_SetsRateLimit(t *testing.T) {
	resetAt := time.Now().Add(90 * time.Minute).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "rejected")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 102, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Equal(t, 1, repo.rateLimitCalls, "已知无 overage 的号 5h rejected 应主动限流")
}

// 已知无 overage + 7d 账号级周窗口耗尽 → 主动限流。
func TestUpdateSessionWindow_7dExhausted_KnownNoOverage_SetsRateLimit(t *testing.T) {
	resetAt := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-7d-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 103, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Equal(t, 1, repo.rateLimitCalls, "已知无 overage 的号 7d 耗尽应主动限流")
	require.Equal(t, resetAt.Unix(), repo.lastRateLimitReset.Unix())
}

// 仅 7d_oi(Fable 包含额度周窗)耗尽 → 不打任何冷却(overage 承载)。
func TestUpdateSessionWindow_Passive7dOIExhaustedDoesNotCooldown(t *testing.T) {
	resetAt := time.Now().Add(48 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-7d_oi-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-7d_oi-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 106, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Zero(t, repo.rateLimitCalls, "仅 7d_oi 耗尽不应打账号级限流")
	require.Zero(t, repo.modelRateLimitCalls, "仅 7d_oi 耗尽不应打模型级限流")
}

// 窗口健康不应打任何冷却。
func TestUpdateSessionWindow_HealthyWindowNoRateLimit(t *testing.T) {
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "0.3")

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 104, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Zero(t, repo.rateLimitCalls)
	require.Zero(t, repo.modelRateLimitCalls)
}

// 开关关闭时即使已知无 overage 也不主动剔除。
func TestUpdateSessionWindow_DisabledByEnv(t *testing.T) {
	t.Setenv("SUB2API_PASSIVE_WINDOW_COOLDOWN", "0")
	resetAt := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-unified-5h-status", "allowed_warning")
	headers.Set("anthropic-ratelimit-unified-5h-utilization", "1.0")
	headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(resetAt.Unix(), 10))

	repo := &anthropicWindowLimitRepo{}
	svc := NewRateLimitService(repo, nil, nil, nil, nil)
	account := withNoOverage(&Account{ID: 105, Type: AccountTypeOAuth, Platform: PlatformAnthropic})

	svc.UpdateSessionWindow(context.Background(), account, headers)

	require.Zero(t, repo.rateLimitCalls, "开关关闭时不应主动打限流")
}
