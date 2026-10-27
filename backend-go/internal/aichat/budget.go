package aichat

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	// ErrRateLimited rejects a burst from one account.
	ErrRateLimited = errors.New("请求过于频繁，请稍后再试")
	// ErrBudgetExhausted rejects the rest of the day once the cap is reached.
	ErrBudgetExhausted = errors.New("今日 AI 调用预算已用完")
)

const (
	defaultPerMinute = 20
	defaultDaily     = 200
)

// Budget bounds how often one account can reach the provider. Both counters are
// incremented in one script so a burst cannot slip between the two checks.
type Budget struct {
	redis     *redis.Client
	perMinute int
	daily     int
}

func NewBudget(client *redis.Client) *Budget {
	return &Budget{redis: client, perMinute: defaultPerMinute, daily: defaultDaily}
}

func NewBudgetWithLimits(client *redis.Client, perMinute, daily int) *Budget {
	if perMinute <= 0 {
		perMinute = defaultPerMinute
	}
	if daily <= 0 {
		daily = defaultDaily
	}
	return &Budget{redis: client, perMinute: perMinute, daily: daily}
}

var budgetScript = redis.NewScript(`
local rateKey, dailyKey = KEYS[1], KEYS[2]
local minuteLimit, dailyLimit = tonumber(ARGV[1]), tonumber(ARGV[2])
local rate = redis.call('INCR', rateKey)
if rate == 1 then redis.call('EXPIRE', rateKey, 60) end
if rate > minuteLimit then return -1 end
local daily = redis.call('INCR', dailyKey)
if daily == 1 then redis.call('EXPIRE', dailyKey, 172800) end
if daily > dailyLimit then return -2 end
return 1
`)

// Allow counts one request. A nil budget allows everything, which keeps local
// tests free of Redis while production always attaches one.
func (budget *Budget) Allow(ctx context.Context, userID uint64) error {
	if budget == nil || budget.redis == nil {
		return nil
	}
	minute := time.Now().Format("200601021504")
	day := time.Now().In(zoneForBudget()).Format("20060102")
	user := strconv.FormatUint(userID, 10)
	result, err := budgetScript.Run(ctx, budget.redis,
		[]string{"ai:rate:" + user + ":" + minute, "ai:budget:" + user + ":" + day},
		budget.perMinute, budget.daily).Int()
	if err != nil {
		return err
	}
	switch result {
	case -1:
		return ErrRateLimited
	case -2:
		return ErrBudgetExhausted
	default:
		return nil
	}
}

func zoneForBudget() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return location
}
