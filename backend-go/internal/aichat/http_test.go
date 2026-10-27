package aichat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func TestChatRoutesRequireLoginButCapabilitiesStaysPublic(t *testing.T) {
	identity := auth.NewService(nil, nil, "development")
	router := httpapi.NewRouter(nil, time.Second, "aichat-test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), func(routes gin.IRouter) {
			Register(routes, NewConversations(nil), nil, "stub", identity)
		})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/ai/conversations/abc", http.StatusUnauthorized},
		{http.MethodDelete, "/ai/conversations/abc", http.StatusUnauthorized},
		{http.MethodPost, "/ai/conversations/abc/messages", http.StatusUnauthorized},
		{http.MethodPost, "/ai/conversations", http.StatusUnauthorized},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("userId", "1")
		router.ServeHTTP(response, request)
		var body httpapi.Result
		if response.Code != test.status || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Success {
			t.Fatalf("%s %s must reject without a session, got %d", test.method, test.path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ai/capabilities", nil))
	var capabilities struct {
		Success bool `json:"success"`
		Data    struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &capabilities) != nil ||
		!capabilities.Success || capabilities.Data.Mode != "stub" {
		t.Fatal("capabilities must stay public and report the stub provider")
	}
}

func TestAskRejectsBlankMessageBeforeTouchingStorage(t *testing.T) {
	assistant := NewAssistant(nil, nil, nil, nil)
	err := assistant.Ask(context.Background(), 1, "conversation", nil, "client-1", "   ",
		func(string, any) error { return nil })
	if !errors.Is(err, ErrInput) {
		t.Fatal("a blank message must fail before Redis or the provider")
	}
	long := ""
	for i := 0; i < maxMessageRunes+1; i++ {
		long += "字"
	}
	err = assistant.Ask(context.Background(), 1, "conversation", nil, "client-1", long,
		func(string, any) error { return nil })
	if !errors.Is(err, ErrInput) {
		t.Fatal("an oversized message must be rejected instead of truncated")
	}
}

func TestCodeOfMapsOnlyKnownErrors(t *testing.T) {
	if codeOf(ErrInput) != "INVALID_INPUT" || codeOf(ErrNotFound) != "CONVERSATION_NOT_FOUND" ||
		codeOf(errors.New("boom")) != "AI_UNAVAILABLE" {
		t.Fatal("stream error codes must stay stable and not leak internal text")
	}
	if messageOf(errors.New("boom")) != "AI 服务暂时不可用，请稍后重试。" {
		t.Fatal("an unknown error must not echo its own text to the client")
	}
}
