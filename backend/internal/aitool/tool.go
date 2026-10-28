// Package aitool answers "my own data" questions with read-only queries bound to
// the authenticated user. It never accepts a user id, phone or token from the
// request, and it never mutates a business row.
package aitool

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"gorm.io/gorm"
)

type Kind string

const (
	KindOrders   Kind = "orders"
	KindOrder    Kind = "order"
	KindActivity Kind = "activity"
	KindShop     Kind = "shop"
)

// Result carries the text the assistant may show and the structured card the
// frontend can render. Cards contain only public or self-owned fields.
type Result struct {
	Kind Kind
	Text string
	Card map[string]any
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

var (
	// Order ids are snowflakes; treating a short number as an order id would turn
	// "订单 3 个" into a lookup.
	orderIDPattern = regexp.MustCompile(`\d{10,20}`)
	shortIDPattern = regexp.MustCompile(`\b\d{1,20}\b`)
)

// parse recognizes only explicit intents. A matched intent without a plausible id
// falls back to the list form; an unrelated sentence returns no intent at all so
// the caller can continue with retrieval.
func parse(message string) (Kind, string) {
	text := strings.TrimSpace(message)
	switch {
	case strings.Contains(text, "订单"):
		if id := orderIDPattern.FindString(text); id != "" {
			return KindOrder, id
		}
		return KindOrders, ""
	case strings.Contains(text, "活动"):
		if id := shortIDPattern.FindString(text); id != "" {
			return KindActivity, id
		}
	case strings.Contains(text, "门店") || strings.Contains(text, "店铺"):
		if id := shortIDPattern.FindString(text); id != "" {
			return KindShop, id
		}
	}
	return "", ""
}

// Route returns nil when the message is not a data question.
func (service *Service) Route(ctx context.Context, userID uint64, message string) (*Result, error) {
	kind, id := parse(message)
	switch kind {
	case KindOrders:
		return service.recentOrders(ctx, userID)
	case KindOrder:
		return service.oneOrder(ctx, userID, id)
	case KindActivity:
		return service.activity(ctx, id)
	case KindShop:
		return service.shop(ctx, id)
	}
	return nil, nil
}

func zone() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return location
}

func display(value *time.Time) *string {
	if value == nil {
		return nil
	}
	text := value.In(zone()).Format("2006-01-02 15:04:05")
	return &text
}

func statusMessage(status int) string {
	switch status {
	case 1:
		return "待支付"
	case 2:
		return "已支付，待核销"
	case 3:
		return "已核销"
	case 4:
		return "已取消"
	case 6:
		return "已退款"
	default:
		return "状态未知"
	}
}

type orderRow struct {
	ID        int64
	Status    int
	PayAmount uint64
	CreateAt  time.Time
	PayTime   *time.Time
	UseTime   *time.Time
}

