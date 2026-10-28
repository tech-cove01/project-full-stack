package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// Exercise the actual compatible HTTP adapter through the public SSE surface.
// The upstream is a controlled local server: this verifies transport cancellation
// and durable cleanup without credentials, a paid model, or a timing race.
func TestIntegrationReviewCompatibleHTTPAbortRetryAndReplay(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated MySQL and Redis required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	var reply, chats atomic.Int32
	entered, cancelled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/embeddings":
			var request struct {
				Input []string `json:"input"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Input) == 0 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data := make([]map[string]any, 0, len(request.Input))
			for i := range request.Input {
				data = append(data, map[string]any{"index": i, "embedding": []float32{1, 0}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/chat/completions":
			// Drain the request before waiting: an HTTP/1 server cannot observe
			// the peer closing while its request body still owns the read side.
			if _, err := io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20)); err != nil {
				return
			}
			chats.Add(1)
			if reply.Load() == 0 {
				close(entered)
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-ctx.Done():
					// Bound failed-fixture cleanup without turning it into success.
				}
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"停车可免费两小时。"}}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	provider, err := ai.NewCompatible(ai.CompatibleConfig{BaseURL: upstream.URL, APIKey: "local-review-fixture",
		ChatModel: "review-chat", EmbeddingModel: "review-vector", Dimensions: 2,
		MaxOutputTokens: 100, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(provider.CloseIdleConnections)
	fixture := newProviderEvalFixture(t, resources)
	fixture.publish(t, ctx, provider, providerEvalDocument{Key: "REVIEW-HTTP", Shop: 0,
		Title: "独立核验停车规则", Body: "顾客可免费停车两小时。"})
	conversations := NewConversations(resources.Redis)
	conversation, err := conversations.Create(ctx, fixture.user, &fixture.shops[0])
	if err != nil {
		t.Fatal(err)
	}
	fixture.conversations = append(fixture.conversations, conversation.ID)
	messageID := "review-http-abort"
	fixture.dedupe = append(fixture.dedupe, dedupeKey(fixture.user, conversation.ID, messageID))
	token := strings.Repeat(randomSuffix(t), 4)
	session := "login:token:" + token
	if err = resources.Redis.HSet(ctx, session, "id", evalDecimal(fixture.user), "nickName", "review", "icon", "").Err(); err != nil {
		t.Fatal(err)
	}
	if err = resources.Redis.Expire(ctx, session, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		keys, _, _ := lifecycleBudgetCounters(cleanup, resources, fixture.user)
		if err := resources.Redis.Del(cleanup, append(keys, session)...).Err(); err != nil {
			t.Error("owned session/budget cleanup failed")
		}
	})
	assistant := NewAssistant(conversations, aiknowledge.NewService(resources.ORM, provider),
		aitool.NewService(resources.ORM), provider).
		WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis)).
		WithBudget(NewBudgetWithLimits(resources.Redis, 1, 1))
	router := gin.New()
	Register(router, conversations, assistant, provider.Mode(), auth.NewService(resources.ORM, resources.Redis, "development"))
	handlerDone := make(chan struct{})
	var firstChatOutcome string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Review-Abort") == "1" {
			defer close(handlerDone)
			r = r.WithContext(ai.WithObserver(r.Context(), func(item ai.Observation) {
				if item.Operation == "chat" {
					firstChatOutcome = item.Outcome
				}
			}))
		}
		router.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	payload, _ := json.Marshal(map[string]string{"clientMessageId": messageID,
		"message": "停车免费多久？", "shopId": evalDecimal(fixture.shops[0])})
	request := func(requestCtx context.Context) *http.Request {
		r, err := http.NewRequestWithContext(requestCtx, http.MethodPost,
			server.URL+"/ai/conversations/"+conversation.ID+"/messages", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("authorization", token)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	abortCtx, abort := context.WithCancel(ctx)
	defer abort()
	first := request(abortCtx)
	first.Header.Set("X-Review-Abort", "1")
	response, err := client.Do(first)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owned SSE request was rejected before streaming: %d", response.StatusCode)
	}
	select {
	case <-entered:
	case <-handlerDone:
		t.Fatal("SSE handler ended before compatible Chat began")
	case <-ctx.Done():
		t.Fatal("request never reached the compatible Chat HTTP server")
	}
	if ctx.Err() != nil {
		t.Fatal("review deadline expired before client cancellation")
	}
	for _, signal := range []<-chan struct{}{cancelled, handlerDone} {
		select {
		case <-signal:
			t.Fatal("turn completed before the client could cancel it")
		default:
		}
	}
	abort()
	_ = response.Body.Close()
	for _, signal := range []<-chan struct{}{cancelled, handlerDone} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("client cancellation did not reach upstream and finish the handler")
		}
	}
	if firstChatOutcome != "cancelled" {
		t.Fatalf("expected client cancellation, got Chat outcome %q", firstChatOutcome)
	}
	remaining, err := resources.Redis.Exists(ctx, lockKey(fixture.user, conversation.ID),
		dedupeKey(fixture.user, conversation.ID, messageID)).Result()
	if err != nil || remaining != 0 {
		t.Fatal("cancelled turn retained a lease or dedupe claim")
	}
	_, rate, daily := lifecycleBudgetCounters(ctx, resources, fixture.user)
	if rate != 0 || daily != 0 {
		t.Fatal("cancelled logical turn retained its budget")
	}
	history, err := conversations.History(ctx, fixture.user, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if message.Role == "assistant" {
			t.Fatal("cancelled HTTP turn persisted an assistant answer")
		}
	}
	reply.Store(1)
	for i := 0; i < 2; i++ {
		response, err := client.Do(request(ctx))
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		text := string(body)
		if response.StatusCode != http.StatusOK || readErr != nil ||
			strings.Count(text, "event: answer\n") != 1 || strings.Count(text, "event: done\n") != 1 ||
			strings.Contains(text, "event: error") || !strings.Contains(text, "停车可免费两小时。") {
			t.Fatalf("retry/replay %d failed its public SSE contract", i)
		}
	}
	if chats.Load() != 2 {
		t.Fatal("replay called the upstream again or cancellation triggered an implicit retry")
	}
	_, rate, daily = lifecycleBudgetCounters(ctx, resources, fixture.user)
	if rate != 1 || daily != 1 {
		t.Fatal("retry and cached replay did not consume exactly one successful logical budget")
	}
	history, err = conversations.History(ctx, fixture.user, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	answers := 0
	for _, message := range history {
		if message.Role == "assistant" {
			answers++
		}
	}
	if answers != 1 {
		t.Fatal("retry and cached replay did not preserve exactly one assistant message")
	}
}
