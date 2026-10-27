// Package activity owns merchant activity creation and its one-way lifecycle.
// Redis publication effects are delivered separately from committed DB facts.
package activity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/activitypub"
)

var (
	ErrForbidden = errors.New("无权管理该门店活动")
	ErrNotFound  = errors.New("活动不存在")
	ErrConflict  = errors.New("相同请求键对应不同发布内容")
	ErrOffline   = errors.New("活动已下架")
	ErrUnsafe    = errors.New("活动发布状态需要核对，请稍后重试")
	// ErrNotActive rejects a rebuild of anything that is not an ACTIVE activity.
	ErrNotActive = errors.New("只有 ACTIVE 活动需要重建 Redis 状态")
	keyPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	businessZone = mustBusinessZone()
)

// blockedError lists the in-flight work that prevents a rebuild. The gate stays
// closed, so refusing here is the safe outcome rather than a failed operation.
type blockedError struct{ reason string }

func (e blockedError) Error() string { return "仍有未处理项，准入保持关闭：" + e.reason }

func mustBusinessZone() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return zone
}

type validationError struct{ message string }

func (e validationError) Error() string { return e.message }
func invalid(message string) error      { return validationError{message} }

// CreateRequest is the complete write whitelist. Identity and ownership are
// resolved from the authenticated user and locked DB rows, never this payload.
type CreateRequest struct {
	ShopID          string  `json:"shopId"`
	Title           string  `json:"title"`
	SubTitle        *string `json:"subTitle"`
	Rules           *string `json:"rules"`
	PayValue        string  `json:"payValue"`
	ActualValue     string  `json:"actualValue"`
	TotalStock      int     `json:"totalStock"`
	BeginTime       string  `json:"beginTime"`
	EndTime         string  `json:"endTime"`
	RedeemBeginTime string  `json:"redeemBeginTime"`
	RedeemEndTime   string  `json:"redeemEndTime"`
}

type View struct {
	ID              string  `json:"id"`
	ShopID          string  `json:"shopId"`
	ShopName        string  `json:"shopName"`
	Title           string  `json:"title"`
	SubTitle        *string `json:"subTitle,omitempty"`
	PayValue        string  `json:"payValue"`
	ActualValue     string  `json:"actualValue"`
	TotalStock      int     `json:"totalStock"`
	Stock           int     `json:"stock"`
	PublishStatus   string  `json:"publishStatus"`
	PublishVersion  int     `json:"publishVersion"`
	Ready           bool    `json:"ready"`
	BeginTime       string  `json:"beginTime"`
	EndTime         string  `json:"endTime"`
	RedeemBeginTime *string `json:"redeemBeginTime,omitempty"`
	RedeemEndTime   *string `json:"redeemEndTime,omitempty"`
}

type Query struct {
	Current  uint64
	PageSize uint64
	ShopID   string
	Status   string
}

type gate interface {
	Close(context.Context, string, int) error
	Reconcile(context.Context, string, int, int, time.Time, time.Time) error
	Ready(context.Context, string, int) (bool, error)
}

type Service struct {
	db    *gorm.DB
	gate  gate
	redis *redis.Client
}

func NewService(db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{db: db, gate: activitypub.NewGate(rdb), redis: rdb}
}

type validated struct {
	request                            CreateRequest
	shopID                             uint64
	payValue, actualValue              int64
	begin, end, redeemBegin, redeemEnd time.Time
	hash                               string
}

func decimal(id uint64) string { return strconv.FormatUint(id, 10) }

func positiveID(value string) (uint64, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || decimal(id) != value {
		return 0, invalid("ID必须为正十进制整数字符串")
	}
	return id, nil
}

func positiveMoney(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return 0, invalid("金额必须为不超过64位有符号上限的正整数字符串")
	}
	return n, nil
}

func text(value string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > max || (required && value == "") {
		return "", invalid("标题或规则为空、过长或格式错误")
	}
	return value, nil
}

