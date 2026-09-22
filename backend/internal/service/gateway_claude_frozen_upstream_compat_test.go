//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClaudeFrozenUpstreamCompatibility_BetaFilteringPreservesClientBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "count_tokens"} {
		for _, tc := range []struct {
			name       string
			storedBeta bool
			filterBeta bool
		}{
			{name: "old_profile_gains_supported_capability"},
			{name: "policy_filters_new_capability", filterBeta: true},
			{name: "policy_filters_frozen_capability", storedBeta: true, filterBeta: true},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				resetGatewayForwardingSettingsCacheForTest(t)
				cfg := &config.Config{}
				repo := &frozenEnvironmentAccountRepo{}
				svc := &GatewayService{
					cfg: cfg, accountRepo: repo, identityService: &IdentityService{},
					settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
						SettingKeyEnableFingerprintUnification: "true",
						SettingKeyEnableCCHSigning:             "true",
					}}, cfg),
				}
				account := &Account{ID: 6943, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
				profile := newClaudeFrozenEnvironmentProfile(account, nil)
				profile.BetaSet = []string{claude.BetaOAuth, "frozen-account-capability"}
				if tc.storedBeta {
					profile.BetaSet = append(profile.BetaSet, claude.BetaMidConversationOutputConfig)
				}
				originalProfile, err := json.Marshal(profile)
				require.NoError(t, err)
				svc.claudeFrozenProfiles.Store(account.ID, profile)

				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				c.Request.Header.Set("User-Agent", "untrusted-client/1.0")
				c.Request.Header.Set("Anthropic-Beta", "untrusted-client-beta")
				drop := map[string]struct{}{}
				if tc.filterBeta {
					drop[claude.BetaMidConversationOutputConfig] = struct{}{}
				}
				c.Set(betaPolicyFilterSetKey, drop)
				body := []byte(`{"model":"claude-opus-5","max_tokens":128,"output_config":{"effort":"high"},"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.91.abc; cc_entrypoint=cli;"},{"type":"text","text":"stable prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"system","content":[],"output_config":{"effort":"medium"}},{"role":"user","content":"hello"}]}`)
				body, err = sjson.SetBytes(body, "metadata.user_id", FormatMetadataUserID(
					strings.Repeat("a", 64), "", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", claude.CLICurrentVersion))
				require.NoError(t, err)
				var req *http.Request
				var wireBody []byte
				if endpoint == "messages" {
					req, wireBody, err = svc.buildUpstreamRequest(context.Background(), c, account,
						body, "test-token", "oauth", "claude-opus-5", false, true)
				} else {
					req, wireBody, err = svc.buildCountTokensRequest(context.Background(), c, account,
						body, "test-token", "oauth", "claude-opus-5", true)
				}
				require.NoError(t, err)
				actualBody := readUpstreamBodyForTest(t, req)
				require.NoError(t, req.Body.Close())
				require.Equal(t, wireBody, actualBody)
				beta := getHeaderRaw(req.Header, "anthropic-beta")
				require.Equal(t, !tc.filterBeta, anthropicBetaTokensContains(beta, claude.BetaMidConversationOutputConfig))
				require.True(t, anthropicBetaTokensContains(beta, "frozen-account-capability"))
				if endpoint == "messages" {
					require.False(t, anthropicBetaTokensContains(beta, "untrusted-client-beta"))
				} else {
					require.True(t, anthropicBetaTokensContains(beta, claude.BetaTokenCounting))
					require.False(t, gjson.GetBytes(wireBody, "max_tokens").Exists())
				}
				messages := gjson.GetBytes(wireBody, "messages").Array()
				if tc.filterBeta {
					require.Len(t, messages, 1)
					require.Equal(t, "user", messages[0].Get("role").String())
				} else {
					require.Len(t, messages, 2)
					require.Equal(t, "medium", messages[0].Get("output_config.effort").String())
				}
				require.Equal(t, "hello", messages[len(messages)-1].Get("content").String())
				require.Equal(t, "high", gjson.GetBytes(wireBody, "output_config.effort").String())
				require.Equal(t, claudeCodeSystemPrompt, gjson.GetBytes(wireBody, "system.0.text").String())
				require.Equal(t, "1h", gjson.GetBytes(wireBody, "system.2.cache_control.ttl").String())
				require.Equal(t, gjson.GetBytes(body, "system.0.text").String(), gjson.GetBytes(actualBody, "system.1.text").String())
				require.Equal(t, profile.UserAgent, getHeaderRaw(req.Header, "User-Agent"))
				require.Equal(t, profile.StainlessOS, getHeaderRaw(req.Header, "X-Stainless-OS"))
				metadata := ParseMetadataUserID(gjson.GetBytes(wireBody, "metadata.user_id").String())
				require.NotNil(t, metadata)
				require.Equal(t, profile.DeviceID, metadata.DeviceID)
				require.Equal(t, metadata.SessionID, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
				currentProfile, err := json.Marshal(profile)
				require.NoError(t, err)
				require.Equal(t, originalProfile, currentProfile, "capability compatibility must not rewrite frozen identity")
				require.Zero(t, repo.updateCalls)
			})
		}
	}
}

