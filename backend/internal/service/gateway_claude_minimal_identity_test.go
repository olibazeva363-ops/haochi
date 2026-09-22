package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func requireMinimalIdentityPreservesClientSystem(t *testing.T, original, actual gjson.Result) {
	t.Helper()
	require.True(t, actual.IsArray())
	blocks := actual.Array()
	require.NotEmpty(t, blocks)
	require.JSONEq(t, `{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."}`, blocks[0].Raw,
		"the only generated block must contain the minimal identity sentence")
	switch {
	case !original.Exists() || original.Type == gjson.Null || (original.Type == gjson.String && original.String() == ""):
		require.Len(t, blocks, 1)
	case original.Type == gjson.String:
		require.Len(t, blocks, 2)
		require.Equal(t, "text", blocks[1].Get("type").String())
		require.Equal(t, original.String(), blocks[1].Get("text").String())
	default:
		clientBlocks := original.Array()
		require.Len(t, blocks, len(clientBlocks)+1)
		for i, clientBlock := range clientBlocks {
			require.JSONEq(t, clientBlock.Raw, blocks[i+1].Raw, "client block %d must keep its text and metadata", i)
		}
	}
}

func TestAnthropicOAuthMinimalIdentityNativeRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"messages", "count_tokens"} {
			for _, tc := range []struct {
				name         string
				system       string
				claudeClient bool
				alreadyHasID bool
			}{
				{name: "missing"},
				{name: "null", system: `null`},
				{name: "empty_string", system: `""`},
				{name: "empty_array", system: `[]`},
				{name: "client_string", system: `"  You are OpenCode.\nToday’s date is 2026/09/19.\n "`},
				{name: "client_blocks", system: `[{"type":"text","text":"Client-owned instructions.","cache_control":{"type":"ephemeral","ttl":"1h"}}]`},
				{name: "claude_client_billing_without_identity", claudeClient: true, system: `[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.client; cc_entrypoint=cli; cch=abcde;"}]`},
				{name: "claude_client_existing_identity", claudeClient: true, alreadyHasID: true, system: `[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.220.client; cc_entrypoint=cli; cch=abcde;"},{"type":"text","text":"\nYou are Claude Code, Anthropic's official CLI for Claude.\nClient-owned continuation.","cache_control":{"type":"ephemeral"}}]`},
			} {
				t.Run(accountType+"/"+endpoint+"/"+tc.name, func(t *testing.T) {
					svc, upstream := newOAuthPromptTestService(t, "legacy_enabled", endpoint, http.StatusOK)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					ctx := context.Background()
					if tc.claudeClient {
						ctx = SetClaudeCodeClient(ctx, true)
						c.Request.Header.Set("User-Agent", "claude-cli/2.1.220 (external, cli)")
					}
					body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"Keep my wording. Today’s date is 2026/09/19."}]}`)
					if tc.system != "" {
						var err error
						body, err = sjson.SetRawBytes(body, "system", []byte(tc.system))
						require.NoError(t, err)
					}
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					if endpoint == "messages" {
						_, err = svc.Forward(ctx, c, oauthPromptTestAccount(accountType), parsed)
					} else {
						err = svc.ForwardCountTokens(ctx, c, oauthPromptTestAccount(accountType), parsed)
					}
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 1)
					if tc.alreadyHasID {
						require.JSONEq(t, tc.system, gjson.GetBytes(upstream.lastBody, "system").Raw)
					} else {
						requireMinimalIdentityPreservesClientSystem(t, gjson.GetBytes(body, "system"), gjson.GetBytes(upstream.lastBody, "system"))
					}
					require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
					require.NotContains(t, string(upstream.lastBody), "Never insert this legacy instruction.")
				})
			}
		}
	}
}

func TestAnthropicOAuthMinimalIdentityOpenAIClientsDoNotDuplicateIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"chat_completions", "responses"} {
			t.Run(accountType+"/"+endpoint, func(t *testing.T) {
				svc, upstream := newOAuthPromptTestService(t, "legacy_enabled", endpoint, http.StatusOK)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
				instructions := " \n" + claudeCodeSystemPrompt + "\nClient-owned continuation.\n "
				request := map[string]any{"model": "claude-sonnet-4-6"}
				if endpoint == "chat_completions" {
					request["messages"] = []any{map[string]any{"role": "system", "content": instructions}, map[string]any{"role": "user", "content": oauthClientMessage}}
				} else {
					request["instructions"], request["input"] = instructions, oauthClientMessage
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				if endpoint == "chat_completions" {
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, oauthPromptTestAccount(accountType), body, nil)
				} else {
					_, err = svc.ForwardAsResponses(context.Background(), c, oauthPromptTestAccount(accountType), body, nil)
				}
				require.NoError(t, err)
				require.Len(t, upstream.bodies, 1)
				system := gjson.GetBytes(upstream.lastBody, "system")
				if system.IsArray() {
					require.Len(t, system.Array(), 1)
					require.Equal(t, instructions, system.Array()[0].Get("text").String())
				} else {
					require.Equal(t, instructions, system.String())
				}
			})
		}
	}
}

func TestAnthropicMinimalIdentityDoesNotAffectAPIKeyOrVertexRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeServiceAccount} {
		for _, endpoint := range []string{"messages", "count_tokens"} {
			t.Run(accountType+"/"+endpoint, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				svc := &GatewayService{cfg: &config.Config{}}
				account := &Account{ID: 815, Platform: PlatformAnthropic, Type: accountType,
					Credentials: map[string]any{"project_id": "vertex-test", "location": "us-east5"}}
				body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"system":"Client-only system.","messages":[{"role":"user","content":"hello"}]}`)
				var req *http.Request
				var wireBody []byte
				var err error
				if endpoint == "messages" {
					req, wireBody, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", accountType, "claude-sonnet-4-6", false, false)
				} else {
					req, wireBody, err = svc.buildCountTokensRequest(context.Background(), c, account, body, "test-token", accountType, "claude-sonnet-4-6", false)
				}
				require.NoError(t, err)
				require.NoError(t, req.Body.Close())
				require.Equal(t, "Client-only system.", gjson.GetBytes(wireBody, "system").String())
				require.NotContains(t, string(wireBody), claudeCodeSystemPrompt)
			})
		}
	}
}

func TestAnthropicOAuthMinimalIdentityAccountConnectionProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			_, upstream := newOAuthPromptTestService(t, "legacy_enabled", "responses", http.StatusOK)
			svc := &AccountTestService{httpUpstream: upstream}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/814/test", nil)
			err := svc.testClaudeAccountConnection(c, oauthPromptTestAccount(accountType), "claude-sonnet-4-6")
			require.NoError(t, err)
			require.Len(t, upstream.bodies, 1)
			requireMinimalIdentityPreservesClientSystem(t, gjson.Result{}, gjson.GetBytes(upstream.lastBody, "system"))
			require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.text").String())
			require.NotContains(t, string(upstream.lastBody), "x-anthropic-billing-header:")
			require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
		})
	}
}
