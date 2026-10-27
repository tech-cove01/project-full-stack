package social

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

// Register mounts the expected-state follow write, the caller's own
// relationship and the follow feed. All three need a session: a feed is personal.
func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.PUT("/authors/:id/follow", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			Follow *bool `json:"follow"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		if body.Follow == nil {
			// An absent field would make the call ambiguous; the client must say
			// which state it wants instead of relying on a toggle.
			writeError(c, ErrInput)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		relationship, err := service.SetFollow(ctx, user.ID, c.Param("id"), *body.Follow)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: relationship})
	})
	// Expected-state like: the body must say which state the caller wants.
	routes.PUT("/notes/:id/like", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			Liked *bool `json:"liked"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		if body.Liked == nil {
			writeError(c, ErrInput)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		state, err := service.SetLike(ctx, user.ID, c.Param("id"), *body.Liked)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: state})
	})
	routes.GET("/notes/:id/like", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		state, err := service.LikeState(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: state})
	})
	routes.GET("/authors/:id/relationship", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		relationship, err := service.Relationship(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: relationship})
	})
	routes.GET("/feed/notes", auth.RequireLogin(identity), func(c *gin.Context) {
		limit := feedDefaultLimit
		if raw := c.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 || parsed > feedMaxLimit {
				writeError(c, ErrPagination)
				return
			}
			limit = parsed
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		page, err := service.Feed(ctx, user.ID, c.Query("cursorTime"), c.Query("cursorId"), limit)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: page})
	})
}

func decodeBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInput
	}
	return nil
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	switch {
	case errors.Is(err, ErrInput), errors.Is(err, ErrPagination), errors.Is(err, ErrSelf):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoteNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrUnsafe):
		status, message = http.StatusServiceUnavailable, ErrUnsafe.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
