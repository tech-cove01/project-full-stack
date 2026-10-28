package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// These diagnostics are paid and explicitly opt-in. The normal test suite only
// compiles them; cancellation settlement is unknown rather than a zero-cost claim.
func TestIntegrationRealProviderHTTPLifecycle(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" || os.Getenv("GO_REAL_AI") != "1" {
		t.Skip("set GO_INTEGRATION=1 and GO_REAL_AI=1 for paid HTTP lifecycle diagnostics")
	}
	if strings.TrimSpace(os.Getenv("AI_API_KEY")) == "" {
		t.Skip("real provider key is not configured; lifecycle diagnostics were not executed")
	}
	output := os.Getenv("GO_AI_LIFECYCLE_OUTPUT")
	if output == "" {
		t.Fatal("GO_AI_LIFECYCLE_OUTPUT is required to preserve failed and successful observations")
	}
	provider, err := ai.NewCompatible(ai.CompatibleConfig{
		BaseURL: os.Getenv("AI_BASE_URL"), APIKey: os.Getenv("AI_API_KEY"),
		ChatModel: os.Getenv("AI_CHAT_MODEL"), EmbeddingModel: os.Getenv("AI_EMBEDDING_MODEL"),
		Dimensions:      evalEnvInt(t, "AI_EMBEDDING_DIMENSIONS", 1536, 16, 4096),
		MaxOutputTokens: 300, Timeout: 35 * time.Second, DisableThinking: true,
	})
	if err != nil {
		t.Fatal("real provider configuration is invalid; raw diagnostics omitted")
	}
	t.Cleanup(provider.CloseIdleConnections)
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("explicit isolated integration configuration is invalid")
	}
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	conn, err := resources.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("lifecycle fixture lock unavailable")
	}
	var acquired int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK('go075_integration',30)").Scan(&acquired); err != nil || acquired != 1 {
		_ = conn.Close()
		t.Fatal("another GO-075 fixture is active")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "SELECT RELEASE_LOCK('go075_integration')")
		_ = conn.Close()
	})
	meter := &lifecycleProvider{Provider: provider}
	report := lifecycleReport{Task: "GO-075d", RealProvider: true, ChatModel: provider.ChatModel(),
		EmbeddingModel: provider.Model(), Dimensions: provider.Dimension(), MaxCalls: 10, MaxObservedTokens: 20000,
		StartedAt:            time.Now().UTC().Format(time.RFC3339),
		Cost:                 "unknown: canceled/timed-out requests may be billed; observed usage is not an invoice",
		CancellationBoundary: "client abort propagates through actual loopback HTTP SSE to compatible net/http; remote settlement unknown",
	}
	t.Cleanup(func() {
		report.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		report.Attempts, report.ObservedTokens, report.UnknownUsage, report.Observations = meter.snapshot()
		report.Passed = !t.Failed()
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil || os.MkdirAll(filepath.Dir(output), 0755) != nil || os.WriteFile(output, data, 0600) != nil {
			t.Error("could not persist safe lifecycle evidence")
		}
	})
	fixture := newProviderEvalFixture(t, resources)
	meter.arm("setup")
	fixture.publish(t, ctx, meter, providerEvalDocument{Key: "LIFE-P", Shop: 0,
		Title: "松风咖啡停车规则", Body: "松风咖啡停车规则：顾客可以在东侧停车场免费停车两小时，超过两小时每小时收费三元。"})
	knowledge := aiknowledge.NewService(resources.ORM, meter)
	conversations := NewConversations(resources.Redis)
	shop := fixture.shops[0]
	conversation, err := conversations.Create(ctx, fixture.user, &shop)
	if err != nil {
		t.Fatal("owned lifecycle conversation unavailable")
	}
	fixture.conversations = append(fixture.conversations, conversation.ID)
	assistant := NewAssistant(conversations, knowledge, aitool.NewService(resources.ORM), meter).
		WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis)).
		WithBudget(NewBudgetWithLimits(resources.Redis, 1, 1)).WithRetrieval(5, 0.5)
	identity := auth.NewService(resources.ORM, resources.Redis, "development")
	router := gin.New()
	auth.Register(router, identity)
	Register(router, conversations, assistant, meter.Mode(), identity)
	var activeMu sync.Mutex
	states := make(map[string]*lifecycleHTTPState)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeMu.Lock()
		state := states[r.Header.Get("X-GO075-Case")]
		activeMu.Unlock()
		if state == nil {
			router.ServeHTTP(w, r)
			return
		}
		defer close(state.done)
		router.ServeHTTP(&lifecycleCapture{ResponseWriter: w, state: state}, r)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 50 * time.Second
	var phone string
	if resources.ORM.WithContext(ctx).Raw("SELECT phone FROM tb_user WHERE id=?", fixture.user).Scan(&phone).Error != nil || phone == "" {
		t.Fatal("owned login fixture unavailable")
	}
	var tokens []string
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, token := range tokens {
			if identity.Logout(cleanup, token) != nil {
				t.Error("owned lifecycle session cleanup failed")
			}
		}
		keys, rate, daily := lifecycleBudgetCounters(cleanup, resources, fixture.user)
		if rate < 0 || daily < 0 {
			t.Error("owned lifecycle budget lookup failed during cleanup")
		}
		keys = append(keys, "login:code:"+phone, "login:code:throttle:"+phone)
		if len(keys) > 0 && resources.Redis.Del(cleanup, keys...).Err() != nil {
			t.Error("owned lifecycle budget/code cleanup failed")
		}
	})
	// Both valid sessions are prepared before revocation. Resetting only this
	// fixture phone's throttle is a setup action, not a product bypass assertion.
	first := lifecycleLogin(t, ctx, client, server.URL, phone)
	tokens = append(tokens, first)
	if resources.Redis.Del(ctx, "login:code:throttle:"+phone).Err() != nil {
		t.Fatal("owned second-login setup failed")
	}
	second := lifecycleLogin(t, ctx, client, server.URL, phone)
	tokens = append(tokens, second)
	report.RealHTTPLogin = true
	for _, scenario := range []string{"client_abort", "provider_deadline", "session_revoked", "recovery", "budget_reject"} {
		started := time.Now()
		result := lifecycleCaseResult{Case: scenario}
		control := meter.arm(scenario)
		state := &lifecycleHTTPState{done: make(chan struct{})}
		activeMu.Lock()
		states[scenario] = state
		activeMu.Unlock()
		messageID := "life-" + scenario
		fixture.dedupe = append(fixture.dedupe, dedupeKey(fixture.user, conversation.ID, messageID))
		token := first
		if scenario == "recovery" || scenario == "budget_reject" {
			token = second
		}
		beforeCalls, _, _, _ := meter.snapshot()
		requestCtx, cancel := context.WithCancel(ctx)
		body, _ := json.Marshal(map[string]string{"clientMessageId": messageID, "message": "松风咖啡停车免费多长时间？", "shopId": evalDecimal(shop)})
		request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, server.URL+"/ai/conversations/"+conversation.ID+"/messages", bytes.NewReader(body))
		if err != nil {
			cancel()
			t.Fatal("lifecycle HTTP request construction failed")
		}
		request.Header.Set("authorization", token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-GO075-Case", scenario)
		clientDone := make(chan error, 1)
		go func() {
			response, err := client.Do(request)
			if err == nil {
				defer response.Body.Close()
				_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
				if response.StatusCode != http.StatusOK && err == nil {
					err = errors.New("unexpected HTTP status")
				}
			}
			clientDone <- err
		}()
		if scenario == "client_abort" || scenario == "session_revoked" {
			select {
			case <-control.chatStarted:
				result.ChatEntered = true
			case <-state.done:
				cancel()
				report.Results = append(report.Results, result)
				t.Fatal("turn ended before reaching real Chat; lifecycle condition unmet")
			case <-time.After(30 * time.Second):
				cancel()
				report.Results = append(report.Results, result)
				t.Fatal("real Chat was not reached within the lifecycle budget")
			}
			select {
			case <-control.chatFinished:
				cancel()
				report.Results = append(report.Results, result)
				t.Fatal("provider completed before interruption; requested in-flight condition unmet")
			case <-time.After(100 * time.Millisecond):
			}
			if scenario == "client_abort" {
				cancel()
			} else {
				logout, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/user/logout", nil)
				if err != nil {
					cancel()
					t.Fatal("logout request construction failed")
				}
				logout.Header.Set("authorization", first)
				response, err := client.Do(logout)
				if err != nil {
					cancel()
					t.Fatal("session revocation HTTP request failed")
				}
				payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
				_ = response.Body.Close()
				var revoked struct {
					Success bool `json:"success"`
				}
				if response.StatusCode != http.StatusOK || readErr != nil || json.Unmarshal(payload, &revoked) != nil || !revoked.Success {
					cancel()
					t.Fatal("real session revocation did not complete")
				}
				result.SessionRevoked = true
			}
		}
		select {
		case clientErr := <-clientDone:
			result.ClientInterrupted = clientErr != nil
		case <-time.After(45 * time.Second):
			cancel()
			report.Results = append(report.Results, result)
			t.Fatal("HTTP request failed to terminate within budget")
		}
		select {
		case <-state.done:
		case <-time.After(5 * time.Second):
			cancel()
			report.Results = append(report.Results, result)
			t.Fatal("server did not finish handler and cleanup after interruption")
		}
		cancel()
		result.Events = state.events()
		result.TotalMS = time.Since(started).Milliseconds()
		afterCalls, _, _, observations := meter.snapshot()
		result.Attempts = afterCalls - beforeCalls
		for _, observation := range observations {
			if observation.Case == scenario && observation.Operation == "chat" {
				result.ChatEntered, result.ChatOutcome = true, observation.Outcome
				result.ChatUsageKnown = observation.Usage.Known
			}
		}
		var leaseCount, dedupeCount int64
		leaseCount, err = resources.Redis.Exists(ctx, lockKey(fixture.user, conversation.ID)).Result()
		if err == nil {
			dedupeCount, err = resources.Redis.Exists(ctx, dedupeKey(fixture.user, conversation.ID, messageID)).Result()
		}
		if err != nil {
			t.Fatal("lifecycle lease/dedupe verification failed")
		}
		result.LeaseReleased = leaseCount == 0
		result.DedupeAbsent = dedupeCount == 0
		_, result.RateCount, result.DailyCount = lifecycleBudgetCounters(ctx, resources, fixture.user)
		messages, err := conversations.History(ctx, fixture.user, conversation.ID)
		if err != nil {
			t.Fatal("lifecycle history verification failed")
		}
		for _, message := range messages {
			if message.Role == "assistant" {
				result.StoredAssistantMessages++
			}
		}
		if scenario == "recovery" {
			result.Passed = result.LeaseReleased && !result.DedupeAbsent && result.RateCount == 1 && result.DailyCount == 1 &&
				result.StoredAssistantMessages == 1 && evalHasEvents(result.Events, "answer", "done") && result.ChatOutcome == "success"
		} else if scenario == "budget_reject" {
			result.Passed = result.LeaseReleased && result.DedupeAbsent && result.RateCount == 1 && result.DailyCount == 1 &&
				result.StoredAssistantMessages == 1 && result.Attempts == 0 && evalHasEvents(result.Events, "error") && !lifecycleHasEvent(result.Events, "answer", "done")
		} else {
			result.Passed = result.LeaseReleased && result.DedupeAbsent && result.RateCount == 0 && result.DailyCount == 0 &&
				result.StoredAssistantMessages == 0 && !lifecycleHasEvent(result.Events, "answer", "done")
			switch scenario {
			case "client_abort":
				result.Passed = result.Passed && result.ClientInterrupted && result.ChatOutcome == "cancelled" && !result.ChatUsageKnown
			case "provider_deadline":
				result.Passed = result.Passed && result.ChatOutcome == "timeout" && !result.ChatUsageKnown && evalHasEvents(result.Events, "error")
			case "session_revoked":
				result.Passed = result.Passed && result.SessionRevoked && result.ChatOutcome == "success" && evalHasEvents(result.Events, "error")
			}
		}
		report.Results = append(report.Results, result)
		if !result.Passed {
			t.Fatal("lifecycle invariant failed; preserve the safe evidence before retrying")
		}
	}
	var ownStatus, otherStatus int
	if resources.ORM.WithContext(ctx).Raw("SELECT status FROM tb_voucher_order WHERE id=?", fixture.order).Scan(&ownStatus).Error != nil ||
		resources.ORM.WithContext(ctx).Raw("SELECT status FROM tb_voucher_order WHERE id=?", fixture.otherOrder).Scan(&otherStatus).Error != nil {
		t.Fatal("business invariant query failed")
	}
	report.BusinessInvariant = ownStatus == 1 && otherStatus == 1
	if !report.BusinessInvariant {
		t.Error("lifecycle diagnostics changed a business order")
	}
	actual, observed, _, _ := meter.snapshot()
	if actual > 10 || observed > 20000 {
		t.Error("lifecycle diagnostic exceeded the configured observation budget")
	}
}

