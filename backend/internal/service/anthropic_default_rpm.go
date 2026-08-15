package service

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// anthropicDefaultBaseRPMProvider 是 anthropic_default_base_rpm 设置的读取源，
// 在 ProvideSettingService 中注入（regen-safe 全局，同 SetConvertCookieProvider 模式）。
var anthropicDefaultBaseRPMProvider SettingRepository

// SetAnthropicDefaultBaseRPMProvider 注册全局默认 RPM 的设置读取源；nil 表示禁用。
func SetAnthropicDefaultBaseRPMProvider(p SettingRepository) {
	anthropicDefaultBaseRPMProvider = p
}

const (
	// anthropicDefaultBaseRPMFallback 是设置不可用（未注入/DB 短暂故障）时的兜底值。
	anthropicDefaultBaseRPMFallback = 15
	// anthropicDefaultBaseRPMTTL 限制设置读取频率：GetBaseRPM 在网关热路径上被
	// 每请求调用，不能每次都打 DB。
	anthropicDefaultBaseRPMTTL = 60 * time.Second
)

var (
	anthropicDefaultBaseRPMCache   atomic.Value // int
	anthropicDefaultBaseRPMCacheAt atomic.Int64 // unix nanos
)

// anthropicDefaultBaseRPM 返回未配置账号级 base_rpm 的 Anthropic OAuth/SetupToken
// 账号使用的全局默认 RPM。读取失败时沿用上次缓存；从未成功读取过则用内置兜底值。
func anthropicDefaultBaseRPM() int {
	now := time.Now().UnixNano()
	if cachedAt := anthropicDefaultBaseRPMCacheAt.Load(); cachedAt > 0 && now-cachedAt < int64(anthropicDefaultBaseRPMTTL) {
		if v, ok := anthropicDefaultBaseRPMCache.Load().(int); ok {
			return v
		}
	}

	value := anthropicDefaultBaseRPMFallback
	if anthropicDefaultBaseRPMProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		raw, err := anthropicDefaultBaseRPMProvider.GetValue(ctx, SettingKeyAnthropicDefaultBaseRPM)
		cancel()
		if err == nil {
			if n, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil && n >= 0 {
				value = n
			}
		}
	}

	anthropicDefaultBaseRPMCache.Store(value)
	anthropicDefaultBaseRPMCacheAt.Store(now)
	return value
}
