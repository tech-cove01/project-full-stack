package auth

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	codePrefix     = "login:code:"
	throttlePrefix = "login:code:throttle:"
	sessionPrefix  = "login:token:"
)

// Validate both types before either write: Redis Lua does not roll back writes
// that precede a runtime error. TTLs and argument order retain the Java contract.
var issueCodeScript = redis.NewScript(`
local throttleType = redis.call('TYPE', KEYS[1]).ok
local codeType = redis.call('TYPE', KEYS[2]).ok
if (throttleType ~= 'none' and throttleType ~= 'string') or
   (codeType ~= 'none' and codeType ~= 'string') then
    return redis.error_reply('invalid login code key type')
end
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('SET', KEYS[1], '1', 'EX', ARGV[2])
redis.call('SET', KEYS[2], ARGV[1], 'EX', ARGV[3])
return 1
`)

var consumeCodeScript = redis.NewScript(`
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then return 0 end
if kind ~= 'string' then return redis.error_reply('invalid login code key type') end
if redis.call('PTTL', KEYS[1]) <= 0 then return 0 end
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`)

var createSessionScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], 'id', ARGV[1], 'nickName', ARGV[2], 'icon', ARGV[3])
redis.call('EXPIRE', KEYS[1], ARGV[4])
return 1
`)

var refreshSessionScript = redis.NewScript(`
local kind = redis.call('TYPE', KEYS[1]).ok
if kind == 'none' then return {} end
if kind ~= 'hash' then return redis.error_reply('invalid login session key type') end
if redis.call('PTTL', KEYS[1]) <= 0 then return {} end
local id = redis.call('HGET', KEYS[1], 'id')
local name = redis.call('HGET', KEYS[1], 'nickName')
local icon = redis.call('HGET', KEYS[1], 'icon')
if not id or not name or not icon or not string.match(id, '^[1-9][0-9]*$') or
   string.len(id) > 20 or (string.len(id) == 20 and id > '18446744073709551615') then
    return {}
end
-- Expiry never recreates a deleted hash. Read/refresh and logout are serialized
-- by Redis, so an in-flight refresh cannot resurrect a logged-out session.
redis.call('EXPIRE', KEYS[1], ARGV[1])
return {id, name, icon}
`)

type redisSessions struct{ client redis.UniversalClient }

func (store redisSessions) IssueCode(ctx context.Context, phone, code string) (bool, error) {
	issued, err := issueCodeScript.Run(ctx, store.client,
		[]string{throttlePrefix + phone, codePrefix + phone}, code,
		int64(ThrottleTTL.Seconds()), int64(CodeTTL.Seconds())).Int64()
	return issued == 1, err
}

func (store redisSessions) ConsumeCode(ctx context.Context, phone, code string) (bool, error) {
	consumed, err := consumeCodeScript.Run(ctx, store.client, []string{codePrefix + phone}, code).Int64()
	return consumed == 1, err
}

func (store redisSessions) CreateSession(ctx context.Context, token string, user User) (bool, error) {
	created, err := createSessionScript.Run(ctx, store.client, []string{sessionPrefix + token},
		user.ID, user.NickName, user.Icon, int64(SessionTTL.Seconds())).Int64()
	return created == 1, err
}

func (store redisSessions) RefreshSession(ctx context.Context, token string) (*User, error) {
	fields, err := refreshSessionScript.Run(ctx, store.client,
		[]string{sessionPrefix + token}, int64(SessionTTL.Seconds())).StringSlice()
	if err != nil {
		return nil, err
	}
	if len(fields) != 3 {
		return nil, nil
	}
	id, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != fields[0] {
		return nil, nil
	}
	return &User{ID: fields[0], NickName: fields[1], Icon: fields[2]}, nil
}

func (store redisSessions) DeleteSession(ctx context.Context, token string) error {
	return store.client.Del(ctx, sessionPrefix+token).Err()
}
