//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUserConvertSKForwardsSKAsCookiePayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var got struct {
		Method         string
		CookieHeader   string
		AcceptLanguage string
		Origin         string
		Referer        string
		Body           map[string]string
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.CookieHeader = r.Header.Get("Cookie")
		got.AcceptLanguage = r.Header.Get("Accept-Language")
		got.Origin = r.Header.Get("Origin")
		got.Referer = r.Header.Get("Referer")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got.Body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://example.com/sub"}`))
	}))
	defer upstream.Close()

	t.Setenv(envUserConvertURL, upstream.URL+"/api/user/convert")
	t.Setenv(envUserConvertCookie, "auth_token=token; auth_role=user")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/convert", strings.NewReader(`{"sk":"sk-test-value"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Accept-Language", "zh-CN")

	(&UserHandler{}).ConvertSK(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"url":"https://example.com/sub"}`, recorder.Body.String())
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "auth_token=token; auth_role=user", got.CookieHeader)
	require.Equal(t, "zh-CN", got.AcceptLanguage)
	require.Equal(t, upstream.URL, got.Origin)
	require.Equal(t, upstream.URL+"/dashboard/convert", got.Referer)
	require.Equal(t, map[string]string{"cookie": "sk-test-value"}, got.Body)
}

func TestUserConvertSKRequiresSK(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/convert", strings.NewReader(`{"sk":"  "}`))
	c.Request.Header.Set("Content-Type", "application/json")

	(&UserHandler{}).ConvertSK(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "sk is required")
}

func TestUserConvertSKRequiresConfiguredUpstreamCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv(envUserConvertURL, "https://example.com/api/user/convert")
	t.Setenv(envUserConvertCookie, "")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/convert", strings.NewReader(`{"sk":"sk-test"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	(&UserHandler{}).ConvertSK(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), envUserConvertCookie)
}
