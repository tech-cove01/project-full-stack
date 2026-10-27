package com.fullstack.platform.ratelimit;

import lombok.extern.slf4j.Slf4j;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Service;

import java.time.Duration;

/** Shared fixed-window limiter. It fails open only when Redis itself is unavailable. */
@Slf4j
@Service
public class RateLimitService {
    private final StringRedisTemplate redis;

    public RateLimitService(StringRedisTemplate redis) {
        this.redis = redis;
    }

    public boolean allow(String action, long userId, int limit) {
        if (limit <= 0) return true;
        long window = System.currentTimeMillis() / 1000;
        String key = "rate:" + action + ":" + userId + ":" + window;
        try {
            Long count = redis.opsForValue().increment(key);
            if (count != null && count == 1L) {
                redis.expire(key, Duration.ofSeconds(2));
            }
            return count != null && count <= limit;
        } catch (RuntimeException e) {
            log.warn("Rate limiter unavailable; allowing {} for user {}", action, userId, e);
            return true;
        }
    }
}
