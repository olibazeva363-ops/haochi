//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayTextHandlers_RejectBlankModelBeforeForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoints := []struct {
		name          string
		path          string
		openAIHandle  func(*OpenAIGatewayHandler, *gin.Context)
		gatewayHandle func(*GatewayHandler, *gin.Context)
	}{
		{name: "openai responses", path: "/v1/responses", openAIHandle: (*OpenAIGatewayHandler).Responses},
		{name: "openai chat completions", path: "/v1/chat/completions", openAIHandle: (*OpenAIGatewayHandler).ChatCompletions},
		{name: "openai messages", path: "/v1/messages", openAIHandle: (*OpenAIGatewayHandler).Messages},
		{name: "gateway responses", path: "/v1/responses", gatewayHandle: (*GatewayHandler).Responses},
		{name: "gateway chat completions", path: "/v1/chat/completions", gatewayHandle: (*GatewayHandler).ChatCompletions},
	}
	models := []struct {
		name    string
		model   string
		omitted bool
	}{
		{name: "space", model: " "},
		{name: "mixed whitespace", model: "\t\r\n"},
		{name: "empty"},
		{name: "omitted", omitted: true},
	}
	for _, endpoint := range endpoints {
		for _, allowlistEnabled := range []bool{false, true} {
			allowlistName := "allowlist disabled"
			if allowlistEnabled {
				allowlistName = "allowlist enabled"
			}
			for _, model := range models {
				t.Run(endpoint.name+"/"+allowlistName+"/"+model.name, func(t *testing.T) {
					payload := map[string]any{"stream": false}
					if endpoint.path == "/v1/responses" {
						payload["input"] = "hello"
					} else {
						payload["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
						if endpoint.path == "/v1/messages" {
							payload["max_tokens"] = 64
						}
					}
					if !model.omitted {
						payload["model"] = model.model
					}
					body, err := json.Marshal(payload)
					require.NoError(t, err)
					group := &service.Group{
						ID: 3, Platform: service.PlatformOpenAI, AllowMessagesDispatch: true,
						ModelAllowlist: service.GroupModelAllowlist{
							Enabled: allowlistEnabled,
							Models:  []string{"gpt-5.1"},
						},
					}
					if endpoint.gatewayHandle != nil {
						group.Platform = service.PlatformAnthropic
					}
					c, rec := imageAllowlistContext(endpoint.path, "application/json", body, group)
					if endpoint.openAIHandle != nil {
						upstream := &imageAllowlistUpstreamProbe{}
						h := newOpenAIResponsesFailoverTestHandler(t, upstream)
						endpoint.openAIHandle(h, c)
						require.Zero(t, upstream.calls, "blank models must be rejected before upstream forwarding")
					} else {
						endpoint.gatewayHandle(&GatewayHandler{}, c)
					}

					require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
					errorField := "error.type"
					if endpoint.gatewayHandle != nil && endpoint.path == "/v1/responses" {
						errorField = "error.code"
					}
					require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), errorField).String())
					require.Equal(t, "model is required", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
				})
			}
		}
	}
}
