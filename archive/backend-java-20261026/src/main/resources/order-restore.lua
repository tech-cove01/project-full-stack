-- Idempotent stock restore. The outbox event ID is the persistent dedupe key.
-- KEYS: done-marker, stock, purchased-users, accepted-map
-- ARGV: userId, orderId, releaseEligibility (1 = cancel, 0 = refund), maxStock
-- Returns: 1 applied, 0 already applied, -2 needs reconciliation (missing/wrong type
-- or non-numeric value), -3 needs reconciliation (would exceed total stock).
--
-- Redis does not roll back commands that already ran when a later command fails, so every
-- key, argument and bound is validated before the first write. The done marker carries no
-- TTL on purpose: tb_outbox keeps the durable record, and a replay after the marker expired
-- would add stock a second time.
local doneKey, stockKey, purchasedKey, acceptedKey = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local userId, orderId = ARGV[1], ARGV[2]
local releaseEligibility = ARGV[3]
local maxStock = tonumber(ARGV[4])

local doneType = redis.call('TYPE', doneKey).ok
if doneType ~= 'none' and doneType ~= 'string' then return -2 end
if doneType == 'string' then return 0 end

local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'string' then return -2 end
local stock = tonumber(redis.call('GET', stockKey))
if not stock then return -2 end
local nextStock = stock + 1
if nextStock < 0 then return -3 end
if maxStock and nextStock > maxStock then return -3 end

if releaseEligibility == '1' then
    local purchasedType = redis.call('TYPE', purchasedKey).ok
    if purchasedType ~= 'none' and purchasedType ~= 'set' then return -2 end
    local acceptedType = redis.call('TYPE', acceptedKey).ok
    if acceptedType ~= 'none' and acceptedType ~= 'hash' then return -2 end
end

-- Validation finished: the marker goes first so a crash below is retried as "already done"
-- instead of restoring stock twice.
redis.call('SET', doneKey, '1')
redis.call('SET', stockKey, tostring(nextStock))
if releaseEligibility == '1' then
    redis.call('SREM', purchasedKey, userId)
    if redis.call('HGET', acceptedKey, userId) == orderId then
        redis.call('HDEL', acceptedKey, userId)
    end
end
return 1
