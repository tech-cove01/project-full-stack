-- Open only the exact prepared version. A late older event must not reopen a newer close.
-- KEYS: gate  ARGV: version
local gateType = redis.call('TYPE', KEYS[1]).ok
if gateType ~= 'hash' then return -2 end
local current = tonumber(redis.call('HGET', KEYS[1], 'version')) or -1
if current ~= tonumber(ARGV[1]) then return -1 end
if redis.call('HGET', KEYS[1], 'state') == 'OPEN' then return 0 end
redis.call('HSET', KEYS[1], 'state', 'OPEN')
return 1
