-- Idempotent Redis stock restore, keyed by the outbox event id in KEYS[1].
-- KEYS: done-marker, stock, purchased-users, accepted-map
-- ARGV: userId, orderId, releaseEligibility (1 = cancel, 0 = refund), maxStock
-- Returns: 1 applied, 0 already applied, -2 needs reconciliation (missing key,
-- wrong type or non-numeric stock), -3 needs reconciliation (would exceed total).
--
-- Redis does not roll back commands that already ran when a later command fails,
-- so every key, argument and bound is validated before the first write. The done
-- marker deliberately has no TTL: tb_outbox keeps the durable record, and a replay
-- after the marker expired would add the stock a second time.
local function decimal(value, maximum)
    if type(value) ~= 'string' or not string.match(value, '^[1-9]%d*$') then return false end
    return #value < #maximum or (#value == #maximum and value <= maximum)
end
local function integer(value)
    if type(value) ~= 'string' or not string.match(value, '^%d+$') or (#value > 1 and string.sub(value,1,1) == '0') then return nil end
    local number = tonumber(value)
    if not number or number < 0 or number > 2147483647 or number ~= math.floor(number) then return nil end
    return number
end
if #KEYS ~= 4 or #ARGV ~= 4 then return -2 end
for i = 1,4 do
    for j = i+1,4 do if KEYS[i] == KEYS[j] then return -2 end end
end
if not decimal(ARGV[1],'18446744073709551615') or not decimal(ARGV[2],'9223372036854775807') then return -2 end
if ARGV[3] ~= '0' and ARGV[3] ~= '1' then return -2 end
local maxStock = integer(ARGV[4])
if not maxStock then return -2 end
local doneKey, stockKey, purchasedKey, acceptedKey = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local userId, orderId = ARGV[1], ARGV[2]
local releaseEligibility = ARGV[3]

local doneType = redis.call('TYPE', doneKey).ok
if doneType ~= 'none' and doneType ~= 'string' then return -2 end
if doneType == 'string' then
    if redis.call('GET',doneKey) ~= '1' then return -2 end
    return 0
end

local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'string' then return -2 end
local stock = integer(redis.call('GET', stockKey))
if not stock then return -2 end
local nextStock = stock + 1
if nextStock < 0 then return -3 end
if nextStock > maxStock then return -3 end

local ownsPurchase = false
if releaseEligibility == '1' then
    local purchasedType = redis.call('TYPE', purchasedKey).ok
    if purchasedType ~= 'none' and purchasedType ~= 'set' then return -2 end
    local acceptedType = redis.call('TYPE', acceptedKey).ok
    if acceptedType ~= 'none' and acceptedType ~= 'hash' then return -2 end
    local accepted = redis.call('HGET',acceptedKey,userId)
    if accepted and not decimal(accepted,'9223372036854775807') then return -2 end
    -- The old event owns only its own reservation. A later accepted order must
    -- retain both its stable mapping and purchase eligibility.
    ownsPurchase = not accepted or accepted == orderId
end

-- Validation finished: the marker goes first so a crash below is retried as
-- "already done" instead of restoring the stock twice.
redis.call('SET', doneKey, '1')
redis.call('SET', stockKey, tostring(nextStock))
if releaseEligibility == '1' and ownsPurchase then
    redis.call('SREM', purchasedKey, userId)
    if redis.call('HGET', acceptedKey, userId) == orderId then
        redis.call('HDEL', acceptedKey, userId)
    end
end
return 1
