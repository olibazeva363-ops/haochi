package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

const anthropicUsageGuardSource = "anthropic_usage_guard"

type anthropicUsageGuardDecision struct {
	Window      string
	UsedPercent float64
	Until       time.Time
}

type anthropicUsageGuardReason struct {
	Source          string  `json:"source"`
	Window          string  `json:"window"`
	Threshold       int     `json:"threshold_percent"`
	UsedPercent     float64 `json:"used_percent"`
	UntilUnix       int64   `json:"until_unix"`
	TriggeredAtUnix int64   `json:"triggered_at_unix"`
	StatusCode      int     `json:"status_code"`
	MatchedKeyword  string  `json:"matched_keyword"`
	RuleIndex       int     `json:"rule_index"`
	ErrorMessage    string  `json:"error_message"`
}

// ApplyAnthropicUsageGuard pauses a nearly exhausted account until its known
// quota window resets. The default threshold is intentionally gentle (97%).
func (s *RateLimitService) ApplyAnthropicUsageGuard(ctx context.Context, account *Account) bool {
	if s == nil || s.cfg == nil || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return false
	}
	if !account.IsAnthropicOAuthOrSetupToken() || !account.IsActive() || !account.Schedulable {
		return false
	}
	threshold := s.cfg.RateLimit.AnthropicUsagePauseThresholdPercent
	if threshold <= 0 || threshold >= 100 {
		return false
	}

	now := time.Now().UTC()
	decision, ok := evaluateAnthropicUsageGuard(account, threshold, now)
	if !ok {
		return false
	}
	if sameAnthropicUsageGuardPause(account, decision) {
		return true
	}
	if !account.IsSchedulable() {
		return false
	}

	message := fmt.Sprintf("Anthropic %s usage %.1f%% reached the gentle %d%% pause threshold", decision.Window, decision.UsedPercent, threshold)
	reasonPayload := anthropicUsageGuardReason{
		Source:          anthropicUsageGuardSource,
		Window:          decision.Window,
		Threshold:       threshold,
		UsedPercent:     decision.UsedPercent,
		UntilUnix:       decision.Until.Unix(),
		TriggeredAtUnix: now.Unix(),
		MatchedKeyword:  "usage_threshold",
		RuleIndex:       -1,
		ErrorMessage:    message,
	}
	reasonBytes, err := json.Marshal(reasonPayload)
	if err != nil {
		return false
	}
	reason := string(reasonBytes)

	until := decision.Until
	account.TempUnschedulableUntil = &until
	account.TempUnschedulableReason = reason
	s.notifyAccountSchedulingBlocked(account, until, anthropicUsageGuardSource)
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		slog.Warn("anthropic_usage_guard_persist_failed", "account_id", account.ID, "error", err)
	} else if s.tempUnschedCache != nil {
		state := &TempUnschedState{
			UntilUnix:       until.Unix(),
			TriggeredAtUnix: now.Unix(),
			MatchedKeyword:  "usage_threshold",
			RuleIndex:       -1,
			ErrorMessage:    message,
		}
		if err := s.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); err != nil {
			slog.Warn("anthropic_usage_guard_cache_failed", "account_id", account.ID, "error", err)
		}
	}
	slog.Info("anthropic_usage_guard_paused", "account_id", account.ID, "window", decision.Window, "used_percent", decision.UsedPercent, "threshold_percent", threshold, "until", until)
	return true
}

func evaluateAnthropicUsageGuard(account *Account, threshold int, now time.Time) (anthropicUsageGuardDecision, bool) {
	if account == nil || threshold <= 0 || threshold >= 100 {
		return anthropicUsageGuardDecision{}, false
	}
	maxUntil := now.Add(8 * 24 * time.Hour)
	candidates := make([]anthropicUsageGuardDecision, 0, 2)
	if used, ok := anthropicUsagePercent(account.Extra["session_window_utilization"]); ok && used >= float64(threshold) && account.SessionWindowEnd != nil {
		candidates = append(candidates, anthropicUsageGuardDecision{Window: "5h", UsedPercent: used, Until: account.SessionWindowEnd.UTC()})
	}
	if used, ok := anthropicUsagePercent(account.Extra["passive_usage_7d_utilization"]); ok && used >= float64(threshold) {
		if until, ok := anthropicUsageResetTime(account.Extra["passive_usage_7d_reset"]); ok {
			candidates = append(candidates, anthropicUsageGuardDecision{Window: "7d", UsedPercent: used, Until: until})
		}
	}

	var winner anthropicUsageGuardDecision
	found := false
	for _, candidate := range candidates {
		if !candidate.Until.After(now) || candidate.Until.After(maxUntil) {
			continue
		}
		if !found || candidate.Until.After(winner.Until) {
			winner, found = candidate, true
		}
	}
	return winner, found
}

func anthropicUsagePercent(raw any) (float64, bool) {
	var value float64
	switch v := raw.(type) {
	case float64:
		value = v
	case float32:
		value = float64(v)
	case int:
		value = float64(v)
	case int64:
		value = float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0, false
		}
		value = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		value = parsed
	default:
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	if value <= 1 {
		value *= 100
	}
	if value > 100 {
		return 0, false
	}
	return value, true
}

func anthropicUsageResetTime(raw any) (time.Time, bool) {
	var unix int64
	switch v := raw.(type) {
	case int64:
		unix = v
	case int:
		unix = int64(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return time.Time{}, false
		}
		unix = int64(v)
	case json.Number:
		parsed, err := v.Int64()
		if err != nil {
			return time.Time{}, false
		}
		unix = parsed
	case string:
		value := strings.TrimSpace(v)
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			unix = parsed
		} else if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC(), true
		} else {
			return time.Time{}, false
		}
	default:
		return time.Time{}, false
	}
	if unix > 1e11 {
		unix /= 1000
	}
	if unix <= 0 {
		return time.Time{}, false
	}
	return time.Unix(unix, 0).UTC(), true
}

func sameAnthropicUsageGuardPause(account *Account, decision anthropicUsageGuardDecision) bool {
	if account == nil || account.TempUnschedulableUntil == nil || account.TempUnschedulableUntil.UTC().Unix() != decision.Until.Unix() {
		return false
	}
	var reason anthropicUsageGuardReason
	return json.Unmarshal([]byte(account.TempUnschedulableReason), &reason) == nil && reason.Source == anthropicUsageGuardSource && reason.Window == decision.Window
}
