package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// UpdateConvertCookieRequest is the payload for saving the converter-station cookie.
type UpdateConvertCookieRequest struct {
	Cookie string `json:"cookie"`
}

// GetConvertCookieStatus returns the sanitized status of the effective converter
// cookie (never the raw value).
// GET /api/v1/admin/accounts/import/sk/cookie
func (h *SettingHandler) GetConvertCookieStatus(c *gin.Context) {
	status := h.settingService.GetConvertCookieStatus(c.Request.Context())
	response.Success(c, status)
}

// UpdateConvertCookie persists a new converter-station cookie. Takes effect
// immediately for all conversion paths without a restart.
// PUT /api/v1/admin/accounts/import/sk/cookie
func (h *SettingHandler) UpdateConvertCookie(c *gin.Context) {
	var req UpdateConvertCookieRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cookie := strings.TrimSpace(req.Cookie)
	if cookie == "" {
		response.BadRequest(c, "cookie 不能为空")
		return
	}

	status, err := h.settingService.SetConvertCookie(c.Request.Context(), cookie)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

// ClearConvertCookie removes the stored converter cookie, falling back to the
// SUB2API_CONVERT_COOKIE environment variable.
// DELETE /api/v1/admin/accounts/import/sk/cookie
func (h *SettingHandler) ClearConvertCookie(c *gin.Context) {
	status, err := h.settingService.ClearConvertCookie(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
