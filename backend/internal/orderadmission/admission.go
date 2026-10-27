// Package orderadmission owns Redis admission, stable accepted IDs, and the
// legacy-compatible opaque order ID allocation. It never creates a DB order.
package orderadmission

import (
	"context"
	_ "embed"
	"errors"
	"math"
	"strconv"
	"time"
	_ "time/tzdata"

	"github.com/redis/go-redis/v9"
)

var (
	ErrSoldOut          = errors.New("库存不足")
	ErrAlreadyPurchased = errors.New("该活动已有有效订单")
	ErrNotStarted       = errors.New("活动尚未开始")
	ErrEnded            = errors.New("活动已经结束")
	ErrClosed           = errors.New("活动尚未就绪或已下架")
	ErrUnsafe           = errors.New("活动预约状态需要核对")
)

//go:embed lua/reserve.lua
var reserveSource string

//go:embed lua/recover.lua
var recoverSource string

//go:embed lua/next-id.lua
var nextIDSource string

var reserveScript = redis.NewScript(reserveSource)
var recoverScript = redis.NewScript(recoverSource)
var nextIDScript = redis.NewScript(nextIDSource)

type Result struct {
	ID     string
	Reused bool
}

type Admission struct {
	redis         *redis.Client
	stream        string
	counterPrefix string
}

func New(client *redis.Client) *Admission {
	return &Admission{redis: client, stream: "stream.orders", counterPrefix: "icr:order:"}
}

var businessZone = func() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return zone
}()

// NextID preserves the Java Shanghai wall-clock high bits and daily counter
// namespace. The ID is opaque; its high bits are not a UTC event timestamp.
// Redis counter loss requires reconciliation before resuming allocation. This
// function deliberately never resets an invalid or exhausted existing counter.
func (admission *Admission) NextID(ctx context.Context, now time.Time) (string, error) {
	local := now.In(businessZone)
	wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
	seconds := wall.Unix() - 1640995200
	if seconds < 0 || seconds > math.MaxInt32 {
		return "", ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	sequence, err := nextIDScript.Run(ctx, admission.redis, []string{admission.counterPrefix + local.Format("2006:01:02")}).Int64()
	if err != nil {
		return "", err
	}
	if sequence < 1 || sequence > math.MaxUint32 {
		return "", ErrUnsafe
	}
	id := uint64(seconds)<<32 | uint64(sequence)
	if id == 0 || id > math.MaxInt64 {
		return "", ErrUnsafe
	}
	return strconv.FormatUint(id, 10), nil
}

func (admission *Admission) Recover(ctx context.Context, voucherID, userID string) (string, error) {
	if !unsignedID(voucherID) || !unsignedID(userID) {
		return "", ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	response, err := recoverScript.Run(ctx, admission.redis, []string{"seckill:accepted:" + voucherID}, userID).Slice()
	if err != nil {
		return "", err
	}
	code, id, err := decode(response)
	if err != nil {
		return "", err
	}
	switch code {
	case 0:
		if id != "" {
			return "", ErrUnsafe
		}
		return "", nil
	case 8:
		if !orderID(id) {
			return "", ErrUnsafe
		}
		return id, nil
	default:
		return "", ErrUnsafe
	}
}

// Reserve retains now as a validated legacy wire argument. Redis execution time
// decides new admission and createdAt, so earlier SQL/Redis delays cannot extend
// the sales window. Existing accepted identities remain recoverable afterward.
func (admission *Admission) Reserve(ctx context.Context, voucherID, userID, candidateID string, now, begin, end time.Time, version int) (Result, error) {
	if !unsignedID(voucherID) || !unsignedID(userID) || !orderID(candidateID) ||
		version < 1 || version > math.MaxInt32 || !safeMillis(now) || !safeMillis(begin) || !safeMillis(end) || !begin.Before(end) {
		return Result{}, ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	response, err := reserveScript.Run(ctx, admission.redis, admission.keys(voucherID, candidateID),
		voucherID, userID, candidateID, now.UnixMilli(), begin.UnixMilli(), end.UnixMilli(), version).Slice()
	if err != nil {
		return Result{}, err
	}
	code, id, err := decode(response)
	if err != nil {
		return Result{}, err
	}
	switch code {
	case 0:
		if id != candidateID {
			return Result{}, ErrUnsafe
		}
		return Result{ID: id}, nil
	case 8:
		if !orderID(id) {
			return Result{}, ErrUnsafe
		}
		return Result{ID: id, Reused: true}, nil
	case 1:
		return Result{}, ErrSoldOut
	case 2:
		return Result{}, ErrAlreadyPurchased
	case 4:
		return Result{}, ErrNotStarted
	case 5:
		return Result{}, ErrEnded
	case 7:
		return Result{}, ErrClosed
	default:
		return Result{}, ErrUnsafe
	}
}

func (admission *Admission) keys(voucherID, candidateID string) []string {
	return []string{"seckill:gate:" + voucherID, "seckill:stock:" + voucherID,
		"seckill:begin:" + voucherID, "seckill:end:" + voucherID,
		"seckill:order:" + voucherID, "order:reservation:" + candidateID,
		"seckill:accepted:" + voucherID, admission.stream}
}

func unsignedID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func orderID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func safeMillis(value time.Time) bool {
	return value.UnixMilli() >= 1 && value.UnixMilli() <= 9007199254740991
}

func decode(response []any) (int64, string, error) {
	if len(response) != 2 {
		return 0, "", ErrUnsafe
	}
	code, numeric := response[0].(int64)
	id, textual := response[1].(string)
	if !numeric || !textual {
		return 0, "", ErrUnsafe
	}
	return code, id, nil
}
