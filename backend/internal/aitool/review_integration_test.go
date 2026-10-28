package aitool

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"os"
	"strconv"
	"testing"
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
	if err = resources.ORM.Exec("INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES(?,1,'review-tool',9007199254740993,9007199254740994,1,1)", id).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resources.ORM.Exec("DELETE FROM tb_seckill_voucher WHERE voucher_id=?", id).Error
		_ = resources.ORM.Exec("DELETE FROM tb_voucher WHERE id=?", id).Error
	})
	if err = resources.ORM.Exec("INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time) VALUES(?,1,1,1,'ACTIVE',1,NOW(),DATE_ADD(NOW(),INTERVAL 1 DAY))", id).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(resources.ORM)
	result, err := service.Route(ctx, 1, "活动 "+strconv.FormatUint(id, 10))
	if err != nil || result == nil || result.Card == nil || result.Card["payValue"] != "9007199254740993" || result.Card["type"] != "activities" {
		t.Fatal("activity tool must query public price with exact cents and a renderable card")
	}
	if err = resources.ORM.Exec("UPDATE tb_seckill_voucher SET publish_status='OFFLINE' WHERE voucher_id=?", id).Error; err != nil {
		t.Fatal(err)
	}
	result, err = service.Route(ctx, 1, "活动 "+strconv.FormatUint(id, 10))
	if err != nil || result == nil || result.Card != nil {
		t.Fatal("offline activity must not leak through public AI tool")
	}
}
