package config

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// AI is optional process configuration. Invalid AI settings make only the AI
// surface unavailable; they must not prevent login or trading from starting.
// Never log this struct: APIKey is a backend credential.
type AI struct {
	Enabled         bool
	Ready           bool
	Provider        string
	Reason          string
	BaseURL         string
	APIKey          string
	ChatModel       string
	EmbeddingModel  string
	Dimensions      int
	MaxOutputTokens int
	Timeout         time.Duration
	TopK            int
	MinScore        float64
	DisableThinking bool
}

func loadAI(lookup func(string) (string, bool)) AI {
	cfg := AI{Provider: "stub", Dimensions: 256, MaxOutputTokens: 600,
		Timeout: 60 * time.Second, TopK: 5, MinScore: 0.18,
		Reason: "AI 未启用"}
	if raw, ok := lookup("AI_ENABLED"); ok {
		switch strings.TrimSpace(raw) {
		case "true":
			cfg.Enabled = true
		case "false":
		default:
			cfg.Reason = "AI_ENABLED 必须为 true 或 false"
			return cfg
		}
	}
	if !cfg.Enabled {
		return cfg
	}
	if value, ok := lookup("AI_PROVIDER"); ok {
		cfg.Provider = strings.TrimSpace(value)
	}
	if cfg.Provider != "stub" && cfg.Provider != "openai-compatible" {
		cfg.Provider = "unavailable"
		cfg.Reason = "AI_PROVIDER 必须为 stub 或 openai-compatible"
		return cfg
	}
	if cfg.Provider == "openai-compatible" {
		cfg.Dimensions = 1024
	}
	for _, item := range []struct {
		name   string
		target *string
	}{
		{"AI_BASE_URL", &cfg.BaseURL}, {"AI_API_KEY", &cfg.APIKey},
		{"AI_CHAT_MODEL", &cfg.ChatModel}, {"AI_EMBEDDING_MODEL", &cfg.EmbeddingModel},
	} {
		if raw, ok := lookup(item.name); ok {
			*item.target = strings.TrimSpace(raw)
		}
	}
	for _, item := range []struct {
		name     string
		target   *int
		min, max int
	}{
		{"AI_EMBEDDING_DIMENSIONS", &cfg.Dimensions, 16, 8192},
		{"AI_MAX_OUTPUT_TOKENS", &cfg.MaxOutputTokens, 1, 4096},
		{"AI_TOP_K", &cfg.TopK, 1, 20},
	} {
		if raw, ok := lookup(item.name); ok {
			value, err := parseInt(item.name, raw, item.min, item.max)
			if err != nil {
				cfg.Reason = item.name + " 超出支持范围"
				return cfg
			}
			*item.target = value
		}
	}
	if raw, ok := lookup("AI_CHAT_TIMEOUT_SECONDS"); ok {
		seconds, err := parseInt("AI_CHAT_TIMEOUT_SECONDS", raw, 1, 60)
		if err != nil {
			cfg.Reason = "AI_CHAT_TIMEOUT_SECONDS 必须在 1 至 60 之间"
			return cfg
		}
		cfg.Timeout = time.Duration(seconds) * time.Second
	}
	if raw, ok := lookup("AI_MIN_SCORE"); ok {
		score, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			cfg.Reason = "AI_MIN_SCORE 必须在 0 至 1 之间"
			return cfg
		}
		cfg.MinScore = score
	}
	if raw, ok := lookup("AI_DISABLE_THINKING"); ok {
		switch strings.TrimSpace(raw) {
		case "true":
			cfg.DisableThinking = true
		case "false":
		default:
			cfg.Reason = "AI_DISABLE_THINKING 必须为 true 或 false"
			return cfg
		}
	}
	if cfg.Provider == "openai-compatible" &&
		(cfg.BaseURL == "" || cfg.APIKey == "" || cfg.ChatModel == "" || cfg.EmbeddingModel == "") {
		cfg.Reason = "真实 AI 需要配置地址、Key、Chat 与 Embedding 模型"
		return cfg
	}
	cfg.Ready = true
	if cfg.Provider == "stub" {
		cfg.ChatModel, cfg.EmbeddingModel = "stub-lexical", "stub-lexical"
		cfg.Reason = "当前为确定性 stub，仅验证协议，不代表真实模型质量"
	} else {
		cfg.Reason = "真实 Provider 已配置；远端可用性在请求时确认"
	}
	return cfg
}
