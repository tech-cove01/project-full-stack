package com.fullstack.platform.ai;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.util.List;
import java.util.UUID;

@Service
public class AiLockService {
    private static final DefaultRedisScript<Long> RELEASE = new DefaultRedisScript<>(
            "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end",
            Long.class);

    private final StringRedisTemplate redis;

    public AiLockService(StringRedisTemplate redis) {
        this.redis = redis;
    }

    public String tryLock(long userId, String conversationId) {
        return tryLock(userId, conversationId, Duration.ofSeconds(90));
    }

    /** The lease must outlive the whole turn (queue wait + generation), not just the HTTP call. */
    public String tryLock(long userId, String conversationId, Duration ttl) {
        String key = "ai:lock:" + userId + ":" + conversationId;
        String token = UUID.randomUUID().toString();
        Boolean acquired = redis.opsForValue().setIfAbsent(key, token, ttl);
        return Boolean.TRUE.equals(acquired) ? token : null;
    }

    /** True while this turn still owns the conversation lease. */
    public boolean owns(long userId, String conversationId, String token) {
        if (token == null) return false;
        Object current = redis.opsForValue().get("ai:lock:" + userId + ":" + conversationId);
        return token.equals(current);
    }

    public void unlock(long userId, String conversationId, String token) {
        if (token == null) return;
        redis.execute(RELEASE, List.of("ai:lock:" + userId + ":" + conversationId), token);
    }
}
