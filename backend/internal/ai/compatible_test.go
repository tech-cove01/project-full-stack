package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func compatibleConfig(base string) CompatibleConfig {
	return CompatibleConfig{BaseURL: base, APIKey: "fixture-provider-key", ChatModel: "chat-fixture",
		EmbeddingModel: "embedding-fixture", Dimensions: 3, MaxOutputTokens: 120, Timeout: time.Second}
}

func fixtureProvider(t *testing.T, handler http.HandlerFunc) *Compatible {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewCompatible(compatibleConfig(server.URL + "/compatible/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.CloseIdleConnections)
	return provider
}

func writeChat(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"依据已发布规则回答。"}}],"usage":{"prompt_tokens":12,"completion_tokens":6,"total_tokens":18}}`)
}

func errorCode(err error) string {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError.Code
	}
	return ""
}

func TestCompatibleConfiguration(t *testing.T) {
	for _, raw := range []string{
		"https://provider.example/compatible-mode/v1/", "http://127.0.0.1:19230/v1", "http://[::1]:1234/v1", "http://localhost:1234",
	} {
		provider, err := NewCompatible(compatibleConfig(raw))
		if err != nil {
			t.Fatalf("valid URL rejected: %s", raw)
		}
		provider.CloseIdleConnections()
		if provider.Mode() != "openai-compatible" || provider.Model() != "embedding-fixture" ||
			provider.ChatModel() != "chat-fixture" || provider.Dimension() != 3 {
			t.Fatal("configured model metadata must be reported independently")
		}
	}
	for _, raw := range []string{
		"", "/v1", "http://provider.example/v1", "ftp://provider.example/v1", "https://user:credential@provider.example/v1",
		"https://provider.example/v1?token=secret", "https://provider.example/v1?", "https://provider.example/v1#secret",
		"https://provider.example:0/v1", "https://provider.example:65536/v1", "https://provider.example/v1/../admin",
		"https://provider.example/v1//", "https://provider.example/%2e%2e",
	} {
		_, err := NewCompatible(compatibleConfig(raw))
		if !errors.Is(err, ErrNotConfigured) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), raw) && raw != "" {
			t.Fatalf("unsafe URL must return sanitized configuration error: %q", raw)
		}
	}
	for name, edit := range map[string]func(*CompatibleConfig){
		"key blank":           func(c *CompatibleConfig) { c.APIKey = "" },
		"key newline":         func(c *CompatibleConfig) { c.APIKey = "secret\r\nAuthorization: secret" },
		"key long":            func(c *CompatibleConfig) { c.APIKey = strings.Repeat("x", 4097) },
		"chat blank":          func(c *CompatibleConfig) { c.ChatModel = "" },
		"embedding invalid":   func(c *CompatibleConfig) { c.EmbeddingModel = "has space" },
		"dimension zero":      func(c *CompatibleConfig) { c.Dimensions = 0 },
		"dimension excessive": func(c *CompatibleConfig) { c.Dimensions = 8193 },
		"output zero":         func(c *CompatibleConfig) { c.MaxOutputTokens = 0 },
		"output excessive":    func(c *CompatibleConfig) { c.MaxOutputTokens = 8193 },
		"timeout zero":        func(c *CompatibleConfig) { c.Timeout = 0 },
		"timeout excessive":   func(c *CompatibleConfig) { c.Timeout = 3 * time.Minute },
	} {
		t.Run(name, func(t *testing.T) {
			config := compatibleConfig("https://provider.example/v1")
			edit(&config)
			if _, err := NewCompatible(config); !errors.Is(err, ErrNotConfigured) {
				t.Fatal("invalid configuration must not construct provider")
			}
		})
	}
}

func TestCompatibleChatHTTPPayloadAndUsage(t *testing.T) {
	var calls int
	provider := fixtureProvider(t, func(w http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/compatible/v1/chat/completions" || request.Method != http.MethodPost ||
			request.Header.Get("Authorization") != "Bearer fixture-provider-key" || request.Header.Get("Content-Type") != "application/json" {
			t.Error("request must use configured path and backend-only authentication")
		}
		var payload struct {
			Model          string              `json:"model"`
			Stream         bool                `json:"stream"`
			MaxTokens      int                 `json:"max_tokens"`
			Messages       []map[string]string `json:"messages"`
			EnableThinking *bool               `json:"enable_thinking"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Model != "chat-fixture" || payload.Stream || payload.MaxTokens != 120 ||
			!reflect.DeepEqual(payload.Messages, []map[string]string{{"role": "system", "content": "仅根据证据回答"}, {"role": "user", "content": "【证据】预约提前一天"}}) {
			t.Error("chat payload differs from bounded text-only contract")
		}
		if calls == 1 && payload.EnableThinking != nil || calls == 2 && (payload.EnableThinking == nil || *payload.EnableThinking) {
			t.Error("provider-specific thinking option must be omitted unless explicitly disabled")
		}
		writeChat(w)
	})
	var observations []Observation
	ctx := WithObserver(context.Background(), func(observation Observation) { observations = append(observations, observation) })
	for i := 0; i < 2; i++ {
		provider.config.DisableThinking = i == 1
		answer, err := provider.Chat(ctx, "仅根据证据回答", "【证据】预约提前一天")
		if err != nil || answer != "依据已发布规则回答。" {
			t.Fatalf("valid chat failed: %v", err)
		}
	}
	if calls != 2 || len(observations) != 2 {
		t.Fatal("exactly one observation per actual HTTP call")
	}
	for _, observation := range observations {
		if observation.Operation != "chat" || observation.Model != "chat-fixture" || observation.Outcome != "success" || observation.Duration < 0 ||
			observation.Usage != (Usage{InputTokens: 12, OutputTokens: 6, TotalTokens: 18, Known: true}) {
			t.Fatalf("accounting metadata mismatch: %+v", observation)
		}
	}
}

