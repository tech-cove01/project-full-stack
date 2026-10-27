// Package httpapi owns transport behavior shared by future business routes.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Probe interface {
	Check(context.Context) error
}

// Result retains the existing frontend error envelope. Business handlers can
// explicitly select data/null/empty-array behavior as their contracts require.
type Result struct {
	Success  bool    `json:"success"`
	ErrorMsg string  `json:"errorMsg,omitempty"`
	Data     any     `json:"data,omitempty"`
	Total    *string `json:"total,omitempty"`
}

type health struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Runtime string `json:"runtime"`
	Version string `json:"version"`
}

// RegisterRoutes permits later modules to add their routes without giving those
// modules ownership of the server or the dependency lifecycle.
type RegisterRoutes func(gin.IRouter)

func NewRouter(probe Probe, probeTimeout time.Duration, version string, logger *slog.Logger, register ...RegisterRoutes) *gin.Engine {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	// Authentication uses the explicit authorization header, never a client IP
	// inferred from an untrusted forwarding header.
	_ = router.SetTrustedProxies(nil)
	router.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				// Neither panic values nor request contents are safe to log here.
				logger.Error("HTTP handler panic recovered")
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, Result{ErrorMsg: "服务暂时无法处理请求，请稍后重试。"})
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	})
	if version == "" {
		version = "unknown"
	}
	response := func(status string) health {
		return health{Status: status, Service: "project-full-stack-go", Runtime: "go", Version: version}
	}
	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, response("UP"))
	})
	ready := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
		defer cancel()
		if probe == nil || probe.Check(ctx) != nil {
			c.JSON(http.StatusServiceUnavailable, response("DOWN"))
			return
		}
		c.JSON(http.StatusOK, response("UP"))
	}
	router.GET("/actuator/health", ready)
	router.GET("/health/ready", ready)
	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, Result{ErrorMsg: "接口不存在。"})
	})
	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, Result{ErrorMsg: "请求方法不支持。"})
	})
	for _, addRoutes := range register {
		addRoutes(router)
	}
	return router
}
