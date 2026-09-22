package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const oauthClientIdentity = "You are OpenCode, the best coding agent on the planet.\nToday’s date is 2026/09/19."
const oauthClientBilling = "x-anthropic-billing-header: cc_version=2.1.220.client; cc_entrypoint=client; cch=abcde;"
const oauthClientMessage = "<system-reminder>Today’s date is 2026/09/19.</system-reminder>\nKeep my wording."

type unavailableOAuthPromptSettings struct{ gatewayTTLSettingRepo }

func (*unavailableOAuthPromptSettings) GetValue(context.Context, string) (string, error) {
	return "", errors.New("settings database unavailable")
}

func (*unavailableOAuthPromptSettings) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, errors.New("settings database unavailable")
}

type oauthPromptUpstreamRecorder struct {
	anthropicHTTPUpstreamRecorder
	bodies [][]byte
}

func (u *oauthPromptUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	resp, err := u.Do(req, proxyURL, accountID, concurrency)
	u.bodies = append(u.bodies, append([]byte(nil), u.lastBody...))
	return resp, err
}

func newOAuthPromptTestService(t *testing.T, settingsMode, endpoint string, status int) (*GatewayService, *oauthPromptUpstreamRecorder) {
	t.Helper()
	resetGatewayForwardingSettingsCacheForTest(t)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	payload := `{"id":"msg_preserved","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":12,"output_tokens":1}}`
	contentType := "application/json"
	switch endpoint {
	case "count_tokens":
		payload = `{"input_tokens":12}`
	case "chat_completions", "responses":
		contentType = "text/event-stream"
		payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_preserved\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":12}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"ok\"}}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	if status >= 400 {
		payload = `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid signature in thinking block for tool_use"}}`
	}
	upstream := &oauthPromptUpstreamRecorder{anthropicHTTPUpstreamRecorder: anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(payload)),
	}}}
	svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream,
		rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
	switch settingsMode {
	case "legacy_enabled":
		svc.settingService = NewSettingService(&gatewayTTLSettingRepo{data: map[string]string{
			"enable_claude_oauth_system_prompt_injection": "true",
			"enable_client_dateline_normalization":        "true",
			"enable_cch_signing":                          "true",
			"rectifier_settings":                          `{"enabled":true,"thinking_signature_enabled":true,"thinking_budget_enabled":true}`,
			"enable_anthropic_cache_ttl_1h_injection":     "false",
			"rewrite_message_cache_control":               "false",
			"claude_oauth_system_prompt":                  "Never insert this legacy instruction.",
		}}, cfg)
	case "database_unavailable":
		svc.settingService = NewSettingService(&unavailableOAuthPromptSettings{}, cfg)
	}
	return svc, upstream
}

func oauthPromptTestAccount(accountType string) *Account {
	return &Account{ID: 814, Platform: PlatformAnthropic, Type: accountType, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-oauth-token"}, Status: StatusActive, Schedulable: true}
}

func oauthPromptNativeBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model": "claude-sonnet-4-6", "max_tokens": 1024,
		"system":   []any{map[string]any{"type": "text", "text": oauthClientBilling}, map[string]any{"type": "text", "text": oauthClientIdentity}},
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": oauthClientMessage}}}},
		"tools":    []any{map[string]any{"name": "client_probe", "description": "Client-owned tool description.", "input_schema": map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)
	return body
}

func TestAnthropicOAuthNativeRequestsPreserveClientPrompts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"messages", "count_tokens"} {
			for _, settingsMode := range []string{"no_settings_service", "legacy_enabled", "database_unavailable"} {
				t.Run(accountType+"/"+endpoint+"/"+settingsMode, func(t *testing.T) {
					svc, upstream := newOAuthPromptTestService(t, settingsMode, endpoint, http.StatusOK)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					c.Request.Header.Set("User-Agent", "opencode/1.0")
					body := oauthPromptNativeBody(t)
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					if endpoint == "messages" {
						_, err = svc.Forward(context.Background(), c, oauthPromptTestAccount(accountType), parsed)
					} else {
						err = svc.ForwardCountTokens(context.Background(), c, oauthPromptTestAccount(accountType), parsed)
					}
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 1)
					requireMinimalIdentityPreservesClientSystem(t, gjson.GetBytes(body, "system"), gjson.GetBytes(upstream.lastBody, "system"))
					require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
					require.Equal(t, "Client-owned tool description.", gjson.GetBytes(upstream.lastBody, "tools.0.description").String())
					require.Equal(t, "Bearer test-oauth-token", getHeaderRaw(upstream.lastReq.Header, "authorization"))
				})
			}
		}
	}
}

