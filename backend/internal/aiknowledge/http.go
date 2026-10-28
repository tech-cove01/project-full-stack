package aiknowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// Register mounts the merchant FAQ surface and the anonymous knowledge endpoint.
// The public route returns only a published, non-disabled version.
func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.GET("/merchant/knowledge", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		items, err := service.List(ctx, user.ID, c.Query("shopId"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: items})
	})
	routes.POST("/merchant/knowledge", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			ShopID string `json:"shopId"`
			Title  string `json:"title"`
			Body   string `json:"body"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.CreateDraft(ctx, user.ID, body.ShopID, body.Title, body.Body)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	routes.PUT("/merchant/knowledge/:id", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.UpdateDraft(ctx, user.ID, c.Param("id"), body.Title, body.Body)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	routes.POST("/merchant/knowledge/:id/publish", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Publish(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	routes.POST("/merchant/knowledge/:id/disable", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Disable(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	// Anonymous retrieval returns only published, non-disabled chunks; without a
	// shopId it is limited to platform-scope knowledge.
	routes.POST("/merchant/knowledge/:id/rebuild", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Rebuild(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	routes.GET("/ai/search", func(c *gin.Context) {
		var shop *uint64
		if raw := c.Query("shopId"); raw != "" {
			parsed, err := positive(raw)
			if err != nil {
				writeError(c, err)
				return
			}
			shop = &parsed
		}
		limit := 5
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 || parsed > 20 {
				writeError(c, ErrInput)
				return
			}
			limit = parsed
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		hits, err := service.Search(ctx, c.Query("q"), shop, limit, minScore)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: hits})
	})
	routes.GET("/ai/knowledge/:id", func(c *gin.Context) {
		var version *int
		if raw := c.Query("version"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				writeError(c, ErrInput)
				return
			}
			version = &parsed
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Public(ctx, c.Param("id"), version)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
}

func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 10*time.Second)
}

// decodeBody is a whitelist: identity, shop ownership and status are never read
// from the request.
func decodeBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInput
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInput
	}
	return nil
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	switch {
	case errors.Is(err, ErrInput):
		status, message = http.StatusBadRequest, ErrInput.Error()
	case errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, ErrConflict.Error()
	case errors.Is(err, ErrUnsafe):
		status, message = http.StatusServiceUnavailable, ErrUnsafe.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
