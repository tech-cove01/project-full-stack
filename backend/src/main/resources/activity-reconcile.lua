-- Explicit reconcile after Redis loss. The caller must prove no Stream reservation is in flight.
-- KEYS: gate, stock, begin, end
-- ARGV: targetVersion, dbStock, beginMs, endMs
-- Returns: 1 rebuilt, -2 unsafe state (wrong type, newer gate, etc.).
local gateKey, stockKey = KEYS[1], KEYS[2]
local target = tonumber(ARGV[1])
if not target or target < 1 then return -2 end

local gateType = redis.call('TYPE', gateKey).ok
if gateType ~= 'none' and gateType ~= 'hash' then return -2 end
local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'none' and stockType ~= 'string' then return -2 end
if gateType == 'hash' then
    local current = tonumber(redis.call('HGET', gateKey, 'version')) or -1
    if current > target then return -2 end
end

-- No reservations are in flight by contract; DB stock is the only safe snapshot.
redis.call('SET', stockKey, ARGV[2])
redis.call('SET', KEYS[3], ARGV[3])
redis.call('SET', KEYS[4], ARGV[4])
redis.call('HSET', gateKey, 'state', 'CLOSED', 'version', ARGV[1])
return 1
