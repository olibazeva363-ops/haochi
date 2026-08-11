//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type anthropicProtectionCacheStub struct {
	counts     map[string][]int64
	states     []*TempUnschedState
	resetCalls []int64
}

func (s *anthropicProtectionCacheStub) SetTempUnsched(_ context.Context, _ int64, state *TempUnschedState) error {
	s.states = append(s.states, state)
	return nil
}

func (s *anthropicProtectionCacheStub) GetTempUnsched(context.Context, int64) (*TempUnschedState, error) {
	return nil, nil
}

func (s *anthropicProtectionCacheStub) DeleteTempUnsched(context.Context, int64) error { return nil }

func (s *anthropicProtectionCacheStub) IncrementAnthropicProtectionFailure(
	_ context.Context,
	_ int64,
	failureClass string,
	_ time.Duration,
) (int64, error) {
	values := s.counts[failureClass]
	if len(values) == 0 {
		return 1, nil
	}
	count := values[0]
	s.counts[failureClass] = values[1:]
	return count, nil
}

func (s *anthropicProtectionCacheStub) ResetAnthropicProtectionFailures(_ context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	return nil
}

func newAnthropicProtectionAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformAnthropic,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestRateLimitService_AnthropicAmbiguous403UsesSoftCooldown(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	cache := &anthropicProtectionCacheStub{counts: map[string][]int64{
		anthropicAccessFailureClass: {1},
	}}
	blocker := &runtimeBlockRecorder{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	svc.SetAccountRuntimeBlocker(blocker)
	account := newAnthropicProtectionAccount(4101)

	disabled := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"temporary edge rejection"}}`),
	)

	require.True(t, disabled)
	require.Zero(t, repo.setErrorCalls)
	require.Equal(t, 1, repo.tempCalls)
	require.Contains(t, repo.lastTempReason, "failure=1")
	require.WithinDuration(t, time.Now().Add(time.Minute), *account.TempUnschedulableUntil, 3*time.Second)
	require.Len(t, cache.states, 1)
	require.Equal(t, anthropicAccessFailureClass, cache.states[0].MatchedKeyword)
	require.Equal(t, "anthropic_access_circuit", blocker.reasons[0])
}

func TestRateLimitService_AnthropicExplicitSuspensionStillDisables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	cache := &anthropicProtectionCacheStub{counts: map[string][]int64{}}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	account := newAnthropicProtectionAccount(4102)

	disabled := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusForbidden,
		http.Header{},
		[]byte(`{"error":{"message":"account has been suspended"}}`),
	)

	require.True(t, disabled)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Zero(t, repo.tempCalls)
}

func TestRateLimitService_AnthropicProviderCircuitRequiresThreshold(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	cache := &anthropicProtectionCacheStub{counts: map[string][]int64{
		anthropicProviderFailureClass: {3, 4},
	}}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	account := newAnthropicProtectionAccount(4103)

	svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"busy"}}`))
	require.Zero(t, repo.tempCalls)

	svc.HandleUpstreamError(context.Background(), account, http.StatusServiceUnavailable, http.Header{}, []byte(`{"error":{"message":"busy"}}`))
	require.Equal(t, 1, repo.tempCalls)
	require.WithinDuration(t, time.Now().Add(anthropicProviderCooldown), *account.TempUnschedulableUntil, 3*time.Second)
	require.Equal(t, anthropicProviderFailureClass, cache.states[0].MatchedKeyword)
}

func TestRateLimitService_AnthropicTransportCircuitAndSuccessReset(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	cache := &anthropicProtectionCacheStub{counts: map[string][]int64{
		anthropicTransportFailureClass: {2, 3},
	}}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, cache)
	account := newAnthropicProtectionAccount(4104)

	svc.HandleAnthropicTransportFailure(context.Background(), account, "dial timeout")
	require.Zero(t, repo.tempCalls)

	svc.HandleAnthropicTransportFailure(context.Background(), account, "dial timeout")
	require.Equal(t, 1, repo.tempCalls)
	require.WithinDuration(t, time.Now().Add(anthropicTransportCooldown), *account.TempUnschedulableUntil, 3*time.Second)

	svc.ResetAnthropicProtection(context.Background(), account.ID)
	require.Equal(t, []int64{account.ID}, cache.resetCalls)
}
