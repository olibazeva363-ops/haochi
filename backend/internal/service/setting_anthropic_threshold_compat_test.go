//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnthropicThresholdConfigCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, stored string
		want         int
	}{
		{"missing uses config", "", 97},
		{"stored threshold wins", `{"anthropic":90}`, 90},
		{"explicit disable wins", `{"anthropic":100}`, 100},
		{"stored other platform uses upstream default", `{"openai":80}`, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := map[string]string{}
			if tc.stored != "" {
				seed[SettingKeyAccountSchedulingThresholds] = tc.stored
			}
			svc := newSettingServiceForPlatformThresholdTest(seed)
			svc.cfg.RateLimit.AnthropicUsagePauseThresholdPercent = 97
			thresholds := svc.GetAccountSchedulingThresholds(context.Background())
			require.Equal(t, tc.want, thresholds[PlatformAnthropic])
			require.Equal(t, thresholds, svc.parseSettings(seed).AccountSchedulingThresholds)

			now := time.Now().UTC()
			until := now.Add(time.Hour)
			account := &Account{Platform: PlatformAnthropic, SessionWindowEnd: &until,
				Credentials: map[string]any{accountSchedulingThresholdCredentialKey: 95},
				Extra:       map[string]any{"session_window_utilization": 96.0}}
			decision := EvaluateAccountSchedulingThreshold(account, thresholds, now)
			require.True(t, decision.ShouldPause)
			require.Equal(t, 95, decision.ThresholdPercent)
		})
	}
}
