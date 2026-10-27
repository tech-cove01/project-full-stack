-- KEYS: gate, stock, begin, end. ARGV: version, initialStock, beginMs, endMs.
-- Never rebuild or overwrite existing inventory. All checks precede writes.
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
local types = {}
for i = 1, 4 do
  types[i] = redis.call('TYPE', KEYS[i]).ok
  local expected = i == 1 and 'hash' or 'string'
  if types[i] ~= 'none' and types[i] ~= expected then return -2 end
end
if types[1] == 'none' then
  if types[2] ~= 'none' or types[3] ~= 'none' or types[4] ~= 'none' then return -2 end
  redis.call('SET', KEYS[2], ARGV[2])
  redis.call('SET', KEYS[3], ARGV[3])
  redis.call('SET', KEYS[4], ARGV[4])
  redis.call('HSET', KEYS[1], 'state', 'CLOSED', 'version', ARGV[1], 'phase', 'PREPARED')
  return 1
end
local current = integer(redis.call('HGET', KEYS[1], 'version'), 1, 2147483647)
local phase = redis.call('HGET', KEYS[1], 'phase')
local state = redis.call('HGET', KEYS[1], 'state')
if not current or (phase ~= 'PREPARED' and phase ~= 'OPEN' and phase ~= 'OFFLINE') then return -2 end
if (phase == 'OPEN' and state ~= 'OPEN') or (phase ~= 'OPEN' and state ~= 'CLOSED') then return -2 end
if current > target then return -1 end
if current ~= target or phase == 'OFFLINE' then return -2 end
if types[2] ~= 'string' or types[3] ~= 'string' or types[4] ~= 'string' then return -2 end
local remaining = integer(redis.call('GET', KEYS[2]), 0, 2147483647)
if not remaining or remaining > stock then return -2 end
if redis.call('GET', KEYS[3]) ~= ARGV[3] or redis.call('GET', KEYS[4]) ~= ARGV[4] then return -2 end
return 0
