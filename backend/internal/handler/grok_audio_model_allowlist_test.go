//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokRealtime_ModelAllowlistAfterDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		model     string
		allowed   string
		disabled  bool
		wantModel string
		wantDeny  bool
	}{
		{name: "omitted model denied", allowed: "grok-4.5", wantModel: "grok-voice-latest", wantDeny: true},
		{name: "omitted model allowed", allowed: "grok-voice-latest"},
		{name: "omitted model wildcard allowed", allowed: "grok-voice-*"},
		{name: "blank model denied", model: " \t ", allowed: "grok-4.5", wantModel: "grok-voice-latest", wantDeny: true},
		{name: "blank model allowed", model: " \t ", allowed: "grok-voice-latest"},
		{name: "explicit model denied", model: "grok-voice-v2", allowed: "grok-voice-latest", wantModel: "grok-voice-v2", wantDeny: true},
		{name: "explicit model allowed", model: "grok-voice-v2", allowed: "grok-voice-v2"},
		{name: "disabled allowlist preserves default", allowed: "grok-4.5", disabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &service.Group{
				ID: 3, Platform: service.PlatformGrok,
				ModelAllowlist: service.GroupModelAllowlist{Enabled: !tt.disabled, Models: []string{tt.allowed}},
			}
			path := "/v1/realtime"
			if tt.model != "" {
				path += "?model=" + url.QueryEscape(tt.model)
			}
			c, rec := imageAllowlistContext(path, "", nil, group)
			c.Request = httptest.NewRequest(http.MethodGet, path, nil)
			c.Request.Header.Set("Connection", "Upgrade")
			c.Request.Header.Set("Upgrade", "websocket")
			upstream := &imageAllowlistUpstreamProbe{}
			// This fixture has no Grok accounts: allowed requests reach the real
			// scheduler's no-account response without opening an external socket.
			h := newOpenAIResponsesFailoverTestHandler(t, upstream)

			h.GrokRealtime(c)

			if tt.wantDeny {
				requireImageAllowlistDenied(t, c, rec, tt.wantModel)
			} else {
				require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "No available Grok accounts")
				_, marked := middleware2.GetIngressRejectReason(c)
				require.False(t, marked)
			}
			require.Zero(t, upstream.calls)
		})
	}
}
