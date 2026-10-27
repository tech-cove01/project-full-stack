package com.fullstack.platform.ai;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.time.LocalDate;
import java.time.ZoneId;
import java.util.List;

/**
 * Per-user request budget in front of the provider.
 *
 * <p>Both counters are incremented before the call so a burst cannot slip past the limit, and a
 * rejected request never reaches embedding or chat. This is a local safety valve, not billing.
 */
@Service
public class AiBudgetService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final StringRedisTemplate redis;
    private final AiProperties properties;

    public AiBudgetService(StringRedisTemplate redis, AiProperties properties) {
        this.redis = redis;
        this.properties = properties;
    }

    /** Returns null when the request may proceed, otherwise a stable reason code. */
    public String rejectReason(long userId) {
        long minute = System.currentTimeMillis() / 60_000;
        if (!increment("ai:rate:" + userId + ":" + minute, Duration.ofMinutes(2),
                properties.getMaxPerMinute())) {
            return "AI_RATE_LIMITED";
        }
        String day = LocalDate.now(BUSINESS_ZONE).toString();
        if (!increment("ai:budget:" + userId + ":" + day, Duration.ofDays(2),
                properties.getDailyBudget())) {
            return "AI_BUDGET_EXHAUSTED";
        }
        return null;
    }

    public void recordUsage(long userId) {
        String day = LocalDate.now(BUSINESS_ZONE).toString();
        redis.opsForValue().increment("ai:usage:" + userId + ":" + day);
    }

    private boolean increment(String key, Duration ttl, int limit) {
        Long value = redis.execute(new org.springframework.data.redis.core.script.DefaultRedisScript<>(
                "local n = redis.call('INCR', KEYS[1]) "
                        + "if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end "
                        + "return n", Long.class),
                List.of(key), Long.toString(ttl.toSeconds()));
        return value == null || value <= limit;
    }
}
