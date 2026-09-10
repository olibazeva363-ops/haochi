//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type imageAllowlistUpstreamProbe struct {
	service.HTTPUpstream
	calls int
	body  []byte
}

func (u *imageAllowlistUpstreamProbe) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.body, _ = io.ReadAll(req.Body)
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"unknown_parameter","message":"allowlist upstream reached","param":"size"}}`,
		)),
	}, nil
}

func imageAllowlistRequest(t *testing.T, path, model string) ([]byte, string) {
	t.Helper()
	if !strings.Contains(path, "/edits") {
		payload := map[string]string{"prompt": "draw a square"}
		if model != "" {
			payload["model"] = model
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		return body, "application/json"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("prompt", "draw a square"))
	if model != "" {
		require.NoError(t, writer.WriteField("model", model))
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": []string{`form-data; name="image"; filename="input.png"`},
		"Content-Type":        []string{"image/png"},
	})
	require.NoError(t, err)
	_, err = part.Write([]byte("image fixture"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func imageAllowlistContext(path, contentType string, body []byte, group *service.Group) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: 9, UserID: 7, GroupID: &group.ID, Group: group, User: &service.User{ID: 7},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 7, Concurrency: 0})
	return c, rec
}

func setImageAllowlistPublicAlias(c *gin.Context, alias string) {
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
		Matched: true, Source: service.CompositeRouteSourceExplicit,
		PublicModel: alias, TargetPlatform: service.PlatformOpenAI, UpstreamModel: "gpt-image-2",
	}))
}

func requireImageAllowlistDenied(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder, model string) {
	t.Helper()
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Equal(t, "invalid_request_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "model_not_found", gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
	require.Contains(t, rec.Body.String(), model)
	require.Equal(t, service.OpsClientBusinessLimitedReasonLocalModelConfiguration, service.OpsClientBusinessLimitedReason(c))
	reason, marked := middleware2.GetIngressRejectReason(c)
	require.True(t, marked)
	require.Equal(t, middleware2.IngressRejectModelNotAllowed, reason)
}

func TestOpenAIGatewayHandlerImages_ModelAllowlistAfterDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		allowed  string
		model    string
		alias    string
		wantDeny bool
	}{
		{name: "omitted model denied", allowed: "gpt-image-1", wantDeny: true},
		{name: "omitted model allowed", allowed: "gpt-image-2"},
		{name: "image 2.5 flare allowed", allowed: "gpt-image-2.5-flare", model: "gpt-image-2.5-flare"},
		{name: "image 2.5 sunburst snapshot allowed", allowed: "gpt-image-2.5-sunburst-2026-09-08", model: "gpt-image-2.5-sunburst-2026-09-08"},
		{name: "image 2.5 blocked by old allowlist", allowed: "gpt-image-2", model: "gpt-image-2.5-flare", wantDeny: true},
		{name: "composite public alias allowed", allowed: "public-image", alias: "public-image"},
		{name: "upstream model does not allow blocked public alias", allowed: "gpt-image-2", alias: "public-image", wantDeny: true},
	}
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				group := &service.Group{
					ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true,
					ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{tt.allowed}},
				}
				model := tt.model
				if tt.alias != "" {
					group.Platform = service.PlatformComposite
					model = "gpt-image-2"
				}
				body, contentType := imageAllowlistRequest(t, path, model)
				c, rec := imageAllowlistContext(path, contentType, body, group)
				if tt.alias != "" {
					setImageAllowlistPublicAlias(c, tt.alias)
				}
				upstream := &imageAllowlistUpstreamProbe{}
				h := newOpenAIResponsesFailoverTestHandler(t, upstream)

				h.Images(c)

				if tt.wantDeny {
					blocked := model
					if blocked == "" {
						blocked = "gpt-image-2"
					}
					if tt.alias != "" {
						blocked = tt.alias
					}
					requireImageAllowlistDenied(t, c, rec, blocked)
					require.Zero(t, upstream.calls, "a denied model must never reach the upstream")
					return
				}
				require.Equal(t, 1, upstream.calls, rec.Body.String())
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Contains(t, rec.Body.String(), "allowlist upstream reached")
				forwardedModel := model
				if forwardedModel == "" {
					forwardedModel = "gpt-image-2"
				}
				require.Contains(t, string(upstream.body), forwardedModel)
				_, marked := middleware2.GetIngressRejectReason(c)
				require.False(t, marked)
			})
		}
	}
}

func TestAsyncImageHandlerSubmit_ModelAllowlistBeforeTaskCreation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		allowed  string
		model    string
		wantDeny bool
	}{
		{name: "default denied", allowed: "gpt-image-1", wantDeny: true},
		{name: "default allowed", allowed: "gpt-image-2"},
		{name: "image 2.5 flare allowed", allowed: "gpt-image-2.5-flare", model: "gpt-image-2.5-flare"},
		{name: "image 2.5 sunburst snapshot allowed", allowed: "gpt-image-2.5-sunburst-2026-09-08", model: "gpt-image-2.5-sunburst-2026-09-08"},
		{name: "image 2.5 blocked by old allowlist", allowed: "gpt-image-2", model: "gpt-image-2.5-flare", wantDeny: true},
	}
	for _, path := range []string{"/v1/images/generations/async", "/v1/images/edits/async"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				group := &service.Group{
					ID: 3, Platform: service.PlatformOpenAI, AllowImageGeneration: true,
					ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{tt.allowed}},
				}
				body, contentType := imageAllowlistRequest(t, path, tt.model)
				c, rec := imageAllowlistContext(path, contentType, body, group)
				store := &asyncImageMemoryStore{tasks: make(map[string]*service.ImageTaskRecord)}
				tasks := service.NewImageTaskServiceWithUploader(store, nil, time.Hour, time.Minute)
				h := NewAsyncImageHandler(tasks, &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}})
				executed := make(chan string, 1)
				h.execute = func(_ string, taskCtx *gin.Context) {
					parsed, err := h.openAI.gatewayService.ParseOpenAIImagesRequest(taskCtx, body)
					if err != nil {
						executed <- err.Error()
						taskCtx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
						return
					}
					executed <- parsed.Model
					taskCtx.JSON(http.StatusOK, gin.H{"data": []gin.H{{"url": "https://example.test/image.png"}}})
				}

				h.Submit(c)

				expectedModel := tt.model
				if expectedModel == "" {
					expectedModel = "gpt-image-2"
				}
				if tt.wantDeny {
					requireImageAllowlistDenied(t, c, rec, expectedModel)
					store.mu.RLock()
					count := len(store.tasks)
					store.mu.RUnlock()
					require.Zero(t, count, "denial must happen before persisting a task")
					select {
					case <-executed:
						t.Fatal("denied request started async execution")
					default:
					}
					return
				}
				require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
				taskID := gjson.GetBytes(rec.Body.Bytes(), "task_id").String()
				require.NotEmpty(t, taskID)
				require.Eventually(t, func() bool {
					task, err := tasks.Get(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 9}, taskID)
					return err == nil && task.Status == service.ImageTaskStatusCompleted
				}, time.Second, 10*time.Millisecond)
				require.Equal(t, expectedModel, <-executed)
			})
		}
	}
}

func TestAsyncImageHandlerValidateRequest_PreservesPublicModelForAllowlist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		ID: 3, Platform: service.PlatformComposite,
		ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"public-image"}},
	}
	path := "/v1/images/generations/async"
	body, contentType := imageAllowlistRequest(t, path, "gpt-image-2")
	c, rec := imageAllowlistContext(path, contentType, body, group)
	setImageAllowlistPublicAlias(c, "public-image")
	h := &AsyncImageHandler{openAI: &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}}

	model, err := h.validateRequest(c, service.PlatformOpenAI, body)

	require.NoError(t, err)
	require.Equal(t, "public-image", model)
	require.True(t, checkGatewayModelAllowlist(c, group, model))
	require.False(t, c.Writer.Written())
	group.ModelAllowlist.Models = []string{"gpt-image-2"}
	require.False(t, checkGatewayModelAllowlist(c, group, model))
	requireImageAllowlistDenied(t, c, rec, "public-image")
}
