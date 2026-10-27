package aiknowledge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

// The suite owns one merchant, one shop, one member, one document and its
// version; everything it inserts is deleted again.
func TestIntegrationKnowledgeRebuild(t *testing.T) {
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
	service := NewService(resources.ORM, stub)

	user := insertUser(t, resources)
	merchant := insertMerchant(t, resources)
	shop := insertShop(t, resources, merchant)
	insertMember(t, resources, merchant, user)
	document := insertDocument(t, resources, shop, "重建测试文档", "核销后不可退款，核销前请联系门店。")
	insertVersion(t, resources, document, "核销后不可退款，核销前请联系门店。")
	// A stale chunk that the rebuild must replace.
	if err = resources.ORM.WithContext(ctx).Exec(`INSERT INTO tb_ai_chunk
		(document_id,version,epoch,ordinal,content,embedding,embedding_model,embedding_dimension)
		VALUES (?,1,1,0,'过时内容','[0.0]','old-model',1)`, document).Error; err != nil {
		t.Fatal("chunk fixture insert failed")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_chunk WHERE document_id=?", document).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_document_version WHERE document_id=?", document).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_ai_document WHERE id=?", document).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_merchant_member WHERE user_id=?", user).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_shop WHERE id=?", shop).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_merchant WHERE id=?", merchant).Error
		_ = resources.ORM.WithContext(cleanupCtx).Exec("DELETE FROM tb_user WHERE id=?", user).Error
	})

	view, err := service.Rebuild(ctx, decimal(user), decimal(document))
	if err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if view.PublishedVersion == nil || *view.PublishedVersion != 1 {
		t.Fatalf("a rebuild must not change the published version: %+v", view)
	}
	var chunks []struct {
		Content            string
		EmbeddingModel     string
		EmbeddingDimension int
	}
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT content,embedding_model,
		embedding_dimension FROM tb_ai_chunk WHERE document_id=? AND version=1 ORDER BY ordinal`,
		document).Scan(&chunks).Error; err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Content == "过时内容" {
		t.Fatalf("the rebuild must replace the stale chunk: %+v", chunks)
	}
	if chunks[0].EmbeddingModel != stub.Model() || chunks[0].EmbeddingDimension != stub.Dimension() {
		t.Fatalf("the rebuilt chunk must carry the active model: %+v", chunks[0])
	}
}

func insertUser(t *testing.T, resources *database.Resources) uint64 {
	t.Helper()
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	phone := "134" + strconv.FormatUint(uint64(binary.BigEndian.Uint32(entropy[:]))%100000000, 10)
	if err := resources.ORM.Exec(`INSERT INTO tb_user (phone,password,nick_name,icon)
		VALUES (?,?,?,?)`, phone, "x", "重建测试", "").Error; err != nil {
		t.Fatal(err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT id FROM tb_user WHERE phone=? ORDER BY id DESC LIMIT 1", phone).Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("user id unavailable")
	}
	return id
}

func insertMerchant(t *testing.T, resources *database.Resources) uint64 {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_merchant (name,status) VALUES ('重建测试商家','ACTIVE')`).Error; err != nil {
		t.Fatal(err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT id FROM tb_merchant WHERE name=? ORDER BY id DESC LIMIT 1", "重建测试商家").Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("merchant id unavailable")
	}
	return id
}

func insertShop(t *testing.T, resources *database.Resources, merchant uint64) uint64 {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_shop
		(merchant_id,name,type_id,images,address,x,y,open_hours) VALUES (?,'重建测试门店',1,'','重建测试地址',0,0,'09:00-21:00')`,
		merchant).Error; err != nil {
		t.Fatal(err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT id FROM tb_shop WHERE name=? ORDER BY id DESC LIMIT 1", "重建测试门店").Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("shop id unavailable")
	}
	return id
}

func insertMember(t *testing.T, resources *database.Resources, merchant, user uint64) {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_merchant_member
		(merchant_id,user_id,role_code,status) VALUES (?,?,'OWNER','ACTIVE')`,
		merchant, user).Error; err != nil {
		t.Fatal(err)
	}
}

func insertDocument(t *testing.T, resources *database.Resources, shop uint64, title, body string) uint64 {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_ai_document
		(scope,shop_id,title,body,status,published_version,epoch,disabled)
		VALUES ('SHOP_PUBLIC',?,?,?,'PUBLISHED',1,1,0)`, shop, title, body).Error; err != nil {
		t.Fatal(err)
	}
	var id uint64
	if err := resources.ORM.Raw("SELECT id FROM tb_ai_document WHERE title=? ORDER BY id DESC LIMIT 1", "重建测试文档").Scan(&id).Error; err != nil || id == 0 {
		t.Fatal("document id unavailable")
	}
	return id
}

func insertVersion(t *testing.T, resources *database.Resources, document uint64, body string) {
	t.Helper()
	if err := resources.ORM.Exec(`INSERT INTO tb_ai_document_version
		(document_id,version,epoch,body,content_hash,status,embedding_model,embedding_dimension)
		VALUES (?,1,1,?,?,'READY','old-model',1)`, document, body, contentHash(body)).Error; err != nil {
		t.Fatal(err)
	}
}
