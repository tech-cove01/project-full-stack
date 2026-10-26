-- One daily legacy counter. It has no TTL and is never silently repaired.
if #KEYS ~= 1 or #ARGV ~= 0 then return -1 end
local keyType = redis.call('TYPE', KEYS[1]).ok
if keyType ~= 'none' and keyType ~= 'string' then return -1 end
if keyType == 'string' then
  local value = redis.call('GET', KEYS[1])
  if not value or not string.match(value, '^%d+$') or (#value > 1 and string.sub(value, 1, 1) == '0') then return -1 end
  local current = tonumber(value)
  if not current or current < 0 or current >= 4294967295 or current ~= math.floor(current) then return -1 end
  if redis.call('PTTL', KEYS[1]) ~= -1 then return -1 end
end
return redis.call('INCR', KEYS[1])
