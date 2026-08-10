package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvaluateAnthropicUsageGuardGentleThreshold(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	fiveHourReset := now.Add(2 * time.Hour)
	sevenDayReset := now.Add(3 * 24 * time.Hour)
	account := &Account{
		SessionWindowEnd: &fiveHourReset,
		Extra: map[string]any{
			"session_window_utilization":   0.96,
			"passive_usage_7d_utilization": 0.98,
			"passive_usage_7d_reset":       sevenDayReset.Unix(),
		},
	}

	decision, ok := evaluateAnthropicUsageGuard(account, 97, now)
	require.True(t, ok)
	require.Equal(t, "7d", decision.Window)
	require.Equal(t, sevenDayReset, decision.Until)
}

func TestEvaluateAnthropicUsageGuardAvoidsFalsePauses(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	tooFar := now.Add(9 * 24 * time.Hour)

	tests := []struct {
		name    string
		account *Account
	}{
		{"below threshold", &Account{SessionWindowEnd: usageGuardTimePtr(now.Add(time.Hour)), Extra: map[string]any{"session_window_utilization": 0.969}}},
		{"expired reset", &Account{SessionWindowEnd: &past, Extra: map[string]any{"session_window_utilization": 1.0}}},
		{"implausible reset", &Account{SessionWindowEnd: &tooFar, Extra: map[string]any{"session_window_utilization": 1.0}}},
		{"invalid utilization", &Account{SessionWindowEnd: usageGuardTimePtr(now.Add(time.Hour)), Extra: map[string]any{"session_window_utilization": math.NaN()}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := evaluateAnthropicUsageGuard(tt.account, 97, now)
			require.False(t, ok)
		})
	}
	_, ok := evaluateAnthropicUsageGuard(&Account{}, 100, now)
	require.False(t, ok)
}

func usageGuardTimePtr(value time.Time) *time.Time { return &value }
