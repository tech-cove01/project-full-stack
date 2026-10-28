package aichat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// Capabilities reports local configuration readiness. It deliberately does not
// probe a paid provider at startup or claim the remote endpoint is healthy.
type Capabilities struct {
	Enabled             bool   `json:"enabled"`
	Ready               bool   `json:"ready"`
	Mode                string `json:"mode"`
	Reason              string `json:"reason"`
	ChatModel           string `json:"chatModel"`
	EmbeddingModel      string `json:"embeddingModel"`
	EmbeddingDimensions int    `json:"embeddingDimensions"`
	ProviderHealth      string `json:"providerHealth"`
}

func Register(routes gin.IRouter, conversations *Conversations, assistant *Assistant,
	mode string, identity *auth.Service, metadata ...Capabilities) {
	caps := defaultCapabilities(assistant, mode)
	if len(metadata) > 0 {
		caps = metadata[0]
	}
	if assistant == nil || assistant.provider == nil || conversations == nil {
		caps.Ready = false
	}
	routes.GET("/ai/capabilities", func(c *gin.Context) {
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: caps})
	})
	routes.POST("/ai/conversations", auth.RequireLogin(identity), requireReady(caps.Ready), func(c *gin.Context) {
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
	routes.GET("/ai/conversations/:id", auth.RequireLogin(identity), requireReady(caps.Ready), func(c *gin.Context) {
		userID, ok := currentUser(c)
		if !ok {
			unauthorized(c)
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		messages, err := conversations.Get(ctx, userID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: messages})
	})
	routes.DELETE("/ai/conversations/:id", auth.RequireLogin(identity), requireReady(caps.Ready), func(c *gin.Context) {
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
	routes.POST("/ai/conversations/:id/messages", auth.RequireLogin(identity), requireReady(caps.Ready), func(c *gin.Context) {
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
		token := strings.TrimPrefix(c.GetHeader("authorization"), "Bearer ")
		ctx = context.WithValue(ctx, sessionGuardKey{}, func(checkCtx context.Context) bool {
			user, err := identity.Resolve(checkCtx, token)
			return err == nil && user != nil && user.ID == strconv.FormatUint(userID, 10)
		})
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
		if err = assistant.Ask(ctx, userID, c.Param("id"), shop, body.ClientMessageID, body.Message, emit); err != nil {
			// Headers are already sent; the only honest signal left is an error event.
			_ = emit("error", map[string]any{"code": codeOf(err), "message": messageOf(err)})
		}
	})
}

func defaultCapabilities(assistant *Assistant, mode string) Capabilities {
	caps := Capabilities{Mode: mode, Reason: "AI 未启用", ProviderHealth: "not_applicable"}
	if assistant == nil || assistant.provider == nil {
		return caps
	}
	caps.Enabled, caps.Ready = true, true
	caps.EmbeddingModel, caps.EmbeddingDimensions = assistant.provider.Model(), assistant.provider.Dimension()
	if chat, ok := assistant.provider.(interface{ ChatModel() string }); ok {
		caps.ChatModel = chat.ChatModel()
	} else if mode == "stub" {
		caps.ChatModel = assistant.provider.Model()
	}
	if mode == "stub" {
		caps.Reason = "当前为确定性 stub，仅验证协议，不代表真实模型质量"
	} else {
		caps.Reason = "真实 Provider 已配置；远端可用性在请求时确认"
		caps.ProviderHealth = "unchecked"
	}
	return caps
}

func requireReady(ready bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ready {
			writeError(c, ai.ErrNotConfigured)
		}
	}
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
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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
	case errors.Is(err, ErrBusy), errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrBudgetExhausted):
		status, message = http.StatusTooManyRequests, err.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
