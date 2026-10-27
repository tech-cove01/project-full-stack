// Package merchantorder owns merchant-side order reads: the shop-scoped order list
// and the management audit trail. Mutations (redeem/refund) live in their own files
// so each one can be reviewed and reverted on its own.
package merchantorder

import (
	"errors"
	"strconv"
	"time"
	_ "time/tzdata"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/merchant"
)

var (
	ErrInvalidQuery = errors.New("查询参数不合法")
	ErrForbidden    = errors.New("无权限操作该数据")
	ErrUnsafe       = errors.New("数据状态需要核对，请稍后重试")
	// ErrConflict covers a state that no longer allows the requested action.
	ErrConflict = errors.New("订单当前状态或期限不允许此操作")
)

// View is the merchant-facing order row. The phone is always masked: a merchant
// list must not become a way to read a customer's full number.
type View struct {
	ID            string  `json:"id"`
	ShopID        string  `json:"shopId"`
	ShopName      string  `json:"shopName"`
	UserID        string  `json:"userId"`
	Phone         string  `json:"phone"`
	VoucherID     string  `json:"voucherId"`
	VoucherTitle  string  `json:"voucherTitle"`
	Status        int     `json:"status"`
	PayAmount     string  `json:"payAmount"`
	CreateTime    *string `json:"createTime,omitempty"`
	PayTime       *string `json:"payTime,omitempty"`
	UseTime       *string `json:"useTime,omitempty"`
	RefundTime    *string `json:"refundTime,omitempty"`
	RedeemEndTime *string `json:"redeemEndTime,omitempty"`
}

// AuditView mirrors the audit columns the management scope may read. Detail is a
// JSON string; it never contains a redeem token.
type AuditView struct {
	ID             string  `json:"id"`
	ShopID         *string `json:"shopId,omitempty"`
	OperatorUserID string  `json:"operatorUserId"`
	OperatorType   string  `json:"operatorType"`
	Action         string  `json:"action"`
	ResourceID     *string `json:"resourceId,omitempty"`
	Detail         *string `json:"detail,omitempty"`
	CreateTime     string  `json:"createTime"`
}

// Query carries only filters the server validates; unknown values are errors
// rather than silently ignored.
type Query struct {
	Current  uint64
	PageSize uint64
	ShopID   string
	Status   string
	OrderID  string
	Action   string
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func (service *Service) access() *merchant.Access { return merchant.NewAccess(service.db) }

func businessZone() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return zone
}

var zone = businessZone()

func decimal(id uint64) string { return strconv.FormatUint(id, 10) }

func positive(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value {
		return 0, ErrInvalidQuery
	}
	return parsed, nil
}

func formattedTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	text := value.In(zone).Format("2006-01-02 15:04:05")
	return &text
}

// maskPhone keeps the first three and last four digits, matching the Java
// contract. Anything shorter is withheld rather than echoed back.
func maskPhone(phone string) string {
	if len(phone) < 7 {
		return ""
	}
	return phone[:3] + "****" + phone[len(phone)-4:]
}

func pageOffset(query Query, total uint64) (uint64, bool) {
	if query.Current == 0 || query.PageSize == 0 || total == 0 ||
		query.Current-1 > (total-1)/query.PageSize {
		return 0, false
	}
	return (query.Current - 1) * query.PageSize, true
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	out := "?"
	for i := 1; i < count; i++ {
		out += ",?"
	}
	return out
}

// parseQuery validates pagination and filters before any database work.
func parseQuery(query Query) (Query, error) {
	if query.Current == 0 {
		query.Current = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 10
	}
	if query.PageSize > 50 {
		return Query{}, ErrInvalidQuery
	}
	if query.Status != "" {
		switch query.Status {
		case "1", "2", "3", "4", "6":
		default:
			return Query{}, ErrInvalidQuery
		}
	}
	if query.OrderID != "" {
		if _, err := positive(query.OrderID); err != nil {
			return Query{}, err
		}
	}
	if query.ShopID != "" {
		if _, err := positive(query.ShopID); err != nil {
			return Query{}, err
		}
	}
	return query, nil
}

// Contains reports whether the requested shop is inside the caller's scope.
func contains(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