func TestCompatibleEmbeddingsReorderBatchesAndUsage(t *testing.T) {
	var sizes []int
	provider := fixtureProvider(t, func(w http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model      string   `json:"model"`
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
			Encoding   string   `json:"encoding_format"`
		}
		if request.URL.Path != "/compatible/v1/embeddings" || json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Model != "embedding-fixture" || payload.Dimensions != 3 || payload.Encoding != "float" {
			t.Error("embedding request must carry explicit model, dimensions, input and float encoding")
		}
		sizes = append(sizes, len(payload.Input))
		rows := make([]map[string]any, 0, len(payload.Input))
		for i := len(payload.Input) - 1; i >= 0; i-- {
			var value int
			_, _ = fmt.Sscanf(payload.Input[i], "text-%d", &value)
			rows = append(rows, map[string]any{"index": i, "embedding": []float32{float32(value + 1), 2, 3}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "usage": map[string]int{"prompt_tokens": len(rows), "total_tokens": len(rows)}})
	})
	texts := make([]string, 23)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	var observations []Observation
	ctx := WithObserver(context.Background(), func(item Observation) { observations = append(observations, item) })
	vectors, err := provider.Embed(ctx, texts)
	if err != nil || !reflect.DeepEqual(sizes, []int{10, 10, 3}) || len(vectors) != len(texts) || len(observations) != 3 {
		t.Fatalf("batch contract failed: %v, sizes %v, observations %d", err, sizes, len(observations))
	}
	var tokens int64
	for i, vector := range vectors {
		if !reflect.DeepEqual(vector, []float32{float32(i + 1), 2, 3}) {
			t.Fatal("provider response index must determine original input order")
		}
	}
	for _, item := range observations {
		if item.Operation != "embedding" || item.Model != "embedding-fixture" || !item.Usage.Known || item.Outcome != "success" {
			t.Fatal("embedding usage must be attributed per batch")
		}
		tokens += item.Usage.TotalTokens
	}
	if tokens != 23 {
		t.Fatal("usage must not double count batches")
	}
}