func optionalText(value *string, max int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	clean, err := text(*value, max, false)
	if err != nil {
		return nil, err
	}
	if clean == "" {
		return nil, nil
	}
	return &clean, nil
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Nanosecond() != 0 {
		return time.Time{}, invalid("时间必须包含明确时区偏移，且精确到秒")
	}
	local := parsed.In(businessZone)
	if local.Year() < 1000 || local.Year() > 9999 {
		return time.Time{}, invalid("时间超出数据库支持范围")
	}
	return local, nil
}

func validate(key string, request CreateRequest) (validated, error) {
	var v validated
	if !keyPattern.MatchString(key) {
		return v, invalid("Idempotency-Key 缺失或格式错误")
	}
	var err error
	if v.shopID, err = positiveID(request.ShopID); err != nil {
		return v, err
	}
	if request.Title, err = text(request.Title, 255, true); err != nil {
		return v, err
	}
	if request.SubTitle, err = optionalText(request.SubTitle, 255); err != nil {
		return v, err
	}
	if request.Rules, err = optionalText(request.Rules, 1024); err != nil {
		return v, err
	}
	if v.payValue, err = positiveMoney(request.PayValue); err != nil {
		return v, err
	}
	if v.actualValue, err = positiveMoney(request.ActualValue); err != nil {
		return v, err
	}
	if v.payValue > v.actualValue {
		return v, invalid("购买价格不能高于抵扣面值")
	}
	if request.TotalStock < 1 || request.TotalStock > 1000000 {
		return v, invalid("库存必须为1到1000000的整数")
	}
	if v.begin, err = parseTime(request.BeginTime); err != nil {
		return v, err
	}
	if v.end, err = parseTime(request.EndTime); err != nil {
		return v, err
	}
	if v.redeemBegin, err = parseTime(request.RedeemBeginTime); err != nil {
		return v, err
	}
	if v.redeemEnd, err = parseTime(request.RedeemEndTime); err != nil {
		return v, err
	}
	if v.begin.UnixMilli() < 1 || v.end.UnixMilli() > 9007199254740991 {
		return v, invalid("销售时间超出活动准入支持范围")
	}
	if !v.begin.Before(v.end) || !v.redeemBegin.Before(v.redeemEnd) || !v.redeemEnd.After(v.end) {
		return v, invalid("销售或核销时间顺序错误，核销结束必须晚于销售结束")
	}
	request.BeginTime = v.begin.Format(time.RFC3339)
	request.EndTime = v.end.Format(time.RFC3339)
	request.RedeemBeginTime = v.redeemBegin.Format(time.RFC3339)
	request.RedeemEndTime = v.redeemEnd.Format(time.RFC3339)
	v.request = request
	// JSON length/escaping prevents the delimiter ambiguity in the Java hash.
	// Java-era request hashes are deliberately not treated as matching this
	// encoding; adoption requires an explicit GO-071 compatibility decision.
	canonical, err := json.Marshal(request)
	if err != nil {
		return v, err
	}
	digest := sha256.Sum256(canonical)
	v.hash = hex.EncodeToString(digest[:])
	return v, nil
}

func validateQuery(q Query) error {
	if q.Current == 0 || q.PageSize == 0 || q.PageSize > 50 {
		return invalid("页码必须为正整数，pageSize必须在1到50之间")
	}
	if q.ShopID != "" {
		if _, err := positiveID(q.ShopID); err != nil {
			return err
		}
	}
	if q.Status != "" && q.Status != "INITIALIZING" && q.Status != "ACTIVE" && q.Status != "OFFLINE" {
		return invalid("未知活动状态")
	}
	return nil
}

func offset(q Query, total uint64) (uint64, bool) {
	if q.Current == 0 || q.PageSize == 0 || total == 0 || q.Current-1 > (total-1)/q.PageSize {
		return 0, false
	}
	return (q.Current - 1) * q.PageSize, true
}

func nextVersion(version int) (int, error) {
	if version < 0 || version >= math.MaxInt32 {
		return 0, ErrUnsafe
	}
	return version + 1, nil
}
