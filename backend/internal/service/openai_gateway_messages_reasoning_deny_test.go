//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsAnthropic_ReasoningDenyStopsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, route := range []string{"responses", "chat_completions"} {
		for _, mappingDeny := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				name := fmt.Sprintf("%s/mapping_deny=%t/stream=%t", route, mappingDeny, stream)
				t.Run(name, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":"gpt-5.6-luna","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"max"},"stream":%t}`, stream))
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")

					account := rawChatCompletionsTestAccount()
					account.Extra = map[string]any{
						openai_compat.ExtraKeyResponsesMode:      string(openai_compat.ResponsesSupportModeAuto),
						openai_compat.ExtraKeyResponsesSupported: true,
					}
					if route == "chat_completions" {
						account = forceChatMessagesFallbackAccount()
					}
					upstream := &httpUpstreamRecorder{err: errors.New("unexpected upstream request")}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "medium", nil, ReasoningEffortOverLimitDeny)
					if mappingDeny {
						ctx = WithOpenAIReasoningEffortPolicy(context.Background(), "", []ReasoningEffortMapping{
							{From: "max", To: ReasoningEffortMappingDeny},
						}, ReasoningEffortOverLimitDowngrade)
					}

					result, err := svc.ForwardAsAnthropic(ctx, c, account, body, "", "")

					require.Nil(t, result)
					require.Error(t, err)
					if mappingDeny {
						var denied *ReasoningEffortMappingDeniedError
						require.ErrorAs(t, err, &denied)
						require.Equal(t, "max", denied.Requested)
					} else {
						var denied *ReasoningEffortOverLimitError
						require.ErrorAs(t, err, &denied)
						require.Equal(t, "max", denied.Requested)
						require.Equal(t, "medium", denied.Max)
					}
					require.Equal(t, http.StatusForbidden, rec.Code)
					require.True(t, gjson.ValidBytes(rec.Body.Bytes()))
					require.Equal(t, "error", gjson.GetBytes(rec.Body.Bytes(), "type").String())
					require.Equal(t, "forbidden_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
					require.Equal(t, err.Error(), gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
					require.True(t, HasOpsClientBusinessLimited(c))
					require.Equal(t, OpsClientBusinessLimitedReasonLocalPolicyDenied, OpsClientBusinessLimitedReason(c))
					require.Empty(t, upstream.requests)
					require.Nil(t, upstream.lastReq)
				})
			}
		}
	}
}
