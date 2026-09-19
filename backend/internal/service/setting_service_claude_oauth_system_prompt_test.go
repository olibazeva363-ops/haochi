package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func resetGatewayForwardingSettingsCacheForTest(t *testing.T) {
	t.Helper()
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	t.Cleanup(func() {
		gatewayForwardingSF.Forget("gateway_forwarding")
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	})
}

type retiredPromptSettingRepo struct {
	*gatewayTTLSettingRepo
	getMultipleErr error
	requestedKeys  []string
}

func (r *retiredPromptSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	r.requestedKeys = append(r.requestedKeys, keys...)
	if r.getMultipleErr != nil {
		return nil, r.getMultipleErr
	}
	return r.gatewayTTLSettingRepo.GetMultiple(ctx, keys)
}

func requireRetiredClaudePromptSettings(t *testing.T, svc *SettingService) {
	t.Helper()
	enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())
	require.False(t, enabled)
	require.Empty(t, prompt)
	require.Empty(t, blocks)
	require.False(t, svc.IsClientDatelineNormalizationEnabled(context.Background()))
}

func TestSettingService_GetClaudeOAuthSystemPromptInjectionSettings(t *testing.T) {
	for _, tt := range []struct {
		name string
		data map[string]string
		err  error
	}{
		{name: "missing settings"},
		{name: "legacy enabled settings", data: map[string]string{
			SettingKeyEnableClaudeOAuthSystemPromptInjection: "true",
			SettingKeyClaudeOAuthSystemPrompt:                "legacy injected prompt",
			SettingKeyClaudeOAuthSystemPromptBlocks:          `[{"type":"text","text":"legacy block"}]`,
			SettingKeyEnableCCHSigning:                       "true",
			SettingKeyEnableClientDatelineNormalization:      "true",
			SettingKeyEnableFingerprintUnification:           "false",
			SettingKeyEnableMetadataPassthrough:              "true",
		}},
		{name: "database error fallback", err: errors.New("database unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetGatewayForwardingSettingsCacheForTest(t)
			repo := &retiredPromptSettingRepo{gatewayTTLSettingRepo: &gatewayTTLSettingRepo{data: tt.data}, getMultipleErr: tt.err}
			svc := NewSettingService(repo, &config.Config{})
			for read := 0; read < 2; read++ {
				fp, mp, cch := svc.GetGatewayForwardingSettings(context.Background())
				require.False(t, cch)
				require.Equal(t, tt.data == nil, fp, "fingerprint settings must retain their existing semantics")
				require.Equal(t, tt.data != nil, mp, "metadata settings must retain their existing semantics")
				requireRetiredClaudePromptSettings(t, svc)
			}
			for _, key := range []string{SettingKeyEnableCCHSigning, SettingKeyEnableClaudeOAuthSystemPromptInjection,
				SettingKeyClaudeOAuthSystemPrompt, SettingKeyClaudeOAuthSystemPromptBlocks, SettingKeyEnableClientDatelineNormalization} {
				require.NotContains(t, repo.requestedKeys, key, "runtime must not fetch retired prompt configuration")
			}
		})
	}

	t.Run("nil service and repository", func(t *testing.T) {
		requireRetiredClaudePromptSettings(t, nil)
		requireRetiredClaudePromptSettings(t, &SettingService{})
	})
}

func TestSettingService_ClaudePromptSettingsIgnoreLegacyStorageAndWrites(t *testing.T) {
	resetGatewayForwardingSettingsCacheForTest(t)
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeySiteName: "Example Gateway",
		SettingKeyEnableClaudeOAuthSystemPromptInjection: "true",
		SettingKeyClaudeOAuthSystemPrompt:                "stored prompt",
		SettingKeyClaudeOAuthSystemPromptBlocks:          "invalid legacy JSON",
		SettingKeyEnableCCHSigning:                       "true",
		SettingKeyEnableClientDatelineNormalization:      "true",
		SettingKeyEnableFingerprintUnification:           "false",
		SettingKeyEnableMetadataPassthrough:              "true",
		SettingKeyEnableAnthropicCacheTTL1hInjection:     "false",
	}}
	svc := NewSettingService(repo, &config.Config{})
	settings, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.EnableClaudeOAuthSystemPromptInjection)
	require.Empty(t, settings.ClaudeOAuthSystemPrompt)
	require.Empty(t, settings.ClaudeOAuthSystemPromptBlocks)
	require.False(t, settings.EnableCCHSigning)
	require.False(t, settings.EnableClientDatelineNormalization)

	settings.EnableClaudeOAuthSystemPromptInjection = true
	settings.ClaudeOAuthSystemPrompt = "new ignored prompt"
	settings.ClaudeOAuthSystemPromptBlocks = "not JSON; must be ignored without validation"
	settings.EnableCCHSigning = true
	settings.EnableClientDatelineNormalization = true
	require.NoError(t, svc.UpdateSettings(context.Background(), settings))
	for _, key := range []string{SettingKeyEnableClaudeOAuthSystemPromptInjection, SettingKeyEnableCCHSigning, SettingKeyEnableClientDatelineNormalization} {
		require.Equal(t, "false", repo.data[key])
	}
	require.Empty(t, repo.data[SettingKeyClaudeOAuthSystemPrompt])
	require.Empty(t, repo.data[SettingKeyClaudeOAuthSystemPromptBlocks])
	require.Equal(t, "Example Gateway", repo.data[SettingKeySiteName])
	fp, mp, cch := svc.GetGatewayForwardingSettings(context.Background())
	require.False(t, fp)
	require.True(t, mp)
	require.False(t, cch)
	require.False(t, svc.IsAnthropicCacheTTL1hInjectionEnabled(context.Background()))
	requireRetiredClaudePromptSettings(t, svc)
}
