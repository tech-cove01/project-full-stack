package aichat

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

type reviewProvider struct {
	*ai.Stub
	chat  func(context.Context, string, string) (string, error)
	calls int
}

func TestIntegrationReviewHTTPIdentityAndDTO(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated real dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	token := strings.Repeat(randomSuffix(t), 4)
	sessionKey := "login:token:" + token
	if err = resources.Redis.HSet(ctx, sessionKey, "id", "95140", "nickName", "review", "icon", "").Err(); err != nil {
		t.Fatal(err)
	}
	if err = resources.Redis.Expire(ctx, sessionKey, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Redis.Del(ctx, sessionKey).Err() })
	provider := &reviewProvider{Stub: ai.NewStub(256)}
	knowledge := aiknowledge.NewService(resources.ORM, provider)
	convs := NewConversations(resources.Redis)
	assistant := NewAssistant(convs, knowledge, aitool.NewService(resources.ORM), provider).WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis))
	router := gin.New()
	Register(router, convs, assistant, "stub", auth.NewService(resources.ORM, resources.Redis, "development"))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("authorization", token)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	caps := request("GET", "/ai/capabilities", "")
	var c struct {
		Data struct {
			Ready               bool
			Mode                string
			EmbeddingDimensions int
		}
	}
	if json.Unmarshal(caps.Body.Bytes(), &c) != nil || !c.Data.Ready || c.Data.Mode != "stub" || c.Data.EmbeddingDimensions != 256 {
		t.Fatal("Vue capabilities DTO unavailable")
	}
	created := request("POST", "/ai/conversations", `{"shopId":"1"}`)
	var view struct{ Data Conversation }
	if json.Unmarshal(created.Body.Bytes(), &view) != nil || view.Data.ID == "" || view.Data.Messages == nil {
		t.Fatal("create conversation DTO missing")
	}
	id := view.Data.ID
	t.Cleanup(func() {
		_ = resources.Redis.Del(ctx, keyConversation(95140, id), keyMessages(95140, id), lockKey(95140, id), dedupeKey(95140, id, "revoked")).Err()
	})
	got := request("GET", "/ai/conversations/"+id, "")
	var body struct{ Data Conversation }
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &body) != nil || body.Data.ID != id || body.Data.Messages == nil {
		t.Fatal("conversation GET must return object with messages")
	}
	doc := insertPublished(t, resources, provider.Stub, "review-http-"+randomSuffix(t), "停车规则：一小时免费。")
	t.Cleanup(func() {
		_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", doc).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", doc).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", doc).Error
	})
	provider.chat = func(context.Context, string, string) (string, error) {
		_ = resources.Redis.Del(ctx, sessionKey).Err()
		return "REVOKED_CANARY", nil
	}
	out := request("POST", "/ai/conversations/"+id+"/messages", `{"clientMessageId":"revoked","message":"停车规则","shopId":"1"}`)
	if strings.Contains(out.Body.String(), "event: answer") || strings.Contains(out.Body.String(), "REVOKED_CANARY") || !strings.Contains(out.Body.String(), "event: error") {
		t.Fatal("revoked session must stop answer and terminate with error")
	}
	history, _ := convs.History(ctx, 95140, id)
	for _, m := range history {
		if m.Role == "assistant" {
			t.Fatal("revoked session persisted assistant answer")
		}
	}
}

func (p *reviewProvider) Chat(ctx context.Context, system, prompt string) (string, error) {
	p.calls++
	if p.chat != nil {
		return p.chat(ctx, system, prompt)
	}
	return p.Stub.Chat(ctx, system, prompt)
}

