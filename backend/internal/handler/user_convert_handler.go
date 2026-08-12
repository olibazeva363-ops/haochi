package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

const (
	envUserConvertURL           = "SUB2API_CONVERT_URL"
	envUserConvertCookie        = "SUB2API_CONVERT_COOKIE"
	envUserConvertUserAgent     = "SUB2API_CONVERT_USER_AGENT"
	defaultUserConvertUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	userConvertTimeout          = 60 * time.Second
	userConvertMaxResponseBytes = 4 << 20
)

var userConvertHTTPClient = &http.Client{Timeout: userConvertTimeout}

type UserConvertRequest struct {
	SK     string `json:"sk"`
	Cookie string `json:"cookie"`
}

// ConvertSK forwards a single sk value to the upstream conversion endpoint.
// POST /api/v1/user/convert
func (h *UserHandler) ConvertSK(c *gin.Context) {
	var req UserConvertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	sk := strings.TrimSpace(req.SK)
	if sk == "" {
		sk = strings.TrimSpace(req.Cookie)
	}
	if sk == "" {
		response.BadRequest(c, "sk is required")
		return
	}

	upstreamURL := strings.TrimSpace(os.Getenv(envUserConvertURL))
	if upstreamURL == "" {
		response.Error(c, http.StatusServiceUnavailable, envUserConvertURL+" is not configured")
		return
	}

	upstreamCookie := strings.TrimSpace(os.Getenv(envUserConvertCookie))
	if upstreamCookie == "" {
		response.Error(c, http.StatusServiceUnavailable, envUserConvertCookie+" is not configured")
		return
	}
	parsedURL, err := url.Parse(upstreamURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		response.InternalError(c, "Invalid conversion upstream URL")
		return
	}

	payload, err := json.Marshal(map[string]string{"cookie": sk})
	if err != nil {
		response.InternalError(c, "Failed to build conversion request")
		return
	}

	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, parsedURL.String(), bytes.NewReader(payload))
	if err != nil {
		response.InternalError(c, "Failed to build conversion request")
		return
	}
	applyUserConvertHeaders(upstreamReq, parsedURL, c.GetHeader("Accept-Language"), upstreamCookie)

	upstreamResp, err := userConvertHTTPClient.Do(upstreamReq)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Conversion upstream request failed")
		return
	}
	defer upstreamResp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(upstreamResp.Body, userConvertMaxResponseBytes+1))
	if err != nil {
		response.Error(c, http.StatusBadGateway, "Failed to read conversion upstream response")
		return
	}
	if len(body) > userConvertMaxResponseBytes {
		response.Error(c, http.StatusBadGateway, "Conversion upstream response is too large")
		return
	}

	contentType := strings.TrimSpace(upstreamResp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(upstreamResp.StatusCode, contentType, body)
}

func applyUserConvertHeaders(req *http.Request, upstreamURL *url.URL, acceptLanguage, cookie string) {
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	if acceptLanguage = strings.TrimSpace(acceptLanguage); acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	} else {
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	}
	req.Header.Set("Cookie", cookie)

	origin := upstreamURL.Scheme + "://" + upstreamURL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/dashboard/convert")

	userAgent := strings.TrimSpace(os.Getenv(envUserConvertUserAgent))
	if userAgent == "" {
		userAgent = defaultUserConvertUserAgent
	}
	req.Header.Set("User-Agent", userAgent)
}