type lifecycleReport struct {
	Task, ChatModel, EmbeddingModel, StartedAt, CompletedAt string
	Cost, CancellationBoundary                              string
	RealProvider, Passed, RealHTTPLogin, BusinessInvariant  bool
	Dimensions, MaxCalls, Attempts, UnknownUsage            int
	MaxObservedTokens, ObservedTokens                       int64
	Observations                                            []lifecycleObservation
	Results                                                 []lifecycleCaseResult
}
type lifecycleCaseResult struct {
	Case, ChatOutcome                   string
	Events                              []string
	ChatEntered, ChatUsageKnown         bool
	ClientInterrupted, SessionRevoked   bool
	LeaseReleased, DedupeAbsent, Passed bool
	StoredAssistantMessages, Attempts   int
	TotalMS, RateCount, DailyCount      int64
}
type lifecycleObservation struct {
	Case string
	ai.Observation
}
type lifecycleControl struct {
	name                      string
	chatStarted, chatFinished chan struct{}
}
type lifecycleProvider struct {
	ai.Provider
	mu                sync.Mutex
	control           *lifecycleControl
	calls             int
	tokens            int64
	unknown           int
	unexpectedUnknown bool
	observations      []lifecycleObservation
}

func (p *lifecycleProvider) arm(name string) *lifecycleControl {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.control = &lifecycleControl{name: name, chatStarted: make(chan struct{}), chatFinished: make(chan struct{})}
	return p.control
}
func (p *lifecycleProvider) before() (*lifecycleControl, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Unknown cancelled settlement has two explicitly reserved calls in this
	// diagnostic. It does not become zero usage or remove the total call cap.
	if p.calls >= 10 || p.tokens+4096 > 20000 || p.unexpectedUnknown {
		return nil, errors.New("lifecycle paid-call budget exhausted")
	}
	p.calls++
	return p.control, nil
}
func (p *lifecycleProvider) observe(control *lifecycleControl) func(ai.Observation) {
	return func(item ai.Observation) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.observations = append(p.observations, lifecycleObservation{Case: control.name, Observation: item})
		if item.Usage.Known {
			p.tokens += item.Usage.TotalTokens
		} else {
			p.unknown++
			if control.name != "client_abort" && control.name != "provider_deadline" {
				p.unexpectedUnknown = true
			}
		}
	}
}
func (p *lifecycleProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	control, err := p.before()
	if err != nil {
		return nil, err
	}
	return p.Provider.Embed(ai.WithObserver(ctx, p.observe(control)), texts)
}
func (p *lifecycleProvider) Chat(ctx context.Context, system, prompt string) (string, error) {
	control, err := p.before()
	if err != nil {
		return "", err
	}
	close(control.chatStarted)
	defer close(control.chatFinished)
	if control.name == "provider_deadline" {
		// This induces a real provider request deadline at the transport boundary,
		// not a simulated successful Chat response or the production HTTP limit.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
	}
	return p.Provider.Chat(ai.WithObserver(ctx, p.observe(control)), system, prompt)
}
func (p *lifecycleProvider) IndexModel() string { return ai.IndexModel(p.Provider) }
func (p *lifecycleProvider) ChatModel() string {
	return p.Provider.(interface{ ChatModel() string }).ChatModel()
}
func (p *lifecycleProvider) snapshot() (int, int64, int, []lifecycleObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.observations), p.tokens, p.unknown, append([]lifecycleObservation(nil), p.observations...)
}

