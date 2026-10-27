-- Only a definite DB rejection, never an unknown commit outcome, may release a reservation.
-- KEYS: reservation, stock, purchased users, accepted map
-- ARGV: userId, reason, keep purchase marker, orderId
if redis.call('HGET', KEYS[1], 'state') ~= 'PROCESSING' then return 0 end
if redis.call('HGET', KEYS[1], 'userId') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'state', 'FAILED', 'message', ARGV[2])
redis.call('EXPIRE', KEYS[1], 86400)
-- Do not recreate a vanished campaign cache with a fictitious stock.
if redis.call('EXISTS', KEYS[2]) == 1 then redis.call('INCRBY', KEYS[2], 1) end
if ARGV[3] ~= '1' then redis.call('SREM', KEYS[3], ARGV[1]) end
-- Only remove the idempotency mapping that belongs to this rejected reservation.
if redis.call('HGET', KEYS[4], ARGV[1]) == ARGV[4] then
    redis.call('HDEL', KEYS[4], ARGV[1])
end
return 1
