-- Explicit rebuild after a Redis loss. The caller has already closed admission at
-- version N+1 and proved that no order message, review row or compensation is
-- pending. Old ACTIVITY_OPEN events carry a lower version and are rejected by
-- open.lua, so this script may overwrite the stock at N+2.
-- KEYS: gate, stock, begin, end. ARGV: target version, stock, beginMs, endMs.
-- Returns 1 rebuilt, -2 needs reconciliation (bad argument, type or version).
local function integer(value, minimum, maximum)
  if type(value) ~= 'string' or not string.match(value, '^%d+$') then return nil end
  local number = tonumber(value)
  if not number or number < minimum or number > maximum or number ~= math.floor(number) then return nil end
  return number
end
if #KEYS ~= 4 or #ARGV ~= 4 then return -2 end
for i = 1, 4 do
  for j = i + 1, 4 do if KEYS[i] == KEYS[j] then return -2 end end
end
local target = integer(ARGV[1], 1, 2147483647)
local stock = integer(ARGV[2], 0, 2147483647)
local begin = integer(ARGV[3], 1, 9007199254740991)
local ending = integer(ARGV[4], 1, 9007199254740991)
if not target or not stock or not begin or not ending or begin >= ending then return -2 end
local gateType = redis.call('TYPE', KEYS[1]).ok
if gateType ~= 'none' and gateType ~= 'hash' then return -2 end
if gateType == 'hash' then
  local current = integer(redis.call('HGET', KEYS[1], 'version'), 1, 2147483647)
  -- The gate was closed at N+1 before this call; a version that is already at or
  -- beyond the target means the rebuild was attempted twice or went backwards.
  if current and current >= target then return -2 end
end
for i = 2, 4 do
  local kind = redis.call('TYPE', KEYS[i]).ok
  if kind ~= 'none' and kind ~= 'string' then return -2 end
end
redis.call('SET', KEYS[2], ARGV[2])
redis.call('SET', KEYS[3], ARGV[3])
redis.call('SET', KEYS[4], ARGV[4])
redis.call('HSET', KEYS[1], 'state', 'CLOSED', 'version', ARGV[1], 'phase', 'PREPARED')
return 1
