-- Standalone Redis baseline; mutations and stream append run in one script.
-- ARGV: voucherId, userId, orderId, nowMs, beginMs, endMs (DB-verified fallback).
local voucherId, userId, orderId = ARGV[1], ARGV[2], ARGV[3]
local stockKey = 'seckill:stock:' .. voucherId
local purchaseKey = 'seckill:order:' .. voucherId
local reservationKey = 'order:reservation:' .. orderId
local stock = tonumber(redis.call('GET', stockKey))
if not stock then return 3 end
local now = tonumber(ARGV[4])
local begins = tonumber(redis.call('GET', 'seckill:begin:' .. voucherId)) or tonumber(ARGV[5])
local ends = tonumber(redis.call('GET', 'seckill:end:' .. voucherId)) or tonumber(ARGV[6])
if not begins or not ends then return 3 end
if now < begins then return 4 end
if now >= ends then return 5 end
if redis.call('SISMEMBER', purchaseKey, userId) == 1 then return 2 end
if stock <= 0 then return 1 end

-- Lua runtime errors do not roll back writes: validate types before changing anything.
local streamType = redis.call('TYPE', 'stream.orders').ok
local reservationType = redis.call('TYPE', reservationKey).ok
if streamType ~= 'none' and streamType ~= 'stream' then return 6 end
if reservationType ~= 'none' then return 6 end
redis.call('INCRBY', stockKey, -1)
redis.call('SADD', purchaseKey, userId)
redis.call('HSET', reservationKey, 'userId', userId, 'voucherId', voucherId,
        'state', 'PROCESSING', 'createdAt', ARGV[4], 'retries', '0')
redis.call('EXPIRE', reservationKey, 86400)
redis.call('XADD', 'stream.orders', '*', 'userId', userId, 'voucherId', voucherId, 'id', orderId)
return 0
