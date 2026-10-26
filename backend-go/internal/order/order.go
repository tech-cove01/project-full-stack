// Package order separates durable order facts from asynchronous admission.
package order

import (
	"context"
	"errors"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/orderadmission"
)

var (
	ErrNotFound   = errors.New("订单不存在")
	ErrCollision  = errors.New("订单标识与已有持久记录不一致")
	ErrUnsafe     = errors.New("订单状态需要核对，请稍后重试")
	ErrActivity   = errors.New("活动尚未就绪或已下架")
	ErrPagination = errors.New("页码必须为正整数，pageSize必须在1到50之间")
	ErrInvalidID  = errors.New("ID必须为正十进制整数字符串")
	businessZone  = loadBusinessZone()
)

func loadBusinessZone() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return zone
}

const (
	Created    = "CREATED"
	Failed     = "FAILED"
	Processing = "PROCESSING"
)

// OrderStatus is explicitly null before a DB order exists. Unknown or absent
// snapshot fields remain omitted; no Redis status invents money or deadlines.
type View struct {
	ID              string  `json:"id"`
	State           string  `json:"state"`
	Message         string  `json:"message"`
	OrderStatus     *int    `json:"orderStatus"`
	ShopID          *string `json:"shopId,omitempty"`
	PayAmount       *string `json:"payAmount,omitempty"`
	PayDeadlineAt   *string `json:"payDeadlineAt,omitempty"`
	RedeemBeginTime *string `json:"redeemBeginTime,omitempty"`
	RedeemEndTime   *string `json:"redeemEndTime,omitempty"`
	RedeemToken     *string `json:"redeemToken,omitempty"`
	CreateTime      *string `json:"createTime,omitempty"`
	PayTime         *string `json:"payTime,omitempty"`
	UseTime         *string `json:"useTime,omitempty"`
	RefundTime      *string `json:"refundTime,omitempty"`
}

// A nil error is evidence of a durable result: CREATED is a committed order,
// FAILED is a committed rejection event. Every error remains unknown to the
// Stream consumer, which must retain the message pending rather than ACK it.
type Outcome struct {
	State string
	View  *View
}

type Creator struct {
	db         *gorm.DB
	payTimeout time.Duration
}

func NewCreator(db *gorm.DB, payTimeout time.Duration) *Creator {
	return &Creator{db: db, payTimeout: payTimeout}
}

type admission interface {
	NextID(context.Context, time.Time) (string, error)
	Recover(context.Context, string, string) (string, error)
	Reserve(context.Context, string, string, string, time.Time, time.Time, time.Time, int) (orderadmission.Result, error)
}

type Service struct {
	db        *gorm.DB
	redis     *redis.Client
	admission admission
}

func NewService(db *gorm.DB, rdb *redis.Client, payTimeout time.Duration) *Service {
	return &Service{db: db, redis: rdb, admission: orderadmission.New(rdb)}
}

type Query struct {
	Current  uint64
	PageSize uint64
}

func unsignedID(value string) (uint64, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
		return 0, ErrInvalidID
	}
	return n, nil
}

func orderID(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return 0, ErrInvalidID
	}
	return n, nil
}

func decimal(value uint64) string { return strconv.FormatUint(value, 10) }

type record struct {
	ID              int64 `gorm:"primaryKey;autoIncrement:false"`
	UserID          uint64
	VoucherID       uint64
	ShopID          uint64
	PayType         uint8
	PayAmount       uint64
	Status          int
	Version         int
	PayDeadlineAt   time.Time
	RedeemBeginTime *time.Time
	RedeemEndTime   *time.Time
	RedeemToken     *string
	CreateTime      time.Time
	PayTime         *time.Time
	UseTime         *time.Time
	RefundTime      *time.Time
}

func (record) TableName() string { return "tb_voucher_order" }

const columns = `id,user_id,voucher_id,shop_id,pay_type,pay_amount,status,version,
 pay_deadline_at,redeem_begin_time,redeem_end_time,redeem_token,create_time,pay_time,use_time,refund_time`

func toView(row record, includeToken bool) View {
	status := row.Status
	shop, amount := decimal(row.ShopID), decimal(row.PayAmount)
	view := View{ID: strconv.FormatInt(row.ID, 10), State: Created, Message: statusMessage(status), OrderStatus: &status,
		ShopID: &shop, PayAmount: &amount, PayDeadlineAt: formatted(&row.PayDeadlineAt),
		RedeemBeginTime: formatted(row.RedeemBeginTime), RedeemEndTime: formatted(row.RedeemEndTime),
		CreateTime: formatted(&row.CreateTime), PayTime: formatted(row.PayTime), UseTime: formatted(row.UseTime), RefundTime: formatted(row.RefundTime)}
	if includeToken && status == 2 {
		view.RedeemToken = row.RedeemToken
	}
	return view
}

func formatted(value *time.Time) *string {
	if value == nil {
		return nil
	}
	text := value.In(businessZone).Format("2006-01-02 15:04:05")
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
		return "订单状态未知"
	}
}

func pageOffset(query Query, total uint64) (uint64, bool) {
	if query.Current == 0 || query.PageSize == 0 || total == 0 || query.Current-1 > (total-1)/query.PageSize {
		return 0, false
	}
	return (query.Current - 1) * query.PageSize, true
}
