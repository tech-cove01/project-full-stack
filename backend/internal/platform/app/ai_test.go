package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/platform/config"
)

func TestDisabledAndUnconfiguredAIDoNotFallBackToStub(t *testing.T) {
	for _, settings := range []config.AI{
		{},
		{Enabled: true, Provider: "openai-compatible", Reason: "真实 AI 需要配置"},
		{Enabled: false, Ready: true, Provider: "stub", Dimensions: 256},
	} {
		provider, caps := configuredAI(settings)
		if caps.Ready || provider.Mode() == "stub" || caps.EmbeddingDimensions != 0 {
			t.Fatal("AI not ready must never become a stub or claim vector readiness")
		}
		if _, err := provider.Embed(context.Background(), []string{"公开规则"}); !errors.Is(err, ai.ErrNotConfigured) {
			t.Fatal("indexing must report unavailable when AI is disabled")
		}
		if _, err := provider.Chat(context.Background(), "test", "test"); !errors.Is(err, ai.ErrNotConfigured) {
			t.Fatal("Chat must report unavailable when AI is disabled")
		}
	}
}

func TestConfiguredAIReportsDifferentChatAndEmbeddingModelsWithoutCallingUpstream(t *testing.T) {
	settings := config.AI{Enabled: true, Ready: true, Provider: "openai-compatible",
		BaseURL: "https://provider.example/v1", APIKey: "synthetic-private-key",
		ChatModel: "test-chat", EmbeddingModel: "test-vector", Dimensions: 1536,
		MaxOutputTokens: 600, Timeout: time.Second, DisableThinking: true}
	provider, caps := configuredAI(settings)
	if !caps.Enabled || !caps.Ready || caps.ChatModel != "test-chat" || caps.EmbeddingModel != "test-vector" ||
		caps.EmbeddingDimensions != 1536 || caps.Mode != "openai-compatible" || caps.ProviderHealth != "unchecked" ||
		provider.Mode() != "openai-compatible" {
		t.Fatal("configuration readiness and distinct model names must be truthful without a network probe")
	}
	encoded, err := json.Marshal(caps)
	if err != nil || strings.Contains(string(encoded), "synthetic-private-key") || strings.Contains(string(encoded), "provider.example") {
		t.Fatal("public capabilities must not expose backend credentials or addresses")
	}
	settings.BaseURL = "https://synthetic-private-key@provider.example/v1"
	provider, caps = configuredAI(settings)
	if caps.Ready || !caps.Enabled || provider.Mode() == "stub" || strings.Contains(caps.Reason, "synthetic-private-key") {
		t.Fatal("constructor validation must only disable AI and return a safe capability diagnostic")
	}
}

func TestConfiguredStubReportsProtocolOnly(t *testing.T) {
	provider, caps := configuredAI(config.AI{Enabled: true, Ready: true, Provider: "stub",
		Dimensions: 256, Reason: "确定性 stub，仅验证协议"})
	if !caps.Ready || provider.Mode() != "stub" || caps.ChatModel != "stub-lexical" || caps.EmbeddingModel != "stub-lexical" ||
		caps.ProviderHealth != "not_applicable" || !strings.Contains(caps.Reason, "stub") {
		t.Fatal("stub readiness must describe protocol validation without real model claims")
	}
}
