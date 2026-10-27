-- Read readiness atomically; a missing/invalid projection is never ready.
if #KEYS ~= 4 or #ARGV ~= 1 then return -2 end
for i = 1, 4 do
  for j = i + 1, 4 do if KEYS[i] == KEYS[j] then return -2 end end
end
for i = 1, 4 do
  local expected = i == 1 and 'hash' or 'string'
  if redis.call('TYPE', KEYS[i]).ok ~= expected then return 0 end
end
if redis.call('HGET', KEYS[1], 'state') ~= 'OPEN'
  or redis.call('HGET', KEYS[1], 'phase') ~= 'OPEN'
  or redis.call('HGET', KEYS[1], 'version') ~= ARGV[1] then return 0 end
local function integer(value, maximum)
  if not value or not string.match(value, '^%d+$') then return nil end
  local number = tonumber(value)
  if not number or number > maximum or number ~= math.floor(number) then return nil end
  return number
end
local stock = integer(redis.call('GET', KEYS[2]), 2147483647)
local begin = integer(redis.call('GET', KEYS[3]), 9007199254740991)
local ending = integer(redis.call('GET', KEYS[4]), 9007199254740991)
if not stock or not begin or not ending or begin < 1 or begin >= ending then return 0 end
return 1