func TestIntegrationReviewKnowledgeLifecycle(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated real dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resources, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	ctx := context.Background()
	shop := uint64(1)
	for _, stage := range []string{"before-provider", "during-provider", "cached-replay", "history"} {
		t.Run(stage, func(t *testing.T) {
			p := &reviewProvider{Stub: ai.NewStub(256)}
			doc := insertPublished(t, resources, p.Stub, "review-"+randomSuffix(t), "停车规则 canary-retired-rule：停车一小时免费。")
			t.Cleanup(func() {
				_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", doc).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", doc).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", doc).Error
			})
			convs := NewConversations(resources.Redis)
			conv, err := convs.Create(ctx, 95139, &shop)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = resources.Redis.Del(ctx, keyConversation(95139, conv.ID), keyMessages(95139, conv.ID), lockKey(95139, conv.ID), dedupeKey(95139, conv.ID, "first"), dedupeKey(95139, conv.ID, "second")).Err()
			})
			assistant := NewAssistant(convs, aiknowledge.NewService(resources.ORM, p), aitool.NewService(resources.ORM), p).WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis))
			disable := func() {
				if e := resources.ORM.Exec("UPDATE tb_ai_document SET disabled=1,status='DISABLED',epoch=epoch+1 WHERE id=?", doc).Error; e != nil {
					t.Fatal(e)
				}
			}
			answers := 0
			emit := func(event string, data any) error {
				if event == "answer" {
					answers++
				}
				if event == "sources" && stage == "before-provider" {
					disable()
				}
				return nil
			}
			if stage == "during-provider" {
				p.chat = func(context.Context, string, string) (string, error) { disable(); return "canary-retired-rule", nil }
			}
			_ = assistant.Ask(ctx, 95139, conv.ID, &shop, "first", "停车规则", emit)
			if stage == "before-provider" || stage == "during-provider" {
				if answers != 0 {
					t.Fatalf("invalidated rule emitted %d answers", answers)
				}
				if stage == "before-provider" && p.calls != 0 {
					t.Fatal("invalidated evidence reached Chat")
				}
				history, _ := convs.History(ctx, 95139, conv.ID)
				for _, m := range history {
					if m.Role == "assistant" {
						t.Fatal("invalidated answer persisted")
					}
				}
				return
			}
			if answers != 1 {
				t.Fatal("initial grounded turn failed")
			}
			disable()
			answers = 0
			if stage == "cached-replay" {
				_ = assistant.Ask(ctx, 95139, conv.ID, &shop, "first", "停车规则", emit)
				if answers != 0 {
					t.Fatal("retired cached answer replayed")
				}
				return
			}
			// The second current document gives the new turn valid evidence while the
			// old assistant answer must stay out of its model input.
			next := insertPublished(t, resources, p.Stub, "review-new-"+randomSuffix(t), "停车规则：仅停车十分钟免费。")
			t.Cleanup(func() {
				_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", next).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", next).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", next).Error
			})
			p.chat = func(_ context.Context, _ string, prompt string) (string, error) {
				if strings.Contains(prompt, "canary-retired-rule") {
					t.Error("retired assistant history reached provider")
				}
				return "新规则", nil
			}
			if err := assistant.Ask(ctx, 95139, conv.ID, &shop, "second", "停车规则现在是什么", emit); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReviewRejectsSensitiveInputBeforeStorage(t *testing.T) {
	a := NewAssistant(nil, nil, nil, nil)
	for _, message := range []string{"我的手机号13812345678，查停车规则", "验证码：123456，停车规则", "token=0123456789abcdef0123456789abcdef"} {
		func() {
			defer func() {
				if recover() != nil {
					t.Error("sensitive input touched storage")
				}
			}()
			err := a.Ask(context.Background(), 1, "c", nil, "m", message, func(string, any) error { return nil })
			if !errors.Is(err, ErrInput) {
				t.Errorf("sensitive input not rejected: %T", err)
			}
		}()
	}
}

