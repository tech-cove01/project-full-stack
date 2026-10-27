-- Prepare a published activity's Redis state, then leave the gate CLOSED.
-- KEYS: gate, stock, begin, end
-- ARGV: targetVersion, stock, beginMs, endMs, mode (optional: "force" for explicit reconciliation)
-- Returns: 1 prepared, 0 already prepared, -2 needs reconciliation.
local gateKey, stockKey = KEYS[1], KEYS[2]
local gateType = redis.call('TYPE', gateKey).ok
if gateType ~= 'none' and gateType ~= 'hash' then return -2 end
local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'none' and stockType ~= 'string' then return -2 end
local target = tonumber(ARGV[1])
if not target or target < 1 then return -2 end
local force = ARGV[5] == 'force'
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
-- A gate without a version is only the startup guard marker. Any other existing gate means the
-- activity was prepared before, so rebuilding stock is refused unless reconciliation forces it.
if redis.call('EXISTS', gateKey) == 1 and not force then
  if redis.call('HEXISTS', gateKey, 'version') == 0 then
    redis.call('SET', stockKey, ARGV[2])
    redis.call('SET', KEYS[3], ARGV[3])
    redis.call('SET', KEYS[4], ARGV[4])
    redis.call('HSET', gateKey, 'state', 'CLOSED', 'version', ARGV[1])
    return 1
  end
  return -2
end
redis.call('SET', stockKey, ARGV[2])
redis.call('SET', KEYS[3], ARGV[3])
redis.call('SET', KEYS[4], ARGV[4])
redis.call('HSET', gateKey, 'state', 'CLOSED', 'version', ARGV[1])
return 1