func TestClaudeUpstreamCompatibility_AccountBetaOverrideControlsMessageOutputConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "count_tokens"} {
		for _, passthrough := range []bool{false, true} {
			for _, keepBeta := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/passthrough=%v/override_has_beta=%v", endpoint, passthrough, keepBeta), func(t *testing.T) {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					clientBeta, override := claude.BetaMidConversationOutputConfig, "account-other-beta"
					if keepBeta {
						clientBeta, override = "client-other-beta", claude.BetaMidConversationOutputConfig
					}
					c.Request.Header.Set("Anthropic-Beta", clientBeta)
					account := newAnthropicAPIKeyPassthroughAccountForBetaTest()
					account.Credentials[credKeyHeaderOverrideEnabled] = true
					account.Credentials[credKeyHeaderOverrides] = map[string]any{"anthropic-beta": override}
					account.Extra["anthropic_passthrough"] = passthrough
					body := []byte(`{"model":"claude-opus-5","output_config":{"effort":"high"},"messages":[{"role":"system","content":[],"output_config":{"effort":"medium"}},{"role":"user","content":"hello"}]}`)
					svc := &GatewayService{cfg: &config.Config{}}
					var req *http.Request
					var err error
					switch {
					case endpoint == "messages" && passthrough:
						req, _, err = svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "test-key")
					case endpoint == "messages":
						req, _, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-key", "apikey", "claude-opus-5", false, false)
					case passthrough:
						req, err = svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, body, "test-key")
					default:
						req, _, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "test-key", "apikey", "claude-opus-5", false)
					}
					require.NoError(t, err)
					wireBody := readUpstreamBodyForTest(t, req)
					require.NoError(t, req.Body.Close())
					require.Equal(t, override, getHeaderRaw(req.Header, "anthropic-beta"))
					messages := gjson.GetBytes(wireBody, "messages").Array()
					if keepBeta {
						require.Len(t, messages, 2)
						require.Equal(t, "medium", messages[0].Get("output_config.effort").String())
					} else {
						require.Len(t, messages, 1)
						require.False(t, messages[0].Get("output_config").Exists())
					}
					require.Equal(t, "hello", messages[len(messages)-1].Get("content").String())
					require.Equal(t, "high", gjson.GetBytes(wireBody, "output_config.effort").String())
				})
			}
		}
	}
}

func TestClaudeFrozenUpstreamCompatibility_CountTokensCacheLimitPreservesClientBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetGatewayForwardingSettingsCacheForTest(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("User-Agent", "third-party-client/1.0")
	c.Set(betaPolicyFilterSetKey, map[string]struct{}{claude.BetaMidConversationOutputConfig: {}})
	body := []byte(`{"model":"claude-sonnet-4-6","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.91.abc; cc_entrypoint=cli;"},{"type":"text","text":"stable client prefix","cache_control":{"type":"ephemeral","ttl":"1h"}}],"tools":[{"name":"probe","description":"d","input_schema":{"type":"object"}}],"messages":[{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":[{"type":"text","text":"one","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":[{"type":"text","text":"two","cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"text","text":"three","cache_control":{"type":"ephemeral"}}]}]}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6"}
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"input_tokens":42}`)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	svc := &GatewayService{
		cfg: cfg, httpUpstream: upstream, rateLimitService: &RateLimitService{}, identityService: &IdentityService{},
		settingService: NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			SettingKeyEnableFingerprintUnification:           "true",
			SettingKeyEnableCCHSigning:                       "true",
			SettingKeyEnableAnthropicCacheTTL1hInjection:     "false",
			SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
			SettingKeyRewriteMessageCacheControl:             "false",
		}}, cfg),
	}
	account := &Account{ID: 6995, Platform: PlatformAnthropic, Type: AccountTypeSetupToken,
		Concurrency: 1, Credentials: map[string]any{"access_token": "test-oauth-token"}, Status: StatusActive, Schedulable: true}
	profile := newClaudeFrozenEnvironmentProfile(account, nil)
	profile.BetaSet = []string{claude.BetaOAuth}
	svc.claudeFrozenProfiles.Store(account.ID, profile)
	require.NoError(t, svc.ForwardCountTokens(context.Background(), c, account, parsed))
	_, messagePaths, toolPaths, systemPaths := collectCacheControlPaths(upstream.lastBody)
	require.Equal(t, maxCacheControlBlocks, len(messagePaths)+len(toolPaths)+len(systemPaths))
	require.Equal(t, claudeCodeSystemPrompt, gjson.GetBytes(upstream.lastBody, "system.0.text").String())
	require.Equal(t, "stable client prefix", gjson.GetBytes(upstream.lastBody, "system.2.text").String())
	require.Equal(t, "1h", gjson.GetBytes(upstream.lastBody, "system.2.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages.0.output_config").Exists())
	require.Len(t, gjson.GetBytes(upstream.lastBody, "messages").Array(), 3)
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_tokens").Exists())
	require.Equal(t, gjson.GetBytes(body, "system.0.text").String(), gjson.GetBytes(upstream.lastBody, "system.1.text").String())
	require.Equal(t, upstream.lastBody, parsed.Body.Bytes(), "accepted request snapshot must retain the actual wire body")
}
