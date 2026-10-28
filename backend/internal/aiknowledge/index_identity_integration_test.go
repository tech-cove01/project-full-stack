package aiknowledge

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/gin-gonic/gin"
)

// A visible published document with unusable vectors must be an index failure,
// not a successful empty search (which would become a misleading refusal).
func TestIntegrationIndexCompatibility(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated MySQL/Redis required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid test configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = r.Close() })
	user := insertUser(t, r)
	merchant := insertMerchant(t, r)
	shop := insertShop(t, r, merchant)
	insertMember(t, r, merchant, user)
	p := &observedProvider{Stub: ai.NewStub(256)}
	s := NewService(r.ORM, p)
	draft, err := s.CreateDraft(ctx, decimal(user), decimal(shop), "GO075向量兼容测试", "门店停车两小时免费，预约需要提前一天。\n\n"+strings.Repeat("甲", 400)+"\n\n"+strings.Repeat("乙", 400))
	if err != nil {
		t.Fatal("create owned draft failed")
	}
	doc, _ := positive(draft.ID)
	t.Cleanup(func() {
		for _, statement := range []string{
			"DELETE FROM tb_ai_chunk WHERE document_id=?", "DELETE FROM tb_ai_document_version WHERE document_id=?", "DELETE FROM tb_ai_document WHERE id=?",
		} {
			if r.ORM.Exec(statement, doc).Error != nil {
				t.Error("owned document cleanup failed")
			}
		}
		for _, item := range []struct {
			sql string
			id  uint64
		}{
			{"DELETE FROM tb_audit_log WHERE merchant_id=?", merchant},
			{"DELETE FROM tb_merchant_member WHERE merchant_id=?", merchant},
			{"DELETE FROM tb_shop WHERE id=?", shop}, {"DELETE FROM tb_merchant WHERE id=?", merchant}, {"DELETE FROM tb_user WHERE id=?", user},
		} {
			if r.ORM.Exec(item.sql, item.id).Error != nil {
				t.Error("owned fixture cleanup failed")
			}
		}
	})
	if _, err = s.Publish(ctx, decimal(user), draft.ID); err != nil {
		t.Fatal("publish owned document failed")
	}
	var original struct {
		ID        uint64
		Embedding string
	}
	if r.ORM.Raw("SELECT id,embedding FROM tb_ai_chunk WHERE document_id=? ORDER BY ordinal LIMIT 1", doc).Scan(&original).Error != nil || original.ID == 0 {
		t.Fatal("owned chunk missing")
	}
	var chunkCount int64
	if r.ORM.Raw("SELECT COUNT(*) FROM tb_ai_chunk WHERE document_id=?", doc).Scan(&chunkCount).Error != nil || chunkCount < 2 {
		t.Fatal("multi-chunk fixture required")
	}
	t.Run("changed_model_is_not_empty_success", func(t *testing.T) {
		if r.ORM.Exec("UPDATE tb_ai_chunk SET embedding_model='different-model' WHERE id=?", original.ID).Error != nil {
			t.Fatal("fixture mutation failed")
		}
		defer r.ORM.Exec("UPDATE tb_ai_chunk SET embedding_model=? WHERE id=?", p.Model(), original.ID)
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatalf("incompatible index must reject before paid query: rejected=%v calls=%d", errors.Is(err, ErrUnsafe), p.calls)
		}
	})
	t.Run("wrong_dimension_is_not_partial_success", func(t *testing.T) {
		r.ORM.Exec("UPDATE tb_ai_chunk SET embedding_dimension=128 WHERE id=?", original.ID)
		defer r.ORM.Exec("UPDATE tb_ai_chunk SET embedding_dimension=256 WHERE id=?", original.ID)
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatal("wrong dimension was silently accepted")
		}
	})
	t.Run("invalid_vector_requires_rebuild", func(t *testing.T) {
		r.ORM.Exec("UPDATE tb_ai_chunk SET embedding='[0,0]' WHERE id=?", original.ID)
		defer r.ORM.Exec("UPDATE tb_ai_chunk SET embedding=? WHERE id=?", original.Embedding, original.ID)
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatal("invalid stored vector was silently skipped")
		}
	})
	t.Run("version_metadata_must_match_chunks", func(t *testing.T) {
		r.ORM.Exec("UPDATE tb_ai_document_version SET embedding_model='other-provider' WHERE document_id=?", doc)
		defer r.ORM.Exec("UPDATE tb_ai_document_version SET embedding_model=? WHERE document_id=?", p.Model(), doc)
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatal("mismatched version model was ignored")
		}
	})
	t.Run("valid_index_queries_once_and_other_shop_is_excluded", func(t *testing.T) {
		p.calls = 0
		hits, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if err != nil || p.calls != 1 || len(hits) < 1 || hits[0].DocumentID != doc {
			t.Fatal("valid index failed")
		}
		foreign := shop + 1000000
		hits, err = s.Search(ctx, "停车规则", &foreign, 5, 0)
		if err != nil || len(hits) != 0 {
			t.Fatal("other shop widened retrieval scope")
		}
	})
	t.Run("anonymous_search_rejects_disable_during_embedding", func(t *testing.T) {
		p.before = func() {
			if r.ORM.Exec("UPDATE tb_ai_document SET disabled=1,status='DISABLED',epoch=epoch+1 WHERE id=?", doc).Error != nil {
				t.Fatal("controlled revocation failed")
			}
		}
		defer func() {
			p.before = nil
			r.ORM.Exec("UPDATE tb_ai_document SET disabled=0,status='PUBLISHED',epoch=epoch-1 WHERE id=?", doc)
		}()
		gin.SetMode(gin.TestMode)
		router := gin.New()
		Register(router, s, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/ai/search?q=parking&shopId="+decimal(shop), nil))
		if response.Code != 503 || strings.Contains(response.Body.String(), "GO075向量兼容测试") {
			t.Fatal("revoked rules escaped anonymous search after paid boundary")
		}
	})
	t.Run("one_missing_chunk_is_unready", func(t *testing.T) {
		if r.ORM.Exec("DELETE FROM tb_ai_chunk WHERE id=?", original.ID).Error != nil {
			t.Fatal("fixture mutation failed")
		}
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatal("partial chunk set silently searched")
		}
	})
	t.Run("missing_chunks_is_unready", func(t *testing.T) {
		if r.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", doc).Error != nil {
			t.Fatal("fixture mutation failed")
		}
		p.calls = 0
		_, err := s.Search(ctx, "停车规则", &shop, 5, 0)
		if !errors.Is(err, ErrUnsafe) || p.calls != 0 {
			t.Fatal("missing chunks became a successful refusal")
		}
	})
}
