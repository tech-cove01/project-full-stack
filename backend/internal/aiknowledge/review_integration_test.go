package aiknowledge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"os"
	"strings"
	"testing"
)

type observedProvider struct {
	*ai.Stub
	calls   int
	before  func()
	failure bool
}

func (p *observedProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	p.calls++
	if p.before != nil {
		p.before()
	}
	if p.failure {
		return nil, errors.New("synthetic provider unavailable")
	}
	return p.Stub.Embed(ctx, texts)
}

func TestIntegrationReviewKnowledgeAuthorizationAndVersions(t *testing.T) {
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
	user := insertUser(t, resources)
	merchant := insertMerchant(t, resources)
	shop := insertShop(t, resources, merchant)
	insertMember(t, resources, merchant, user)
	doc := insertDocument(t, resources, shop, "重建测试文档", "停车规则：一小时免费。")
	insertVersion(t, resources, doc, "停车规则：一小时免费。")
	p := &observedProvider{Stub: ai.NewStub(256)}
	service := NewService(resources.ORM, p)
	t.Cleanup(func() {
		_ = resources.ORM.Exec("DELETE FROM tb_audit_log WHERE merchant_id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", doc).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", doc).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id=?", doc).Error
		_ = resources.ORM.Exec("DELETE FROM tb_merchant_member WHERE merchant_id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_shop WHERE id=?", shop).Error
		_ = resources.ORM.Exec("DELETE FROM tb_merchant WHERE id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_user WHERE id=?", user).Error
	})
	t.Run("unauthorized_rebuild_never_calls_provider", func(t *testing.T) {
		p.calls = 0
		_, err := service.Rebuild(ctx, decimal(user+1), decimal(doc))
		if !errors.Is(err, ErrForbidden) || p.calls != 0 {
			t.Fatal("rebuild leaked source to provider before authorizing")
		}
	})
	t.Run("disabled_rebuild_never_calls_provider", func(t *testing.T) {
		if err := resources.ORM.Exec("UPDATE tb_ai_document SET disabled=1,status='DISABLED' WHERE id=?", doc).Error; err != nil {
			t.Fatal(err)
		}
		defer resources.ORM.Exec("UPDATE tb_ai_document SET disabled=0,status='PUBLISHED' WHERE id=?", doc)
		p.calls = 0
		_, err := service.Rebuild(ctx, decimal(user), decimal(doc))
		if !errors.Is(err, ErrConflict) || p.calls != 0 {
			t.Fatal("disabled document reached embedding provider")
		}
	})
	t.Run("publish_failure_retains_current_version", func(t *testing.T) {
		p.failure = true
		defer func() { p.failure = false }()
		_, err := service.Publish(ctx, decimal(user), decimal(doc))
		if err == nil {
			t.Fatal("provider failure must reject publication")
		}
		v, err := service.Public(ctx, decimal(doc), nil)
		if err != nil || v.Version != 1 {
			t.Fatal("failed publication replaced old current version")
		}
	})
	t.Run("nonpublic_scope_is_never_a_public_source", func(t *testing.T) {
		if err := resources.ORM.Exec("UPDATE tb_ai_document SET scope='PRIVATE' WHERE id=?", doc).Error; err != nil {
			t.Fatal(err)
		}
		defer resources.ORM.Exec("UPDATE tb_ai_document SET scope='SHOP_PUBLIC' WHERE id=?", doc)
		if _, err := service.Public(ctx, decimal(doc), nil); !errors.Is(err, ErrNotFound) {
			t.Fatal("non-public published source was returned")
		}
		hits, err := service.Search(ctx, "停车规则", &shop, 5, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range hits {
			if hit.DocumentID == doc {
				t.Fatal("private document reached public retrieval")
			}
		}
	})
	t.Run("revocation_during_indexing_rejects_commit", func(t *testing.T) {
		p.before = func() {
			if e := resources.ORM.Exec("UPDATE tb_merchant_member SET status='DISABLED' WHERE merchant_id=? AND user_id=?", merchant, user).Error; e != nil {
				t.Fatal(e)
			}
		}
		defer func() {
			p.before = nil
			resources.ORM.Exec("UPDATE tb_merchant_member SET status='ACTIVE' WHERE merchant_id=? AND user_id=?", merchant, user)
		}()
		_, err := service.Publish(ctx, decimal(user), decimal(doc))
		if !errors.Is(err, ErrForbidden) {
			t.Fatal("revoked manager committed publication")
		}
		v, err := service.Public(ctx, decimal(doc), nil)
		if err != nil || v.Version != 1 {
			t.Fatal("revoked publication changed current version")
		}
	})
	t.Run("publish_rebuild_disable_audits_and_old_version_hidden", func(t *testing.T) {
		if _, err := service.Publish(ctx, decimal(user), decimal(doc)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.UpdateDraft(ctx, decimal(user), decimal(doc), "草稿标题CANARY", "草稿正文CANARY"); err != nil {
			t.Fatal(err)
		}
		public, err := service.Public(ctx, decimal(doc), nil)
		if err != nil || public.Title != "重建测试文档" || public.Body != "停车规则：一小时免费。" {
			t.Fatal("unpublished title/body leaked into public version")
		}
		items, err := service.List(ctx, decimal(user), decimal(shop))
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(items)
		if !strings.Contains(string(encoded), "草稿正文CANARY") {
			t.Fatal("manager list must include editable draft body")
		}
		old := 1
		if _, err := service.Public(ctx, decimal(doc), &old); !errors.Is(err, ErrNotFound) {
			t.Fatal("superseded public source remains readable")
		}
		if _, err := service.Rebuild(ctx, decimal(user), decimal(doc)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Disable(ctx, decimal(user), decimal(doc)); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := resources.ORM.Raw("SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=? AND action IN ('KNOWLEDGE_PUBLISH','KNOWLEDGE_REBUILD','KNOWLEDGE_DISABLE')", merchant).Scan(&count).Error; err != nil || count != 3 {
			t.Fatal("knowledge mutations must commit matching audits")
		}
	})
}

func TestIntegrationReviewKnowledgeListBoundary(t *testing.T) {
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
	owner := insertUser(t, resources)
	staff := insertUser(t, resources)
	merchant := insertMerchant(t, resources)
	shop := insertShop(t, resources, merchant)
	insertMember(t, resources, merchant, owner)
	if err = resources.ORM.Exec("INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(?,?,'STAFF','ACTIVE')", merchant, staff).Error; err != nil {
		t.Fatal(err)
	}
	var member uint64
	if err = resources.ORM.Raw("SELECT id FROM tb_merchant_member WHERE merchant_id=? AND user_id=?", merchant, staff).Scan(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err = resources.ORM.Exec("INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES(?,?)", member, shop).Error; err != nil {
		t.Fatal(err)
	}
	doc := insertDocument(t, resources, shop, "重建测试文档", "DRAFT-PRIVATE-CANARY")
	insertVersion(t, resources, doc, "PUBLISHED-PUBLIC")
	service := NewService(resources.ORM, ai.NewStub(256))
	draft, err := service.CreateDraft(ctx, decimal(owner), decimal(shop), "UNPUBLISHED-TITLE-CANARY", "UNPUBLISHED-BODY-CANARY")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resources.ORM.Exec("DELETE FROM tb_audit_log WHERE merchant_id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_chunk WHERE document_id IN (?,?)", doc, draft.ID).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document_version WHERE document_id IN (?,?)", doc, draft.ID).Error
		_ = resources.ORM.Exec("DELETE FROM tb_ai_document WHERE id IN (?,?)", doc, draft.ID).Error
		_ = resources.ORM.Exec("DELETE FROM tb_member_shop_scope WHERE member_id=?", member).Error
		_ = resources.ORM.Exec("DELETE FROM tb_merchant_member WHERE merchant_id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_shop WHERE id=?", shop).Error
		_ = resources.ORM.Exec("DELETE FROM tb_merchant WHERE id=?", merchant).Error
		_ = resources.ORM.Exec("DELETE FROM tb_user WHERE id IN (?,?)", owner, staff).Error
	})
	t.Run("manager_list_contains_editable_draft_body", func(t *testing.T) {
		items, err := service.List(ctx, decimal(owner), decimal(shop))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(items)
		if len(items) != 2 || !strings.Contains(string(raw), "DRAFT-PRIVATE-CANARY") {
			t.Fatal("manager edit list lost draft body")
		}
	})
	t.Run("scoped_staff_reads_only_published_body", func(t *testing.T) {
		items, err := service.List(ctx, decimal(staff), decimal(shop))
		if err != nil {
			t.Fatal("scoped staff read rejected", err)
		}
		raw, _ := json.Marshal(items)
		if len(items) != 1 || !strings.Contains(string(raw), "PUBLISHED-PUBLIC") || strings.Contains(string(raw), "PRIVATE-CANARY") || strings.Contains(string(raw), "UNPUBLISHED") {
			t.Fatal("staff list leaked draft content or omitted published body")
		}
	})
	t.Run("unchanged_draft_save_is_a_noop", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			if _, err := service.UpdateDraft(ctx, decimal(owner), decimal(doc), "重建测试文档", "DRAFT-PRIVATE-CANARY"); err != nil {
				t.Fatal("unchanged save incorrectly failed", err)
			}
		}
	})
}
