package app

import (
	"context"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aichat"
	"github.com/cityloop/fullstack-go/internal/platform/config"
)

// configuredAI performs no I/O. Provider failure and missing optional settings
// must not make login, browsing or trading unavailable.
func configuredAI(settings config.AI) (ai.Provider, aichat.Capabilities) {
	caps := aichat.Capabilities{Enabled: settings.Enabled, Mode: settings.Provider,
		Reason: settings.Reason, ProviderHealth: "not_applicable"}
	if caps.Mode == "" {
		caps.Mode = "disabled"
	}
	if !settings.Enabled || !settings.Ready {
		if caps.Reason == "" {
			caps.Reason = "AI 未启用或配置未就绪"
		}
		return unavailableProvider{}, caps
	}
	var provider ai.Provider
	switch settings.Provider {
	case "stub":
		provider = ai.NewStub(settings.Dimensions)
		caps.ChatModel = "stub-lexical"
	case "openai-compatible":
		compatible, err := ai.NewCompatible(ai.CompatibleConfig{
			BaseURL: settings.BaseURL, APIKey: settings.APIKey,
			ChatModel: settings.ChatModel, EmbeddingModel: settings.EmbeddingModel,
			Dimensions: settings.Dimensions, MaxOutputTokens: settings.MaxOutputTokens,
			Timeout: settings.Timeout, DisableThinking: settings.DisableThinking,
		})
		if err != nil {
			caps.Reason = "真实 AI Provider 配置无效，请检查后端配置"
			return unavailableProvider{}, caps
		}
		provider = compatible
		caps.ChatModel = compatible.ChatModel()
		caps.ProviderHealth = "unchecked"
	default:
		caps.Mode, caps.Reason = "unavailable", "AI Provider 未就绪"
		return unavailableProvider{}, caps
	}
	caps.Ready = true
	caps.EmbeddingModel, caps.EmbeddingDimensions = provider.Model(), provider.Dimension()
	return provider, caps
}

// A non-nil unavailable provider prevents NewService's test convenience fallback
// from silently turning a disabled deployment into the deterministic stub.
type unavailableProvider struct{}

func (unavailableProvider) Mode() string   { return "unavailable" }
func (unavailableProvider) Model() string  { return "" }
func (unavailableProvider) Dimension() int { return 0 }
func (unavailableProvider) Embed(context.Context, []string) ([][]float32, error) {
	return nil, ai.ErrNotConfigured
}
func (unavailableProvider) Chat(context.Context, string, string) (string, error) {
	return "", ai.ErrNotConfigured
}
