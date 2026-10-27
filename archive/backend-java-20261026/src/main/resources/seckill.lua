-- Admission gate: Redis is not the authority for the DB transaction, but no new
-- reservation may pass without an OPEN gate at the expected publish version.
-- KEYS: gate, stock, begin, end, purchased-users, reservation, accepted-map, stream
-- ARGV: voucherId, userId, orderId, nowMs, fallbackBeginMs, fallbackEndMs, expectedVersion
local gateKey, stockKey, beginKey, endKey = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local purchaseKey, reservationKey, acceptedKey, streamKey = KEYS[5], KEYS[6], KEYS[7], KEYS[8]
local voucherId, userId, orderId = ARGV[1], ARGV[2], ARGV[3]

-- Validate every key type before the first write. A Lua runtime error does not
-- roll back earlier writes, so all foreseeable failures must happen first.
local gateType = redis.call('TYPE', gateKey).ok
if gateType ~= 'hash' then return 7 end
if redis.call('HGET', gateKey, 'state') ~= 'OPEN' then return 7 end
if tonumber(redis.call('HGET', gateKey, 'version')) ~= tonumber(ARGV[7]) then return 7 end

local stockType = redis.call('TYPE', stockKey).ok
if stockType ~= 'string' then return 3 end
local stock = tonumber(redis.call('GET', stockKey))
if not stock then return 6 end

local now = tonumber(ARGV[4])
local begins = tonumber(redis.call('GET', beginKey)) or tonumber(ARGV[5])
local ends = tonumber(redis.call('GET', endKey)) or tonumber(ARGV[6])
if not begins or not ends then return 3 end
if now < begins then return 4 end
if now >= ends then return 5 end

-- An accepted request is idempotent for this user: return the original order ID.
if redis.call('HEXISTS', acceptedKey, userId) == 1 then return 8 end
if redis.call('SISMEMBER', purchaseKey, userId) == 1 then return 2 end
if stock <= 0 then return 1 end

local streamType = redis.call('TYPE', streamKey).ok
local reservationType = redis.call('TYPE', reservationKey).ok
local purchaseType = redis.call('TYPE', purchaseKey).ok
local acceptedType = redis.call('TYPE', acceptedKey).ok
if streamType ~= 'none' and streamType ~= 'stream' then return 6 end
if reservationType ~= 'none' and reservationType ~= 'hash' then return 6 end
if purchaseType ~= 'none' and purchaseType ~= 'set' then return 6 end
if acceptedType ~= 'none' and acceptedType ~= 'hash' then return 6 end

redis.call('INCRBY', stockKey, -1)
redis.call('SADD', purchaseKey, userId)
redis.call('HSET', reservationKey, 'userId', userId, 'voucherId', voucherId,
        'state', 'PROCESSING', 'createdAt', ARGV[4], 'retries', '0')
redis.call('EXPIRE', reservationKey, 86400)
redis.call('HSET', acceptedKey, userId, orderId)
redis.call('EXPIRE', acceptedKey, 604800)
redis.call('XADD', streamKey, '*', 'userId', userId, 'voucherId', voucherId, 'id', orderId)
return 0
