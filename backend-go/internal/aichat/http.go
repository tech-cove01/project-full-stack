package aichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func Register(routes gin.IRouter, conversations *Conversations, assistant *Assistant,
	mode string, identity *auth.Service) {
	routes.GET("/ai/capabilities", func(c *gin.Context) {
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: map[string]any{
			"enabled": true, "mode": mode,
		}})
	})
	routes.POST("/ai/conversations", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			ShopID string `json:"shopId"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		var shop *uint64
		if body.ShopID != "" {
			parsed, err := strconv.ParseUint(body.ShopID, 10, 64)
			if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != body.ShopID {
				writeError(c, ErrInput)
				return
			}
			shop = &parsed
		}
		userID, ok := currentUser(c)
		if !ok {
			unauthorized(c)
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		conversation, err := conversations.Create(ctx, userID, shop)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: conversation})
	})
	routes.GET("/ai/conversations/:id", auth.RequireLogin(identity), func(c *gin.Context) {
		userID, ok := currentUser(c)
		if !ok {
			unauthorized(c)
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		messages, err := conversations.History(ctx, userID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: messages})
	})
	routes.DELETE("/ai/conversations/:id", auth.RequireLogin(identity), func(c *gin.Context) {
		userID, ok := currentUser(c)
		if !ok {
			unauthorized(c)
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		if err := conversations.Delete(ctx, userID, c.Param("id")); err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true})
	})
	routes.POST("/ai/conversations/:id/messages", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			ClientMessageID string `json:"clientMessageId"`
			Message         string `json:"message"`
			ShopID          string `json:"shopId"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		userID, ok := currentUser(c)
		if !ok {
			unauthorized(c)
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		exists, err := conversations.Exists(ctx, userID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		if !exists {
			// Checked before the stream starts, so the client still gets a JSON 404.
			writeError(c, ErrNotFound)
			return
		}
		var shop *uint64
		if body.ShopID != "" {
			parsed, err := strconv.ParseUint(body.ShopID, 10, 64)
			if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != body.ShopID {
				writeError(c, ErrInput)
				return
			}
			shop = &parsed
		}
		emit, err := startStream(c)
		if err != nil {
			writeError(c, err)
			return
		}
		if err = assistant.Ask(ctx, userID, c.Param("id"), shop, body.Message, emit); err != nil {
			// Headers are already sent; the only honest signal left is an error event.
			_ = emit("error", map[string]any{"code": codeOf(err), "message": messageOf(err)})
		}
	})
}

func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 45*time.Second)
}

func currentUser(c *gin.Context) (uint64, bool) {
	user, valid := auth.CurrentUser(c)
	if !valid {
		return 0, false
	}
	parsed, err := strconv.ParseUint(user.ID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return parsed, true
}

func decodeBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		return ErrInput
	}
	return nil
}

// startStream writes the SSE headers once and returns the emitter.
func startStream(c *gin.Context) (Emit, error) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming unsupported")
	}
	return func(event string, data any) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}, nil
}

func codeOf(err error) string {
	switch {
	case errors.Is(err, ErrInput):
		return "INVALID_INPUT"
	case errors.Is(err, ErrNotFound):
		return "CONVERSATION_NOT_FOUND"
	default:
		return "AI_UNAVAILABLE"
	}
}

func messageOf(err error) string {
	switch {
	case errors.Is(err, ErrInput):
		return ErrInput.Error()
	case errors.Is(err, ErrNotFound):
		return ErrNotFound.Error()
	default:
		return "AI 服务暂时不可用，请稍后重试。"
	}
}

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, httpapi.Result{ErrorMsg: "请先登录"})
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	switch {
	case errors.Is(err, ErrInput):
		status, message = http.StatusBadRequest, ErrInput.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