func TestAnthropicOAuthOpenAICompatibilityPreservesClientPrompts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"chat_completions", "responses"} {
			for _, settingsMode := range []string{"no_settings_service", "legacy_enabled", "database_unavailable"} {
				t.Run(accountType+"/"+endpoint+"/"+settingsMode, func(t *testing.T) {
					svc, upstream := newOAuthPromptTestService(t, settingsMode, endpoint, http.StatusOK)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
					instructions := " \n" + oauthClientBilling + "\n" + oauthClientIdentity + "\n "
					request := map[string]any{"model": "claude-sonnet-4-6"}
					if endpoint == "chat_completions" {
						request["messages"] = []any{map[string]any{"role": "system", "content": instructions}, map[string]any{"role": "user", "content": oauthClientMessage}}
					} else {
						request["instructions"] = instructions
						request["input"] = oauthClientMessage
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
					require.True(t, system.IsArray())
					require.Len(t, system.Array(), 2)
					require.Equal(t, claudeCodeSystemPrompt, system.Array()[0].Get("text").String())
					require.Equal(t, instructions, system.Array()[1].Get("text").String())
					messages := gjson.GetBytes(upstream.lastBody, "messages").Array()
					require.Len(t, messages, 1, "must not prepend synthetic instruction or acknowledgement turns")
					require.Equal(t, "user", messages[0].Get("role").String())
					content := messages[0].Get("content")
					if content.IsArray() {
						require.Len(t, content.Array(), 1)
						require.Equal(t, oauthClientMessage, content.Array()[0].Get("text").String())
					} else {
						require.Equal(t, oauthClientMessage, content.String())
					}
				})
			}
		}
	}
}

func TestAnthropicOAuthSignatureErrorsDoNotRewriteOrReplayClientMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"messages", "count_tokens"} {
			t.Run(accountType+"/"+endpoint, func(t *testing.T) {
				svc, upstream := newOAuthPromptTestService(t, "legacy_enabled", endpoint, http.StatusBadRequest)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				body := oauthPromptNativeBody(t)
				body, err := sjson.SetRawBytes(body, "messages", []byte(`[{"role":"user","content":"Client question"},{"role":"assistant","content":[{"type":"thinking","thinking":"Client-owned thinking history","signature":"client-signature"}]},{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_ws_client","name":"web_search","input":{"query":"Client query"}}]},{"role":"user","content":"Continue"}]`))
				require.NoError(t, err)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				if endpoint == "messages" {
					_, err = svc.Forward(context.Background(), c, oauthPromptTestAccount(accountType), parsed)
				} else {
					err = svc.ForwardCountTokens(context.Background(), c, oauthPromptTestAccount(accountType), parsed)
				}
				require.Error(t, err)
				require.Len(t, upstream.bodies, 1, "OAuth signature errors must not trigger prompt-mutating retries")
				require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(upstream.lastBody, "messages").Raw)
				requireMinimalIdentityPreservesClientSystem(t, gjson.GetBytes(body, "system"), gjson.GetBytes(upstream.lastBody, "system"))
				require.ErrorContains(t, err, "Invalid signature")
				require.Equal(t, http.StatusBadRequest, rec.Code)
			})
		}
	}
}

