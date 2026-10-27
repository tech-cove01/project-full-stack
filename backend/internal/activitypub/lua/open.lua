-- KEYS: gate, stock, begin, end. ARGV: exact committed ACTIVE version.
local function integer(value, minimum, maximum)
  if type(value) ~= 'string' or not string.match(value, '^%d+$') then return nil end
  local number = tonumber(value)
  if not number or number < minimum or number > maximum or number ~= math.floor(number) then return nil end
  return number
end
if #KEYS ~= 4 or #ARGV ~= 1 then return -2 end
for i = 1, 4 do
  for j = i + 1, 4 do if KEYS[i] == KEYS[j] then return -2 end end
end
local target = integer(ARGV[1], 1, 2147483647)
if not target then return -2 end
for i = 1, 4 do
  local expected = i == 1 and 'hash' or 'string'
  if redis.call('TYPE', KEYS[i]).ok ~= expected then return -2 end
end
local current = integer(redis.call('HGET', KEYS[1], 'version'), 1, 2147483647)
if not current then return -2 end
if current ~= target then return -1 end
local phase = redis.call('HGET', KEYS[1], 'phase')
local state = redis.call('HGET', KEYS[1], 'state')
if phase == 'OFFLINE' then return -1 end
if (phase ~= 'PREPARED' or state ~= 'CLOSED') and (phase ~= 'OPEN' or state ~= 'OPEN') then return -2 end
local stock = integer(redis.call('GET', KEYS[2]), 0, 2147483647)
local begin = integer(redis.call('GET', KEYS[3]), 1, 9007199254740991)
local ending = integer(redis.call('GET', KEYS[4]), 1, 9007199254740991)
if not stock or not begin or not ending or begin >= ending then return -2 end
if phase == 'OPEN' then return 0 end
redis.call('HSET', KEYS[1], 'state', 'OPEN', 'phase', 'OPEN')
return 1
