-- Close the gate synchronously during offline; an equal version is idempotent.
-- KEYS: gate  ARGV: version
local gateType = redis.call('TYPE', KEYS[1]).ok
if gateType ~= 'none' and gateType ~= 'hash' then return -2 end
local current = tonumber(redis.call('HGET', KEYS[1], 'version')) or -1
if current > tonumber(ARGV[1]) then return 0 end
redis.call('HSET', KEYS[1], 'state', 'CLOSED', 'version', ARGV[1])
return 1
