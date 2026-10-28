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
local values={}
for i=1,2 do
 local kind=redis.call('TYPE',KEYS[i]).ok
 if kind~='none' and kind~='string' then return redis.error_reply('invalid AI budget type') end
 local raw=redis.call('GET',KEYS[i])
 if raw and (not string.match(raw,'^%d+$') or #raw>9) then return redis.error_reply('invalid AI budget value') end
 values[i]=tonumber(raw or '0')
end
if values[1]>=minuteLimit then return -1 end
if values[2]>=dailyLimit then return -2 end
local rate=redis.call('INCR',rateKey)
if rate==1 then redis.call('EXPIRE',rateKey,60) end
local daily=redis.call('INCR',dailyKey)
if daily==1 then redis.call('EXPIRE',dailyKey,172800) end
return 1
`)

var refundBudgetScript = redis.NewScript(`
for i=1,2 do
 if redis.call('TYPE',KEYS[i]).ok=='string' then
  local raw=redis.call('GET',KEYS[i]);local value=tonumber(raw)
  if value and value>0 and value==math.floor(value) then redis.call('DECR',KEYS[i]) end
 end
end
return 1`)

// Allow counts one request. A nil budget allows everything, which keeps local
// tests free of Redis while production always attaches one.
func (budget *Budget) Allow(ctx context.Context, userID uint64) error {
	_, err := budget.Reserve(ctx, userID)
	return err
}

// Reserve returns an exact-key release for a failed logical turn. Replays do
// not call it; success retains the count. This is not a provider billing claim.
func (budget *Budget) Reserve(ctx context.Context, userID uint64) (func(), error) {
	if budget == nil || budget.redis == nil {
		return func() {}, nil
	}
	minute := time.Now().Format("200601021504")
	day := time.Now().In(zoneForBudget()).Format("20060102")
	user := strconv.FormatUint(userID, 10)
	keys := []string{"ai:rate:" + user + ":" + minute, "ai:budget:" + user + ":" + day}
	result, err := budgetScript.Run(ctx, budget.redis,
		keys,
		budget.perMinute, budget.daily).Int()
	if err != nil {
		return nil, err
	}
	switch result {
	case -1:
		return nil, ErrRateLimited
	case -2:
		return nil, ErrBudgetExhausted
	default:
		return func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = refundBudgetScript.Run(cleanup, budget.redis, keys).Err()
		}, nil
	}
}

func zoneForBudget() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.UTC
	}
	return location
}
