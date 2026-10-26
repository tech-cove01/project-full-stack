-- KEYS: gate. ARGV: offline version. Closing never requires intact inventory.
local function version(value)
  if type(value) ~= 'string' or not string.match(value, '^%d+$') then return nil end
  local number = tonumber(value)
  if not number or number < 1 or number > 2147483647 or number ~= math.floor(number) then return nil end
  return number
end
if #KEYS ~= 1 or #ARGV ~= 1 then return -2 end
local target = version(ARGV[1])
if not target then return -2 end
local keyType = redis.call('TYPE', KEYS[1]).ok
if keyType ~= 'none' and keyType ~= 'hash' then return -2 end
if keyType == 'hash' then
  local current = version(redis.call('HGET', KEYS[1], 'version'))
  if not current then return -2 end
  if current > target then return -1 end
  local phase = redis.call('HGET', KEYS[1], 'phase')
  local state = redis.call('HGET', KEYS[1], 'state')
  if current == target and phase == 'OFFLINE' and state == 'CLOSED' then return 0 end
  -- A legacy hash may safely become CLOSED at a higher version. Missing phase
  -- is never sufficient to prepare or open an old gate.
  if phase and phase ~= 'PREPARED' and phase ~= 'OPEN' and phase ~= 'OFFLINE' then return -2 end
  if state ~= 'OPEN' and state ~= 'CLOSED' then return -2 end
end
redis.call('HSET', KEYS[1], 'state', 'CLOSED', 'version', ARGV[1], 'phase', 'OFFLINE')
return 1
