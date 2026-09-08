package handler

import (
	"fmt"
	"net/http"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Endpoints that default an omitted model must enforce the allowlist after parsing.
func checkGatewayModelAllowlist(c *gin.Context, group *service.Group, model string) bool {
	if group == nil || group.ModelAllowlist.Allows(model) {
		return true
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
	middleware2.MarkIngressRejected(c, middleware2.IngressRejectModelNotAllowed)
	middleware2.OpenAIErrorWriter(c, http.StatusNotFound, fmt.Sprintf("Model %q is not available for this group", model))
	return false
}