func TestCompatibleRejectsBadEmbeddingResponses(t *testing.T) {
	for name, body := range map[string]string{
		"missing row":      `{"data":[]}`,
		"missing index":    `{"data":[{"embedding":[1,2,3]}]}`,
		"negative index":   `{"data":[{"index":-1,"embedding":[1,2,3]}]}`,
		"out of range":     `{"data":[{"index":1,"embedding":[1,2,3]}]}`,
		"wrong dimensions": `{"data":[{"index":0,"embedding":[1,2]}]}`,
		"zero vector":      `{"data":[{"index":0,"embedding":[0,0,0]}]}`,
		"null vector":      `{"data":[{"index":0,"embedding":null}]}`,
		"null component":   `{"data":[{"index":0,"embedding":[1,null,3]}]}`,
		"float32 overflow": `{"data":[{"index":0,"embedding":[1e60,2,3]}]}`,
		"infinite":         `{"data":[{"index":0,"embedding":[NaN,2,3]}]}`,
		"trailing json":    `{"data":[{"index":0,"embedding":[1,2,3]}]} {}`,
		"null response":    `null`,
	} {
		t.Run(name, func(t *testing.T) {
			provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			if vectors, err := provider.Embed(context.Background(), []string{"规则"}); errorCode(err) != "invalid_response" || vectors != nil {
				t.Fatalf("invalid vectors must not escape: %v", err)
			}
		})
	}
	t.Run("valid numeric zero", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0,-3]}]}`)
		})
		vectors, err := provider.Embed(context.Background(), []string{"规则"})
		if err != nil || !reflect.DeepEqual(vectors, [][]float32{{1, 0, -3}}) {
			t.Fatal("a numeric zero component is valid in a nonzero vector")
		}
	})
	t.Run("duplicate index", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,2,3]},{"index":0,"embedding":[2,3,4]}]}`)
		})
		if _, err := provider.Embed(context.Background(), []string{"first", "second"}); errorCode(err) != "invalid_response" {
			t.Fatal("duplicate input index cannot replace another vector")
		}
	})
	t.Run("later batch fails", func(t *testing.T) {
		calls := 0
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				rows := make([]map[string]any, 10)
				for i := range rows {
					rows[i] = map[string]any{"index": i, "embedding": []int{1, 2, 3}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
			} else {
				w.WriteHeader(http.StatusTooManyRequests)
			}
		})
		texts := make([]string, 11)
		for i := range texts {
			texts[i] = "公开规则"
		}
		if vectors, err := provider.Embed(context.Background(), texts); errorCode(err) != "rate_limited" || vectors != nil || calls != 2 {
			t.Fatal("partial batch must not escape or be retried")
		}
	})
}

