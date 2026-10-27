-- Stable accepted identity is a lookup, even after admission has closed.
local function decimal(value, maximum)
  if type(value) ~= 'string' or not string.match(value, '^[1-9]%d*$') then return false end
  if #value > #maximum or (#value == #maximum and value > maximum) then return false end
  return true
end
if #KEYS ~= 1 or #ARGV ~= 1 or not decimal(ARGV[1], '18446744073709551615') then return {6, ''} end
local keyType = redis.call('TYPE', KEYS[1]).ok
if keyType == 'none' then return {0, ''} end
if keyType ~= 'hash' then return {6, ''} end
local id = redis.call('HGET', KEYS[1], ARGV[1])
if not id then return {0, ''} end
if not decimal(id, '9223372036854775807') then return {6, ''} end
return {8, id}
