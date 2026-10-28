package config

import (
	"strings"
	"testing"
	"time"
)

func TestAIIsDisabledByDefaultAndExplicitStubHasNoCredential(t *testing.T) {
	values := validEnvironment()
	cfg, err := fromMap(values)
	if err != nil || cfg.AI.Enabled || cfg.AI.Ready || cfg.AI.Reason != "AI 未启用" {
		t.Fatal("AI must be disabled by default without breaking core configuration")
	}
	values["AI_ENABLED"] = "true"
	cfg, err = fromMap(values)
	if err != nil || !cfg.AI.Enabled || !cfg.AI.Ready || cfg.AI.Provider != "stub" ||
		cfg.AI.Dimensions != 256 || cfg.AI.ChatModel != "stub-lexical" || cfg.AI.APIKey != "" {
		t.Fatal("an explicit stub must work without a paid credential")
	}
	values["AI_ENABLED"] = "false"
	values["AI_PROVIDER"] = "broken-provider"
	values["AI_MIN_SCORE"] = "NaN"
	cfg, err = fromMap(values)
	if err != nil || cfg.AI.Enabled || cfg.AI.Ready {
		t.Fatal("unused AI settings must not enable or break a disabled AI surface")
	}
}

func realAIEnvironment() map[string]string {
	values := validEnvironment()
	for key, value := range map[string]string{
		"AI_ENABLED": "true", "AI_PROVIDER": "openai-compatible",
		"AI_BASE_URL": "https://provider.example/v1", "AI_API_KEY": "synthetic-secret",
		"AI_CHAT_MODEL": "test-chat", "AI_EMBEDDING_MODEL": "test-embedding",
		"AI_EMBEDDING_DIMENSIONS": "1536", "AI_MAX_OUTPUT_TOKENS": "700",
		"AI_TOP_K": "3", "AI_MIN_SCORE": "0.5", "AI_CHAT_TIMEOUT_SECONDS": "30",
		"AI_DISABLE_THINKING": "true",
	} {
		values[key] = value
	}
	return values
}

func TestRealAIExplicitSettingsAndCoreIsolation(t *testing.T) {
	cfg, err := fromMap(realAIEnvironment())
	if err != nil || !cfg.AI.Enabled || !cfg.AI.Ready || cfg.AI.Provider != "openai-compatible" ||
		cfg.AI.ChatModel != "test-chat" || cfg.AI.EmbeddingModel != "test-embedding" ||
		cfg.AI.Dimensions != 1536 || cfg.AI.TopK != 3 || cfg.AI.MinScore != 0.5 ||
		cfg.AI.MaxOutputTokens != 700 || cfg.AI.Timeout != 30*time.Second || !cfg.AI.DisableThinking {
		t.Fatal("real provider settings must be explicit and applied to the AI surface")
	}
	for _, missing := range []string{"AI_API_KEY", "AI_BASE_URL", "AI_CHAT_MODEL", "AI_EMBEDDING_MODEL"} {
		t.Run(missing, func(t *testing.T) {
			values := realAIEnvironment()
			delete(values, missing)
			cfg, err := fromMap(values)
			if err != nil || !cfg.AI.Enabled || cfg.AI.Ready || cfg.MySQL.Port != 3308 || cfg.Redis.Port != 6380 {
				t.Fatal("an unconfigured provider must affect only AI, not core startup")
			}
			if strings.Contains(cfg.AI.Reason, "synthetic-secret") || strings.Contains(cfg.AI.Reason, "provider.example") {
				t.Fatal("capability reasons must not expose credentials or provider addresses")
			}
		})
	}
}

func TestInvalidAISettingsAreSafeAndDoNotFailCoreStartup(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"AI_ENABLED", "synthetic-secret"}, {"AI_PROVIDER", "synthetic-secret"},
		{"AI_EMBEDDING_DIMENSIONS", "0"}, {"AI_EMBEDDING_DIMENSIONS", "8193"},
		{"AI_MAX_OUTPUT_TOKENS", "0"}, {"AI_MAX_OUTPUT_TOKENS", "4097"},
		{"AI_TOP_K", "0"}, {"AI_TOP_K", "21"},
		{"AI_MIN_SCORE", "NaN"}, {"AI_MIN_SCORE", "+Inf"}, {"AI_MIN_SCORE", "-0.1"},
		{"AI_MIN_SCORE", "1.1"}, {"AI_MIN_SCORE", "synthetic-secret"},
		{"AI_CHAT_TIMEOUT_SECONDS", "0"}, {"AI_CHAT_TIMEOUT_SECONDS", "61"},
		{"AI_DISABLE_THINKING", "synthetic-secret"},
	} {
		t.Run(test.key+"_"+test.value, func(t *testing.T) {
			values := realAIEnvironment()
			values[test.key] = test.value
			cfg, err := fromMap(values)
			if err != nil || cfg.AI.Ready || !strings.Contains(cfg.AI.Reason, test.key) {
				t.Fatal("bad AI configuration must have a safe field diagnostic without stopping core startup")
			}
			if strings.Contains(cfg.AI.Reason, "synthetic-secret") {
				t.Fatal("configuration values must never enter diagnostics")
			}
		})
	}
}
