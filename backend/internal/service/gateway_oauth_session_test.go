package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClaudeOAuthSessionDiscriminatorStaysStableAcrossClientNetworkChanges(t *testing.T) {
	first := &SessionContext{ClientIP: "192.0.2.10", UserAgent: "client-a/1.0", APIKeyID: 42}
	second := &SessionContext{ClientIP: "198.51.100.20", UserAgent: "client-b/2.0", APIKeyID: 42}
	otherKey := &SessionContext{ClientIP: "192.0.2.10", UserAgent: "client-a/1.0", APIKeyID: 43}

	require.Equal(t, "api-key:42", claudeOAuthSessionDiscriminator(nil, first, nil))
	require.Equal(t, claudeOAuthSessionDiscriminator(nil, first, nil), claudeOAuthSessionDiscriminator(nil, second, nil))
	require.NotEqual(t, claudeOAuthSessionDiscriminator(nil, first, nil), claudeOAuthSessionDiscriminator(nil, otherKey, nil))
}

func TestClaudeOAuthSessionDiscriminatorPrefersExplicitClaudeSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "session-a")
	c.Set("api_key", &APIKey{ID: 42})

	first := claudeOAuthSessionDiscriminator(c, &SessionContext{APIKeyID: 42}, nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "session-b")
	second := claudeOAuthSessionDiscriminator(c, &SessionContext{APIKeyID: 42}, nil)

	require.Equal(t, hashClaudeOAuthDiscriminator("session", "session-a"), first)
	require.NotEqual(t, first, second)
}

func TestClaudeOAuthSessionDiscriminatorUsesAuthenticatedAPIKeyContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("api_key", &APIKey{ID: 99})

	require.Equal(t, "api-key:99", claudeOAuthSessionDiscriminator(c, nil, &Fingerprint{ClientID: "device"}))
}
