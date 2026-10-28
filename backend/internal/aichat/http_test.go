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

	"github.com/cityloop/fullstack-go/internal/ai"
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

func TestCapabilitiesDistinguishDisabledStubAndRealConfiguration(t *testing.T) {
	for _, test := range []struct {
		name                                string
		assistant                           *Assistant
		mode                                string
		metadata                            []Capabilities
		wantReady, wantEnabled              bool
		wantChat, wantEmbedding, wantHealth string
	}{
		{name: "disabled", mode: "disabled", wantHealth: "not_applicable"},
		{name: "stub", assistant: NewAssistant(nil, nil, nil, ai.NewStub(256)), mode: "stub",
			wantReady: true, wantEnabled: true, wantChat: "stub-lexical", wantEmbedding: "stub-lexical", wantHealth: "not_applicable"},
		{name: "unconfigured_real", mode: "openai-compatible",
			metadata:    []Capabilities{{Enabled: true, Mode: "openai-compatible", Reason: "真实 AI 配置未就绪", ProviderHealth: "not_applicable"}},
			wantEnabled: true, wantHealth: "not_applicable"},
		{name: "configured_real", assistant: NewAssistant(nil, nil, nil, ai.NewStub(256)), mode: "openai-compatible",
			metadata: []Capabilities{{Enabled: true, Ready: true, Mode: "openai-compatible", Reason: "远端可用性在请求时确认",
				ChatModel: "configured-chat", EmbeddingModel: "configured-vector", EmbeddingDimensions: 1536, ProviderHealth: "unchecked"}},
			wantEnabled: true, wantReady: true, wantChat: "configured-chat", wantEmbedding: "configured-vector", wantHealth: "unchecked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			Register(router, NewConversations(nil), test.assistant, test.mode, auth.NewService(nil, nil, "development"), test.metadata...)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ai/capabilities", nil))
			var body struct {
				Data Capabilities `json:"data"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil ||
				body.Data.Ready != test.wantReady || body.Data.Enabled != test.wantEnabled || body.Data.Mode != test.mode ||
				body.Data.ChatModel != test.wantChat || body.Data.EmbeddingModel != test.wantEmbedding || body.Data.ProviderHealth != test.wantHealth {
				t.Fatal("capabilities must distinguish enabled, locally ready and unprobed provider health")
			}
		})
	}
}

func TestNotReadyGuardStopsBeforeNilAssistantOrStorage(t *testing.T) {
	router := gin.New()
	called := false
	router.POST("/disabled", requireReady(false), func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/disabled", nil))
	if response.Code != http.StatusServiceUnavailable || called || !json.Valid(response.Body.Bytes()) {
		t.Fatal("a disabled AI request must terminate with 503 before storage or assistant access")
	}
}

func TestConfiguredRetrievalRemainsBounded(t *testing.T) {
	assistant := NewAssistant(nil, nil, nil, nil).WithRetrieval(3, 0.6)
	if assistant.topK != 3 || assistant.minScore != 0.6 {
		t.Fatal("configured retrieval parameters must replace lexical stub defaults")
	}
	assistant.WithRetrieval(0, -1).WithRetrieval(21, 2)
	if assistant.topK != 3 || assistant.minScore != 0.6 {
		t.Fatal("invalid deployment values must not widen retrieval bounds")
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
