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
        String key = "ai:lock:" + userId + ":" + conversationId;
        String token = UUID.randomUUID().toString();
        Boolean acquired = redis.opsForValue().setIfAbsent(key, token, Duration.ofSeconds(90));
        return Boolean.TRUE.equals(acquired) ? token : null;
    }

    public void unlock(long userId, String conversationId, String token) {
        if (token == null) return;
        redis.execute(RELEASE, List.of("ai:lock:" + userId + ":" + conversationId), token);
    }
}