func (service *Service) recentOrders(ctx context.Context, userID uint64) (*Result, error) {
	var rows []orderRow
	if err := service.db.WithContext(ctx).Raw(`SELECT id,status,pay_amount,create_time AS create_at,
		pay_time,use_time FROM tb_voucher_order WHERE user_id=?
		ORDER BY create_time DESC,id DESC LIMIT 5`, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	orders := make([]map[string]any, 0, len(rows))
	pending := 0
	for _, row := range rows {
		if row.Status == 1 {
			pending++
		}
		orders = append(orders, map[string]any{
			"id": strconv.FormatInt(row.ID, 10), "status": row.Status,
			"statusText": statusMessage(row.Status), "payAmount": strconv.FormatUint(row.PayAmount, 10),
			"createTime": display(&row.CreateAt),
		})
	}
	text := "你还没有订单。"
	if len(rows) > 0 {
		text = "你最近有 " + strconv.Itoa(len(rows)) + " 个订单"
		if pending > 0 {
			text += "，其中 " + strconv.Itoa(pending) + " 个待支付"
		}
		text += "。"
	}
	return &Result{Kind: KindOrders, Text: text, Card: map[string]any{"type": "orders", "items": orders, "orders": orders}}, nil
}

func (service *Service) oneOrder(ctx context.Context, userID uint64, orderID string) (*Result, error) {
	id, err := strconv.ParseInt(orderID, 10, 64)
	if err != nil || id <= 0 {
		return &Result{Kind: KindOrder, Text: "订单号格式不正确。"}, nil
	}
	var rows []orderRow
	if err := service.db.WithContext(ctx).Raw(`SELECT id,status,pay_amount,create_time AS create_at,
		pay_time,use_time FROM tb_voucher_order WHERE id=? AND user_id=?`, id, userID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		// Unknown and someone else's order look identical: no existence probing.
		return &Result{Kind: KindOrder, Text: "没有找到这个订单。"}, nil
	}
	row := rows[0]
	return &Result{Kind: KindOrder, Text: "订单状态：" + statusMessage(row.Status) + "。",
		Card: map[string]any{
			"type": "order",
			"id":   strconv.FormatInt(row.ID, 10), "status": row.Status,
			"statusText": statusMessage(row.Status), "payAmount": strconv.FormatUint(row.PayAmount, 10),
			"createTime": display(&row.CreateAt), "payTime": display(row.PayTime), "useTime": display(row.UseTime),
		}}, nil
}

func (service *Service) activity(ctx context.Context, activityID string) (*Result, error) {
	id, err := strconv.ParseUint(activityID, 10, 64)
	if err != nil || id == 0 {
		return &Result{Kind: KindActivity, Text: "活动编号格式不正确。"}, nil
	}
	var rows []struct {
		ID        uint64
		ShopID    uint64
		Title     string
		PayValue  uint64
		Stock     uint64
		BeginTime time.Time
		EndTime   time.Time
		Status    string
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT sv.voucher_id AS id,v.shop_id,v.title,v.pay_value,sv.stock,
		sv.begin_time,sv.end_time,sv.publish_status AS status
		FROM tb_seckill_voucher sv JOIN tb_voucher v ON v.id=sv.voucher_id
		WHERE sv.voucher_id=? AND v.status=1 AND sv.publish_status='ACTIVE'`, id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return &Result{Kind: KindActivity, Text: "没有找到这个活动。"}, nil
	}
	row := rows[0]
	item := map[string]any{
		"id": strconv.FormatUint(row.ID, 10), "shopId": strconv.FormatUint(row.ShopID, 10),
		"title": row.Title, "payValue": strconv.FormatUint(row.PayValue, 10), "status": row.Status,
		"stock": strconv.FormatUint(row.Stock, 10), "href": "/shops/" + strconv.FormatUint(row.ShopID, 10),
	}
	return &Result{Kind: KindActivity, Text: "活动：" + row.Title + "。",
		Card: map[string]any{"type": "activities", "items": []map[string]any{item}, "id": strconv.FormatUint(row.ID, 10), "title": row.Title,
			"payValue": strconv.FormatUint(row.PayValue, 10), "status": row.Status,
			"beginTime": display(&row.BeginTime), "endTime": display(&row.EndTime)}}, nil
}

func (service *Service) shop(ctx context.Context, shopID string) (*Result, error) {
	id, err := strconv.ParseUint(shopID, 10, 64)
	if err != nil || id == 0 {
		return &Result{Kind: KindShop, Text: "门店编号格式不正确。"}, nil
	}
	var rows []struct {
		ID      uint64
		Name    string
		Address string
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT id,name,address FROM tb_shop WHERE id=?`, id).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return &Result{Kind: KindShop, Text: "没有找到这个门店。"}, nil
	}
	row := rows[0]
	return &Result{Kind: KindShop, Text: "门店：" + row.Name + "。",
		Card: map[string]any{"type": "shops", "items": []map[string]any{{"id": strconv.FormatUint(row.ID, 10), "name": row.Name, "address": row.Address}}, "id": strconv.FormatUint(row.ID, 10), "name": row.Name, "address": row.Address}}, nil
}