type lifecycleHTTPState struct {
	done chan struct{}
	mu   sync.Mutex
	body bytes.Buffer
}
type lifecycleCapture struct {
	http.ResponseWriter
	state *lifecycleHTTPState
}

func (w *lifecycleCapture) Write(body []byte) (int, error) {
	w.state.mu.Lock()
	if w.state.body.Len()+len(body) <= 64<<10 {
		_, _ = w.state.body.Write(body)
	}
	w.state.mu.Unlock()
	return w.ResponseWriter.Write(body)
}
func (w *lifecycleCapture) Flush() { w.ResponseWriter.(http.Flusher).Flush() }
func (s *lifecycleHTTPState) events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []string
	for _, line := range strings.Split(s.body.String(), "\n") {
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
		}
	}
	return events
}
func lifecycleHasEvent(events []string, wants ...string) bool {
	for _, event := range events {
		for _, want := range wants {
			if event == want {
				return true
			}
		}
	}
	return false
}
func lifecycleLogin(t *testing.T, ctx context.Context, client *http.Client, base, phone string) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/user/code?phone="+phone, nil)
	if err != nil {
		t.Fatal("owned dev code request construction failed")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("owned dev code request failed")
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	var issued struct {
		Success bool                        `json:"success"`
		Data    struct{ Code, Mode string } `json:"data"`
	}
	if response.StatusCode != http.StatusOK || readErr != nil || json.Unmarshal(payload, &issued) != nil || !issued.Success || issued.Data.Mode != "development" {
		t.Fatal("real development code issuance failed")
	}
	body, _ := json.Marshal(map[string]string{"phone": phone, "code": issued.Data.Code})
	request, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/user/login", bytes.NewReader(body))
	if err != nil {
		t.Fatal("owned dev login request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal("owned dev login request failed")
	}
	payload, readErr = io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	var logged struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
	}
	if response.StatusCode != http.StatusOK || readErr != nil || json.Unmarshal(payload, &logged) != nil || !logged.Success || len(logged.Data) != 32 {
		t.Fatal("real development login failed")
	}
	return logged.Data
}

func lifecycleBudgetCounters(ctx context.Context, resources *database.Resources, user uint64) (keys []string, rate, daily int64) {
	seen := make(map[string]bool)
	for index, prefix := range []string{"ai:rate:", "ai:budget:"} {
		var cursor uint64
		for {
			page, next, err := resources.Redis.Scan(ctx, cursor, prefix+strconv.FormatUint(user, 10)+":*", 100).Result()
			if err != nil {
				return keys, -1, -1
			}
			for _, key := range page {
				if seen[key] {
					continue
				}
				seen[key] = true
				keys = append(keys, key)
				value, err := resources.Redis.Get(ctx, key).Int64()
				if err == redis.Nil {
					continue
				}
				if err != nil {
					return keys, -1, -1
				}
				if index == 0 {
					rate += value
				} else {
					daily += value
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return keys, rate, daily
}
