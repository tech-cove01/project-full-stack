-- Idempotent stock restore. The outbox event ID is the dedupe key.
-- KEYS: done-marker, stock, purchased-users, accepted-map
-- ARGV: userId, orderId, releaseEligibility (1 = cancel, 0 = refund)
if redis.call('EXISTS', KEYS[2]) == 0 then return -2 end
if redis.call('TYPE', KEYS[2]).ok ~= 'string' then return -2 end
if redis.call('SETNX', KEYS[1], '1') == 0 then return 0 end
redis.call('INCRBY', KEYS[2], 1)
if ARGV[3] == '1' then
    redis.call('SREM', KEYS[3], ARGV[1])
    if redis.call('HGET', KEYS[4], ARGV[1]) == ARGV[2] then
        redis.call('HDEL', KEYS[4], ARGV[1])
    end
end
redis.call('EXPIRE', KEYS[1], 604800)
return 1