func TestIntegrationReviewTurnFences(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated real dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resources, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	for _, stage := range []string{"lease", "conversation", "identity", "cancellation"} {
		t.Run(stage, func(t *testing.T) {
			provider := &reviewProvider{Stub: ai.NewStub(256)}
			knowledge := aiknowledge.NewService(resources.ORM, provider)
			convs := NewConversations(resources.Redis)
			shop := uint64(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conv, err := convs.Create(ctx, 95141, &shop)
			if err != nil {
				t.Fatal(err)
			}
			id := insertPublished(t, resources, provider.Stub, "review-fence-"+randomSuffix(t), "停车规则：停车一小时免费。")
			t.Cleanup(func() {
				bg := context.Background()
				_ = resources.Redis.Del(bg, keyConversation(95141, conv.ID), keyMessages(95141, conv.ID), lockKey(95141, conv.ID), dedupeKey(95141, conv.ID, "fence")).Err()
				_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", id).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", id).Error
				_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", id).Error
			})
			authorized := true
			ctx = context.WithValue(ctx, sessionGuardKey{}, func(context.Context) bool { return authorized })
			assistant := NewAssistant(convs, knowledge, aitool.NewService(resources.ORM), provider).WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis))
			provider.chat = func(context.Context, string, string) (string, error) {
				switch stage {
				case "lease":
					_ = resources.Redis.Del(context.Background(), lockKey(95141, conv.ID)).Err()
				case "conversation":
					_ = convs.Delete(context.Background(), 95141, conv.ID)
				case "identity":
					authorized = false
				case "cancellation":
					cancel()
				}
				return "FORBIDDEN_TERMINAL", nil
			}
			answers := 0
			err = assistant.Ask(ctx, 95141, conv.ID, &shop, "fence", "停车规则", func(event string, _ any) error {
				if event == "answer" || event == "done" {
					answers++
				}
				return nil
			})
			if err == nil || answers != 0 {
				t.Fatal("invalidated turn emitted terminal response")
			}
			history, _ := convs.History(context.Background(), 95141, conv.ID)
			for _, m := range history {
				if m.Role == "assistant" {
					t.Fatal("invalidated terminal persisted")
				}
			}
		})
	}
}

func TestIntegrationReviewBudgetReplayAndFailure(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated real dependencies required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	provider := &reviewProvider{Stub: ai.NewStub(256)}
	knowledge := aiknowledge.NewService(resources.ORM, provider)
	convs := NewConversations(resources.Redis)
	shop := uint64(1)
	for _, test := range []struct {
		name string
		user uint64
	}{{"cached_tool_retries_do_not_consume_budget", 95142}, {"failed_turn_refunds_logical_budget", 95143}} {
		t.Run(test.name, func(t *testing.T) {
			user := test.user
			conv, err := convs.Create(ctx, user, &shop)
			if err != nil {
				t.Fatal(err)
			}
			userText := strconv.FormatUint(user, 10)
			rate := "ai:rate:" + userText + ":" + time.Now().Format("200601021504")
			daily := "ai:budget:" + userText + ":" + time.Now().In(zoneForBudget()).Format("20060102")
			t.Cleanup(func() {
				_ = resources.Redis.Del(ctx, keyConversation(user, conv.ID), keyMessages(user, conv.ID), lockKey(user, conv.ID), dedupeKey(user, conv.ID, "one"), dedupeKey(user, conv.ID, "two"), rate, daily).Err()
			})
			assistant := NewAssistant(convs, knowledge, aitool.NewService(resources.ORM), provider).WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis)).WithBudget(NewBudgetWithLimits(resources.Redis, 10, 1))
			emit := func(string, any) error { return nil }
			if test.user == 95142 {
				if err = assistant.Ask(ctx, user, conv.ID, &shop, "one", "我的订单", emit); err != nil {
					t.Fatal(err)
				}
				if err = assistant.Ask(ctx, user, conv.ID, &shop, "one", "我的订单", emit); err != nil {
					t.Fatal("cached retry consumed a fresh budget", err)
				}
				if err = assistant.Ask(ctx, user, conv.ID, &shop, "two", "我的订单", emit); !errors.Is(err, ErrBudgetExhausted) {
					t.Fatal("new logical turn bypassed daily cap")
				}
			} else {
				doc := insertPublished(t, resources, provider.Stub, "review-budget-"+randomSuffix(t), "停车规则：停车一小时免费。")
				t.Cleanup(func() {
					_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", doc).Error
					_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", doc).Error
					_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", doc).Error
				})
				provider.chat = func(context.Context, string, string) (string, error) {
					return "", errors.New("synthetic provider unavailable")
				}
				if err = assistant.Ask(ctx, user, conv.ID, &shop, "one", "停车规则", emit); err == nil {
					t.Fatal("provider failure accepted")
				}
				provider.chat = nil
				if err = assistant.Ask(ctx, user, conv.ID, &shop, "one", "停车规则", emit); err != nil {
					t.Fatal("failure did not release logical budget and message claim", err)
				}
			}
		})
	}
}
