package aitool

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"strconv"
	"testing"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func TestIntegrationReviewActivityTool(t *testing.T) {
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
	var entropy [4]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	id := uint64(9007199254740993) + uint64(binary.BigEndian.Uint32(entropy[:]))
	shopID := id + 1
	if err = resources.ORM.Exec("INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(?,1,'review-activity-tool',1,'synthetic public address',0,0)", shopID).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := resources.ORM.Exec("DELETE FROM tb_shop WHERE id=?", shopID).Error; cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	if err = resources.ORM.Exec("INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,?,'review-tool',9007199254740993,9007199254740994,1,1)", id, shopID).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := resources.ORM.Exec("DELETE FROM tb_seckill_voucher WHERE voucher_id=?", id).Error; cleanupErr != nil {
			t.Error(cleanupErr)
		}
		if cleanupErr := resources.ORM.Exec("DELETE FROM tb_voucher WHERE id=?", id).Error; cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	if err = resources.ORM.Exec("INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time) VALUES(?,1,1,1,'ACTIVE',1,NOW(),DATE_ADD(NOW(),INTERVAL 1 DAY))", id).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(resources.ORM)
	assertPublicCard := func(t *testing.T, stock string) {
		t.Helper()
		result, routeErr := service.Route(ctx, 1, "活动 "+strconv.FormatUint(id, 10))
		if routeErr != nil || result == nil || result.Card == nil || result.Card["payValue"] != "9007199254740993" || result.Card["type"] != "activities" {
			t.Fatal("activity tool must query public price with exact cents and a renderable card")
		}
		items, ok := result.Card["items"].([]map[string]any)
		if !ok || len(items) != 1 {
			t.Fatal("activity tool must return exactly one renderable public item")
		}
		for key, want := range map[string]string{
			"id": strconv.FormatUint(id, 10), "shopId": strconv.FormatUint(shopID, 10),
			"stock": stock, "payValue": "9007199254740993", "href": "/shops/" + strconv.FormatUint(shopID, 10),
		} {
			if got := items[0][key]; got != want {
				t.Errorf("activity item %s = %#v, want exact string %q", key, got, want)
			}
		}
	}
	t.Run("public_card_stock_and_large_string_ids", func(t *testing.T) { assertPublicCard(t, "1") })
	t.Run("same_service_reads_updated_zero_stock", func(t *testing.T) {
		if update := resources.ORM.Exec("UPDATE tb_seckill_voucher SET stock=0 WHERE voucher_id=?", id); update.Error != nil || update.RowsAffected != 1 {
			t.Fatal("owned stock update failed")
		}
		assertPublicCard(t, "0")
	})
	t.Run("unpublished_voucher_stays_hidden", func(t *testing.T) {
		if err = resources.ORM.Exec("UPDATE tb_voucher SET status=0 WHERE id=?", id).Error; err != nil {
			t.Fatal(err)
		}
		result, routeErr := service.Route(ctx, 1, "活动 "+strconv.FormatUint(id, 10))
		if routeErr != nil || result == nil || result.Card != nil {
			t.Fatal("unpublished voucher must not leak through public AI tool")
		}
		if err = resources.ORM.Exec("UPDATE tb_voucher SET status=1 WHERE id=?", id).Error; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("offline_activity_stays_hidden", func(t *testing.T) {
		if err = resources.ORM.Exec("UPDATE tb_seckill_voucher SET publish_status='OFFLINE' WHERE voucher_id=?", id).Error; err != nil {
			t.Fatal(err)
		}
		result, routeErr := service.Route(ctx, 1, "活动 "+strconv.FormatUint(id, 10))
		if routeErr != nil || result == nil || result.Card != nil {
			t.Fatal("offline activity must not leak through public AI tool")
		}
	})
}
