-- KEYS: gate, stock, begin, end, purchased-users, reservation, accepted-map, stream.
-- ARGV: voucherId, userId, orderId, nowMs, fallbackBeginMs, fallbackEndMs, expectedVersion.
-- Return {legacyCode, exactStringID}; IDs never pass through a Lua number.
local function decimal(value, maximum)
  if type(value) ~= 'string' or not string.match(value, '^[1-9]%d*$') then return false end
  if #value > #maximum or (#value == #maximum and value > maximum) then return false end
  return true
end
local function integer(value, minimum, maximum)
  if type(value) ~= 'string' or not string.match(value, '^%d+$') or (#value > 1 and string.sub(value, 1, 1) == '0') then return nil end
  local number = tonumber(value)
  if not number or number < minimum or number > maximum or number ~= math.floor(number) then return nil end
  return number
end
if #KEYS ~= 8 or #ARGV ~= 7 then return {6, ''} end
for i = 1, 8 do
  for j = i + 1, 8 do if KEYS[i] == KEYS[j] then return {6, ''} end end
end
if not decimal(ARGV[1], '18446744073709551615') or not decimal(ARGV[2], '18446744073709551615')
  or not decimal(ARGV[3], '9223372036854775807') then return {6, ''} end
local now = integer(ARGV[4], 1, 9007199254740991)
local fallbackBegin = integer(ARGV[5], 1, 9007199254740991)
local fallbackEnd = integer(ARGV[6], 1, 9007199254740991)
local target = integer(ARGV[7], 1, 2147483647)
if not now or not fallbackBegin or not fallbackEnd or fallbackBegin >= fallbackEnd or not target then return {6, ''} end
local expected = {'hash', 'string', 'string', 'string', 'set', 'hash', 'hash', 'stream'}
local types = {}
for i = 1, 8 do
  types[i] = redis.call('TYPE', KEYS[i]).ok
  if types[i] ~= 'none' and types[i] ~= expected[i] then return {6, ''} end
end
-- Recovering an already accepted order consumes no inventory or stream entry.
-- This is intentionally before gate/sales checks so a lost response stays
-- recoverable after a concurrent offline or the end of the sales window.
if types[7] == 'hash' then
  local accepted = redis.call('HGET', KEYS[7], ARGV[2])
  if accepted then
    if not decimal(accepted, '9223372036854775807') then return {6, ''} end
    return {8, accepted}
  end
end
if types[1] ~= 'hash' then return {7, ''} end
if redis.call('HGET', KEYS[1], 'state') ~= 'OPEN' or redis.call('HGET', KEYS[1], 'phase') ~= 'OPEN' then return {7, ''} end
local current = integer(redis.call('HGET', KEYS[1], 'version'), 1, 2147483647)
if not current then return {6, ''} end
if current ~= target then return {7, ''} end
if types[2] ~= 'string' or types[3] ~= 'string' or types[4] ~= 'string' then return {3, ''} end
local stock = integer(redis.call('GET', KEYS[2]), 0, 2147483647)
local begin = integer(redis.call('GET', KEYS[3]), 1, 9007199254740991)
local ending = integer(redis.call('GET', KEYS[4]), 1, 9007199254740991)
if not stock or not begin or not ending or begin >= ending then return {6, ''} end
-- The legacy fallback arguments are retained for wire compatibility, but a
-- missing/mismatched prepared projection must never open a sale.
if begin ~= fallbackBegin or ending ~= fallbackEnd then return {6, ''} end
if now < begin then return {4, ''} end
if now >= ending then return {5, ''} end
if types[5] == 'set' and redis.call('SISMEMBER', KEYS[5], ARGV[2]) == 1 then return {2, ''} end
if stock == 0 then return {1, ''} end
-- Candidate ID collisions are explicit failures, never HSET overwrites.
if types[6] ~= 'none' then return {6, ''} end
if types[8] == 'stream' then
  local info = redis.call('XINFO', 'STREAM', KEYS[8])
  for i = 1, #info, 2 do
    if info[i] == 'last-generated-id' and info[i+1] == '18446744073709551615-18446744073709551615' then return {6, ''} end
  end
end
-- Every foreseeable key, argument and counter error was checked before writes.
redis.call('INCRBY', KEYS[2], -1)
redis.call('SADD', KEYS[5], ARGV[2])
redis.call('HSET', KEYS[6], 'userId', ARGV[2], 'voucherId', ARGV[1], 'state', 'PROCESSING', 'createdAt', ARGV[4], 'retries', '0')
redis.call('EXPIRE', KEYS[6], 86400)
redis.call('HSET', KEYS[7], ARGV[2], ARGV[3])
redis.call('EXPIRE', KEYS[7], 604800)
redis.call('XADD', KEYS[8], '*', 'userId', ARGV[2], 'voucherId', ARGV[1], 'id', ARGV[3])
return {0, ARGV[3]}