func TestCompatibleRejectsIncompleteChatAndReportsUsageUnknown(t *testing.T) {
	for name, body := range map[string]string{
		"truncated answer": `{"choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":"部分答案"}}]}`,
		"filtered":         `{"choices":[{"index":0,"finish_reason":"content_filter","message":{"role":"assistant","content":"隐藏"}}]}`,
		"tool finish":      `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"已退款"}}]}`,
		"wrong role":       `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"user","content":"text"}}]}`,
		"missing index":    `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text"}}]}`,
		"missing finish":   `{"choices":[{"index":0,"message":{"role":"assistant","content":"text"}}]}`,
		"tool payload":     `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"已退款","tool_calls":[{}]}}]}`,
		"function payload": `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"已退款","function_call":{}}}]}`,
		"refusal":          `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"text","refusal":"blocked"}}]}`,
		"blank":            `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":" "}}]}`,
		"multimodal":       `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":[{"text":"text"}]}}]}`,
		"empty":            `{"choices":[]}`,
		"malformed":        `{"choices":`,
		"trailing":         `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			var observations []Observation
			ctx := WithObserver(context.Background(), func(item Observation) { observations = append(observations, item) })
			answer, err := provider.Chat(ctx, "依据规则", "预约怎么操作")
			if errorCode(err) != "invalid_response" || answer != "" || len(observations) != 1 || observations[0].Usage.Known || observations[0].Outcome != "invalid_response" {
				t.Fatal("invalid response must fail without a partial answer")
			}
		})
	}
	for name, raw := range map[string]string{"missing": "{}", "negative": `{"prompt_tokens":-1,"completion_tokens":2,"total_tokens":1}`, "mismatch": `{"prompt_tokens":1,"completion_tokens":2,"total_tokens":4}`, "partial": `{"total_tokens":10}`} {
		t.Run("usage "+name, func(t *testing.T) {
			var wire wireUsage
			if json.Unmarshal([]byte(raw), &wire) != nil || wire.read(false).Known {
				t.Fatal("unverifiable usage must be unknown rather than zero cost")
			}
		})
	}
}

func TestCompatibleInputBoundsAvoidHTTPAndObservation(t *testing.T) {
	var calls atomic.Int32
	provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeChat(w) })
	var observations []Observation
	ctx := WithObserver(context.Background(), func(item Observation) { observations = append(observations, item) })
	for _, pair := range [][2]string{{"", "question"}, {"system", " "}, {"system", strings.Repeat("x", maxPromptBytes)}, {"system", string([]byte{0xff})}} {
		if _, err := provider.Chat(ctx, pair[0], pair[1]); !errors.Is(err, ErrBadInput) {
			t.Fatal("invalid prompts must be refused before provider")
		}
	}
	for _, texts := range [][]string{nil, {""}, {strings.Repeat("x", maxEmbeddingText+1)}, make([]string, maxEmbeddingInputs+1), {string([]byte{0xff})}} {
		if _, err := provider.Embed(ctx, texts); !errors.Is(err, ErrBadInput) {
			t.Fatal("invalid embedding input must be refused before provider")
		}
	}
	if calls.Load() != 0 || len(observations) != 0 {
		t.Fatal("locally rejected input is not a paid HTTP attempt")
	}
}

func TestCompatibleHTTPFailuresAreSanitizedAndNotRetried(t *testing.T) {
	for _, status := range []int{301, 307, 400, 401, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Location", "/secret")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":"fixture-provider-key private prompt https://secret"}`)
			})
			var observed Observation
			_, err := provider.Chat(WithObserver(context.Background(), func(item Observation) { observed = item }), "system", "question")
			var providerError *ProviderError
			if !errors.As(err, &providerError) || providerError.Status != status || calls != 1 || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "fixture-provider-key") || observed.Usage.Known {
				t.Fatal("HTTP failures must be fixed-code errors, unknown cost and no retries/redirects")
			}
			if status == 429 && providerError.Code != "rate_limited" {
				t.Fatal("rate limit must be recognizable")
			}
		})
	}
	t.Run("oversize declared", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprint(maxChatResponse+1))
			w.WriteHeader(200)
		})
		if _, err := provider.Chat(context.Background(), "system", "question"); errorCode(err) != "response_too_large" {
			t.Fatal("declared oversize must be rejected")
		}
	})
	t.Run("oversize chunked", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, strings.Repeat("x", maxChatResponse+1))
		})
		if _, err := provider.Chat(context.Background(), "system", "question"); errorCode(err) != "response_too_large" {
			t.Fatal("chunked oversize must be rejected")
		}
	})
	t.Run("oversize answer", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": strings.Repeat("x", maxAnswerBytes+1)}}}})
		})
		if _, err := provider.Chat(context.Background(), "system", "question"); errorCode(err) != "invalid_response" {
			t.Fatal("oversize content must not reach application")
		}
	})
	t.Run("invalid utf8", func(t *testing.T) {
		provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte{0xff}) })
		if _, err := provider.Chat(context.Background(), "system", "question"); errorCode(err) != "invalid_response" {
			t.Fatal("invalid utf8 must not be silently repaired")
		}
	})
}

