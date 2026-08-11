package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAnthropicProtectionCounterIncrementAndReset(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cache := NewTempUnschedCache(rdb)
	protection, ok := cache.(service.AnthropicProtectionCounterCache)
	require.True(t, ok)
	ctx := context.Background()

	count, err := protection.IncrementAnthropicProtectionFailure(ctx, 99, "provider", 2*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	count, err = protection.IncrementAnthropicProtectionFailure(ctx, 99, "provider", 2*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)

	require.NoError(t, protection.ResetAnthropicProtectionFailures(ctx, 99))
	count, err = protection.IncrementAnthropicProtectionFailure(ctx, 99, "provider", 2*time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestAnthropicProtectionCounterRejectsUnknownClass(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewTempUnschedCache(rdb).(service.AnthropicProtectionCounterCache)

	_, err := cache.IncrementAnthropicProtectionFailure(context.Background(), 99, "unknown", time.Minute)
	require.ErrorContains(t, err, "unsupported anthropic protection failure class")
}
