package aichat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one FAQ document, one conversation and the exact keys it writes.
// It never touches another document, flushes Redis or resets a sequence.
func TestIntegrationAssistantTurn(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("set GO_INTEGRATION=1 with isolated MySQL and Redis")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid integration configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() {
		if resources.Close() != nil {
			t.Error("dependency cleanup failed")
		}
	})
	stub := ai.NewStub(256)
	knowledge := aiknowledge.NewService(resources.ORM, stub)
	conversations := NewConversations(resources.Redis)
	assistant := NewAssistant(conversations, knowledge, aitool.NewService(resources.ORM), stub).
		WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis))

	title := "集成测试规则 " + randomSuffix(t)
	body := "核销后不可退款，核销前可以联系门店处理退款。"
	documentID := insertPublished(t, resources, stub, title, body)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", documentID).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", documentID).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_document WHERE id=?", documentID).Error
	})

	shopID := uint64(1)
	conversation, err := conversations.Create(ctx, 9001, &shopID)
	if err != nil {
		t.Fatal("conversation create failed")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.Redis.Del(cleanupCtx,
			keyConversation(9001, conversation.ID), keyMessages(9001, conversation.ID)).Err()
	})

	var events []string
	var answerText string
	var cached bool
	emit := func(event string, data any) error {
		events = append(events, event)
		if values, ok := data.(map[string]any); ok {
			if event == "answer" {
				answerText, _ = values["text"].(string)
			}
			if event == "done" {
				cached, _ = values["cached"].(bool)
			}
		}
		return nil
	}
	if err = assistant.Ask(ctx, 9001, conversation.ID, &shopID, "client-1", "核销后可以退款吗", emit); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	joined := strings.Join(events, ",")
	for _, want := range []string{"meta", "status", "sources", "answer", "done"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %v", want, events)
		}
	}
	if !strings.Contains(answerText, "核销") {
		t.Fatalf("a grounded answer must quote the evidence, got %q", answerText)
	}
	if cached {
		t.Fatal("the first turn must not be a cached replay")
	}
	history, err := conversations.History(ctx, 9001, conversation.ID)
	if err != nil || len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("history must hold the user and assistant turns, got %v %v", history, err)
	}

	// Same client id must replay the stored answer without a second provider call.
	events = nil
	answerText = ""
	if err = assistant.Ask(ctx, 9001, conversation.ID, &shopID, "client-1", "核销后可以退款吗", emit); err != nil {
		t.Fatal("a completed id must replay instead of failing")
	}
	if !cached || !strings.Contains(answerText, "核销") {
		t.Fatalf("the replay must return the cached answer, got %v %q", events, answerText)
	}

	// A different id with the same text is a new turn, therefore not cached.
	if err = assistant.Ask(ctx, 9001, conversation.ID, &shopID, "client-2", "核销后可以退款吗", emit); err != nil {
		t.Fatal("a new message id must run normally")
	}
	if cached {
		t.Fatal("a new message id must not be served from the old cache")
	}

	if err = conversations.Delete(ctx, 9001, conversation.ID); err != nil {
		t.Fatal("conversation delete failed")
	}
	if err = assistant.Ask(ctx, 9001, conversation.ID, &shopID, "client-3", "核销后可以退款吗", emit); err != ErrNotFound {
		t.Fatalf("a deleted conversation must reject the turn, got %v", err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(entropy[:])
}

func insertPublished(t *testing.T, resources *database.Resources, stub *ai.Stub, title, body string) uint64 {
	t.Helper()
	ctx := context.Background()
	if err := resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_ai_document
		(scope,shop_id,title,body,status,published_version,epoch,disabled)
		VALUES ('SHOP_PUBLIC',1,?,?,'PUBLISHED',1,1,0)`, title, body).Error; err != nil {
		t.Fatal("document insert failed")
	}
	var id uint64
	// The id is read back by the business key: LAST_INSERT_ID() belongs to one
	// connection, and the pool may hand out a different one.
	if err := resources.ORM.WithContext(ctx).Raw("SELECT id FROM tb_ai_document WHERE title=? ORDER BY id DESC LIMIT 1", title).Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("document id unavailable")
	}
	vectors, err := stub.Embed(ctx, []string{body})
	if err != nil {
		t.Fatal("embedding fixture failed")
	}
	encoded, err := json.Marshal(vectors[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_ai_document_version
		(document_id,version,epoch,body,content_hash,status,embedding_model,embedding_dimension)
		VALUES (?,1,1,?,?, 'READY',?,?)`, id, body, strings.Repeat("0", 64),
		stub.Model(), stub.Dimension()).Error; err != nil {
		t.Fatal("version insert failed")
	}
	if err = resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_ai_chunk
		(document_id,version,epoch,ordinal,content,embedding,embedding_model,embedding_dimension)
		VALUES (?,1,1,0,?,?,?,?)`, id, body, string(encoded), stub.Model(), stub.Dimension()).Error; err != nil {
		t.Fatal("chunk insert failed")
	}
	return id
}
