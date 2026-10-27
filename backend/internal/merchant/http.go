package merchant

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// Register migrates only the read-only workspace. Future business routes must
// attach their own concrete resource authorization and transaction checks.
func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.GET("/merchant/workspace", auth.RequireLogin(identity), func(c *gin.Context) {
		user, valid := auth.CurrentUser(c)
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, httpapi.Result{ErrorMsg: "请先登录"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		workspace, err := service.Workspace(ctx, user.ID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, httpapi.Result{ErrorMsg: "服务暂时无法处理请求，请稍后重试。"})
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: workspace})
	})
}
