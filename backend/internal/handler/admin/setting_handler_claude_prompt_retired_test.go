package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateSettingsIgnoresRetiredClaudePromptFields(t *testing.T) {
	for _, sendLegacyFields := range []bool{false, true} {
		name := "unrelated partial update"
		if sendLegacyFields {
			name = "legacy client tries to enable injection"
		}
		t.Run(name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{
				service.SettingKeySiteName:                               "Example Gateway",
				service.SettingKeyEnableFingerprintUnification:           "false",
				service.SettingKeyEnableMetadataPassthrough:              "true",
				service.SettingKeyEnableAnthropicCacheTTL1hInjection:     "false",
				service.SettingKeyEnableClaudeOAuthSystemPromptInjection: "true",
				service.SettingKeyClaudeOAuthSystemPrompt:                "stored legacy prompt",
				service.SettingKeyClaudeOAuthSystemPromptBlocks:          "invalid stored blocks",
				service.SettingKeyEnableCCHSigning:                       "true",
				service.SettingKeyEnableClientDatelineNormalization:      "true",
			})
			payload := map[string]any{"risk_control_enabled": true}
			if sendLegacyFields {
				payload["enable_claude_oauth_system_prompt_injection"] = true
				payload["claude_oauth_system_prompt"] = "ignored new prompt"
				payload["claude_oauth_system_prompt_blocks"] = "not JSON"
				payload["enable_cch_signing"] = true
				payload["enable_client_dateline_normalization"] = true
			}

			rec := doUpdateSettings(t, h, payload, nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var response struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			for _, key := range []string{"enable_claude_oauth_system_prompt_injection", "enable_cch_signing", "enable_client_dateline_normalization"} {
				require.Equal(t, false, response.Data[key], "legacy boolean fields remain present and disabled")
				if sendLegacyFields {
					require.Equal(t, "false", repo.values[key])
				}
			}
			for _, key := range []string{"claude_oauth_system_prompt", "claude_oauth_system_prompt_blocks"} {
				require.Equal(t, "", response.Data[key], "legacy content fields remain present and empty")
				if sendLegacyFields {
					require.Empty(t, repo.values[key])
				}
			}
			require.Equal(t, "true", repo.values[service.SettingKeyRiskControlEnabled])
			require.Equal(t, "Example Gateway", repo.values[service.SettingKeySiteName])
			require.Equal(t, "false", repo.values[service.SettingKeyEnableFingerprintUnification])
			require.Equal(t, "true", repo.values[service.SettingKeyEnableMetadataPassthrough])
			require.Equal(t, "false", repo.values[service.SettingKeyEnableAnthropicCacheTTL1hInjection])
		})
	}
}
