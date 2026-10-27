package com.fullstack.platform.utils;

import cn.hutool.json.JSONUtil;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;
import static com.fullstack.platform.utils.RedisConstants.CACHE_NULL_TTL;

/** Cache-aside with short-lived negative caching. DB failures are never stored as empty results. */
@Component
public class CacheClient {
    private final StringRedisTemplate redis;

    public CacheClient(StringRedisTemplate redis) { this.redis = redis; }

    public void set(String key, Object value, Long time, TimeUnit unit) {
        redis.opsForValue().set(key, JSONUtil.toJsonStr(value), time, unit);
    }

    public <R, ID> R queryWithPassThrough(String prefix, ID id, Class<R> type,
                                         Function<ID, R> database, Long time, TimeUnit unit) {
        String key = prefix + id;
        String json = redis.opsForValue().get(key);
        if (json != null) return json.isEmpty() ? null : JSONUtil.toBean(json, type);
        R value = database.apply(id);
        if (value == null) redis.opsForValue().set(key, "", CACHE_NULL_TTL, TimeUnit.MINUTES);
        else set(key, value, time, unit);
        return value;
    }
}