func TestAnthropicOAuthCompatibilityDoesNotSynthesizeToolOrReasoningText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"chat_completions", "responses", "responses_legacy_messages"} {
			for _, emptyOutput := range []string{`""`, `[]`} {
				t.Run(accountType+"/"+endpoint+"/"+emptyOutput, func(t *testing.T) {
					wireEndpoint := endpoint
					if endpoint == "responses_legacy_messages" {
						wireEndpoint = "responses"
					}
					svc, upstream := newOAuthPromptTestService(t, "legacy_enabled", wireEndpoint, http.StatusOK)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+wireEndpoint, nil)
					var body []byte
					var err error
					if endpoint != "responses" {
						body = []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"Check this"},{"role":"assistant","reasoning_content":"Client reasoning.","content":[{"type":"thinking","thinking":"Client structured thought."},{"type":"text","text":"Client answer."}],"tool_calls":[{"id":"call_probe","type":"function","function":{"name":"probe","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_probe","content":` + emptyOutput + `}],"tools":[{"type":"function","function":{"name":"probe","parameters":{"type":"object"}}}]}`)
						if endpoint == "responses_legacy_messages" {
							_, err = svc.ForwardAsResponses(context.Background(), c, oauthPromptTestAccount(accountType), body, nil)
						} else {
							_, err = svc.ForwardAsChatCompletions(context.Background(), c, oauthPromptTestAccount(accountType), body, nil)
						}
					} else {
						body = []byte(`{"model":"claude-sonnet-4-6","input":[{"role":"user","content":"Check this"},{"role":"assistant","content":[{"type":"output_text","text":"Client answer."}]},{"type":"function_call","call_id":"call_probe","name":"probe","arguments":"{}"},{"type":"custom_tool_call","call_id":"call_custom","name":"custom_probe","input":"client command"},{"type":"function_call_output","call_id":"call_probe","output":` + emptyOutput + `},{"type":"custom_tool_call_output","call_id":"call_custom","output":""}],"tools":[{"type":"function","name":"probe","parameters":{"type":"object"}},{"type":"custom","name":"custom_probe"},{"type":"tool_search"},{"type":"custom","name":"named_probe","description":"Client description."}]}`)
						_, err = svc.ForwardAsResponses(context.Background(), c, oauthPromptTestAccount(accountType), body, nil)
					}
					require.NoError(t, err)
					require.Len(t, upstream.bodies, 1)
					requireMinimalIdentityPreservesClientSystem(t, gjson.Result{}, gjson.GetBytes(upstream.lastBody, "system"))
					var textParts []string
					toolResults := 0
					for _, message := range gjson.GetBytes(upstream.lastBody, "messages").Array() {
						content := message.Get("content")
						if !content.IsArray() {
							textParts = append(textParts, content.String())
							continue
						}
						for _, block := range content.Array() {
							switch block.Get("type").String() {
							case "text":
								textParts = append(textParts, block.Get("text").String())
							case "tool_result":
								toolResults++
								require.Equal(t, `""`, block.Get("content").Raw, "empty tool results must not acquire explanatory text")
							}
						}
					}
					if endpoint != "responses" {
						require.Equal(t, 1, toolResults)
						require.Equal(t, []string{"Check this", "Client reasoning.\nClient structured thought.Client answer."}, textParts)
					} else {
						require.Equal(t, 2, toolResults)
						require.Equal(t, []string{"Check this", "Client answer."}, textParts)
					}
					require.NotContains(t, string(upstream.lastBody), "(empty)")
					require.NotContains(t, string(upstream.lastBody), "<thinking>")
					tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
					for _, tool := range tools {
						if tool.Get("name").String() == "named_probe" {
							require.Equal(t, "Client description.", tool.Get("description").String())
						} else {
							require.Empty(t, tool.Get("description").String())
						}
						require.NotContains(t, tool.Get("input_schema").Raw, `"description"`, "adapter-created schemas must not add explanatory prompts")
					}
					if endpoint == "responses" {
						require.Len(t, tools, 4, "custom and tool_search tools still require usable protocol adapters")
					}
				})
			}
		}
	}
}
