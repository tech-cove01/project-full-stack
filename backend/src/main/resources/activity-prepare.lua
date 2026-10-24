-- Prepare a published activity's Redis state, then leave the gate CLOSED.
-- KEYS: gate, stock, begin, end
-- ARGV: targetVersion, stock, beginMs, endMs
-- Returns: 1 prepared, 0 already prepared, -2 needs reconciliation.
local gateKey, stockKey = KEYS[1], KEYS[2]
local gateType = redis.call('TYPE', gateKey).ok
if gateType ~= 'none' and gateType ~= 'hash' then return -2 end
local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'none' and stockType ~= 'string' then return -2 end
local target = tonumber(ARGV[1])
if not target or target < 1 then return -2 end
local current = -1
if gateType == 'hash' then current = tonumber(redis.call('HGET', gateKey, 'version')) or -1 end
if current >= target then
  if stockType ~= 'string' then return -2 end
  return 0
end
if stockType == 'string' then
  -- Never overwrite a stock key that may already contain reservations.
  redis.call('HSET', gateKey, 'state', 'CLOSED', 'version', ARGV[1])
  return 1
end
if redis.call('EXISTS', gateKey) == 1 then return -2 end
redis.call('SET', stockKey, ARGV[2])
redis.call('SET', KEYS[3], ARGV[3])
redis.call('SET', KEYS[4], ARGV[4])
redis.call('HSET', gateKey, 'state', 'CLOSED', 'version', ARGV[1])
return 1