func TestCompatibleCancellationDeadlineAndRecovery(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "provider timeout"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			ended := make(chan struct{}, 1)
			var fail atomic.Bool
			fail.Store(true)
			provider := fixtureProvider(t, func(w http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				if fail.Load() {
					started <- struct{}{}
					<-request.Context().Done()
					ended <- struct{}{}
					return
				}
				writeChat(w)
			})
			provider.config.Timeout = 150 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
				defer cancel()
			}
			var observations []Observation
			ctx = WithObserver(ctx, func(item Observation) { observations = append(observations, item) })
			done := make(chan error, 1)
			go func() { _, err := provider.Chat(ctx, "system", "question"); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("provider request never began")
			}
			if mode == "cancel" {
				cancel()
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("request failed to stop promptly")
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || len(observations) != 1 || observations[0].Usage.Known {
				t.Fatalf("wrong termination/accounting: %v", err)
			}
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("upstream request context was not cancelled")
			}
			fail.Store(false)
			if answer, err := provider.Chat(context.Background(), "system", "question"); err != nil || answer == "" {
				t.Fatal("capacity must recover after cancellation")
			}
		})
	}
}

func TestCompatibleIndexModelBindsVectorSpaceOnly(t *testing.T) {
	config := compatibleConfig("https://PROVIDER.example:443/compatible-mode/v1/")
	provider, err := NewCompatible(config)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.CloseIdleConnections()
	identity := IndexModel(provider)
	if len(identity) > 64 || identity == provider.Model() || IndexModel(NewStub(256)) != "stub-lexical" {
		t.Fatal("persisted vector identity must fit schema and preserve legacy providers")
	}
	for _, edit := range []func(*CompatibleConfig){
		func(c *CompatibleConfig) { c.APIKey = "rotated-key" },
		func(c *CompatibleConfig) { c.ChatModel = "another-chat-model" },
		func(c *CompatibleConfig) { c.MaxOutputTokens = 200; c.Timeout = time.Minute; c.DisableThinking = true },
		func(c *CompatibleConfig) { c.BaseURL = "https://provider.example/compatible-mode/v1" },
	} {
		changed := config
		edit(&changed)
		candidate, err := NewCompatible(changed)
		if err != nil {
			t.Fatal(err)
		}
		candidate.CloseIdleConnections()
		if IndexModel(candidate) != identity {
			t.Fatal("credential rotation, chat changes and equivalent URLs must preserve persisted vector space")
		}
	}
	for _, edit := range []func(*CompatibleConfig){
		func(c *CompatibleConfig) { c.BaseURL = "https://other.example/compatible-mode/v1" },
		func(c *CompatibleConfig) { c.BaseURL = "https://provider.example/another-endpoint/v1" },
		func(c *CompatibleConfig) { c.EmbeddingModel = "another-embedding-model" },
		func(c *CompatibleConfig) { c.Dimensions = 4 },
	} {
		changed := config
		edit(&changed)
		candidate, err := NewCompatible(changed)
		if err != nil {
			t.Fatal(err)
		}
		candidate.CloseIdleConnections()
		if IndexModel(candidate) == identity {
			t.Fatal("endpoint/model/dimension switches must isolate vectors")
		}
	}
}

func TestCompatibleConcurrencyAdmissionIsBoundedAndCancelable(t *testing.T) {
	started := make(chan struct{}, providerConcurrent)
	release := make(chan struct{})
	var calls atomic.Int32
	provider := fixtureProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		writeChat(w)
	})
	var wg sync.WaitGroup
	for range providerConcurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := provider.Chat(context.Background(), "system", "question"); err != nil {
				t.Error("admitted call failed")
			}
		}()
	}
	for range providerConcurrent {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("admitted call did not reach upstream")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	observations := 0
	_, err := provider.Chat(WithObserver(ctx, func(Observation) { observations++ }), "system", "question")
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != providerConcurrent || observations != 0 {
		t.Fatal("queued cancellation must neither call provider nor consume a paid-attempt observation")
	}
	close(release)
	wg.Wait()
}
