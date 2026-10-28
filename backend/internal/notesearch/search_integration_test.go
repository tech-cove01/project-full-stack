package notesearch

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/gorm"
)

func searchInsertID(t *testing.T, db *gorm.DB, statement string, args ...any) uint64 {
	t.Helper()
	var id uint64
	if db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(statement, args...).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error
	}) != nil || id == 0 {
		t.Fatal("owned fixture insert failed")
	}
	return id
}

// Real ES+SQL, using only a unique alias/generation and owned business rows.
func TestIntegrationPublicNoteSearch(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" || os.Getenv("ES_URL") == "" {
		t.Skip("explicit isolated SQL/Redis/ES required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	lock, err := resources.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("fixture lock unavailable")
	}
	var acquired int
	if lock.QueryRowContext(ctx, "SELECT GET_LOCK('go076_integration',30)").Scan(&acquired) != nil || acquired != 1 {
		_ = lock.Close()
		t.Fatal("fixture lock unavailable")
	}
	t.Cleanup(func() {
		_, _ = lock.ExecContext(context.Background(), "SELECT RELEASE_LOCK('go076_integration')")
		_ = lock.Close()
	})
	alias := "go076-search-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	client, err := NewClient(Config{URL: os.Getenv("ES_URL"), Index: alias, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	generation := alias + "-build-test"
	if client.CreateGeneration(ctx, generation) != nil {
		t.Fatal("create private ES generation failed")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, status, err := client.request(cleanup, "DELETE", "/"+generation, nil)
		if err != nil || status != 200 {
			t.Error("private ES generation cleanup failed")
		}
	})
	merchant := searchInsertID(t, resources.ORM, "INSERT INTO tb_merchant(name,status) VALUES ('GO076搜索夹具','ACTIVE')")
	shop1 := searchInsertID(t, resources.ORM, "INSERT INTO tb_shop(merchant_id,name,type_id,images,address,x,y) VALUES (?,'GO076搜索甲',1,'','合成地址',0,0)", merchant)
	shop2 := searchInsertID(t, resources.ORM, "INSERT INTO tb_shop(merchant_id,name,type_id,images,address,x,y) VALUES (?,'GO076搜索乙',1,'','合成地址',0,0)", merchant)
	user := uint64(time.Now().UnixNano())
	if resources.ORM.Exec("INSERT INTO tb_user(id,phone,nick_name) VALUES (?,?, 'GO076搜索用户')", user, fmt.Sprintf("139%08d", user%100000000)).Error != nil {
		t.Fatal("private user fixture failed")
	}
	ids := make([]uint64, 6)
	for i := range ids {
		ids[i] = user + uint64(i) + 1
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		db := resources.ORM.WithContext(cleanup)
		for _, statement := range []struct {
			sql  string
			args []any
		}{{"DELETE FROM tb_blog WHERE id IN ?", []any{ids}}, {"DELETE FROM tb_user WHERE id=?", []any{user}}, {"DELETE FROM tb_shop WHERE id IN ?", []any{[]uint64{shop1, shop2}}}, {"DELETE FROM tb_merchant WHERE id=?", []any{merchant}}} {
			if db.Exec(statement.sql, statement.args...).Error != nil {
				t.Error("private SQL fixture cleanup failed")
			}
		}
	})
	titles := []string{"城市咖啡", "城市散步", "咖啡路线", "咖啡草稿", "咖啡旧版", "咖啡新文"}
	contents := []string{"停车便利", "午后咖啡好喝", "沿河散步", "DRAFT-DO-NOT-RETURN", "当前数据库新正文", "后来发布"}
	docs := make([]Document, 6)
	for i, id := range ids {
		shop := shop1
		if i == 1 {
			shop = shop2
		}
		status := "PUBLISHED"
		version := 1
		if i == 3 {
			status = "DRAFT"
		}
		if i == 4 {
			version = 2
		}
		if resources.ORM.Exec(`INSERT INTO tb_blog(id,shop_id,user_id,title,images,content,status,publish_version,create_time) VALUES (?,?,?,?,?,?,?,?,?)`, id, shop, user, titles[i], "https://fixture.example/note.jpg", contents[i], status, version, "2026-10-28 12:00:00").Error != nil {
			t.Fatal("private note fixture failed")
		}
		docs[i] = Document{ID: strconv.FormatUint(id, 10), ShopID: strconv.FormatUint(shop, 10), UserID: strconv.FormatUint(user, 10), Title: titles[i], Content: "ES候选文本不得直接返回 " + contents[i], Version: 1, PublishedAt: "2026-10-28T04:00:00Z"}
		if i < 5 && client.Write(ctx, generation, docs[i]) != nil {
			t.Fatal("private ES document write failed")
		}
	}
	if client.Refresh(ctx, generation) != nil || client.Activate(ctx, generation) != nil {
		t.Fatal("private index activation failed")
	}
	service := NewService(resources.ORM, client)
	t.Run("Chinese_query_AND_shop_and_SQL_content", func(t *testing.T) {
		page, err := service.Search(ctx, SearchRequest{Query: "咖啡", ShopID: strconv.FormatUint(shop1, 10), Limit: 20, Sort: "relevance"})
		if err != nil || len(page.Items) != 2 {
			t.Fatalf("Chinese query returned incorrect visible set: count%d err%v", len(page.Items), err)
		}
		for _, item := range page.Items {
			if item.UserID != strconv.FormatUint(user, 10) || item.ShopID == nil || *item.ShopID != strconv.FormatUint(shop1, 10) || len(item.Images) != 1 || item.Content == "" || len(item.ID) < 16 || strings.HasPrefix(item.Content, "ES") {
				t.Fatal("must return only current SQL facts with string IDs")
			}
		}
		empty, err := service.Search(ctx, SearchRequest{Query: "咖啡火星", Limit: 20})
		if err != nil || len(empty.Items) != 0 {
			t.Fatal("AND query must not match only coffee half")
		}
	})
	t.Run("PIT_short_empty_page_advances_raw_cursor_and_filters_current_facts", func(t *testing.T) {
		request := SearchRequest{Query: "咖啡", ShopID: strconv.FormatUint(shop1, 10), Limit: 2, Sort: "recent"}
		first, err := service.Search(ctx, request)
		if err != nil || len(first.Items) != 0 || first.NextCursor == "" {
			t.Fatal("filtered raw page must keep advancing cursor")
		}
		if resources.ORM.Exec("UPDATE tb_blog SET status='DRAFT' WHERE id=?", ids[2]).Error != nil {
			t.Fatal("controlled downline failed")
		}
		defer resources.ORM.Exec("UPDATE tb_blog SET status='PUBLISHED' WHERE id=?", ids[2])
		if client.Write(ctx, client.Index(), docs[5]) != nil || client.Refresh(ctx, client.Index()) != nil {
			t.Fatal("concurrent new note setup failed")
		}
		request.Cursor = first.NextCursor
		second, err := service.Search(ctx, request)
		if err != nil || len(second.Items) != 1 || second.Items[0].ID != docs[0].ID {
			t.Fatal("PIT must exclude later indexed notes while SQL hides newly downlined candidates")
		}
		if second.NextCursor != "" {
			request.Cursor = second.NextCursor
			last, err := service.Search(ctx, request)
			if err != nil || len(last.Items) != 0 || last.NextCursor != "" {
				t.Fatal("last PIT page must close without repeat")
			}
		}
	})
	t.Run("external_version_guards", func(t *testing.T) {
		newer := docs[0]
		newer.Version = 3
		if client.Write(ctx, client.Index(), newer) != nil || client.Write(ctx, client.Index(), newer) != nil {
			t.Fatal("duplicate external version must converge")
		}
		if client.Write(ctx, client.Index(), docs[0]) != ErrVersionConflict {
			t.Fatal("older version must not overwrite newer ES document")
		}
	})
}
