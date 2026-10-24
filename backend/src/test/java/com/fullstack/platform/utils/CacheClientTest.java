package com.fullstack.platform.utils;

import com.fullstack.platform.entity.Shop;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.ValueOperations;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

class CacheClientTest {
    private StringRedisTemplate redis;
    private ValueOperations<String, String> values;
    private CacheClient cache;
    private Function<Long, Shop> database;

    @BeforeEach @SuppressWarnings("unchecked") void setup() {
        redis = mock(StringRedisTemplate.class); values = mock(ValueOperations.class);
        database = mock(Function.class);
        when(redis.opsForValue()).thenReturn(values);
        cache = new CacheClient(redis);
    }

    @Test void negativeCacheSkipsDatabase() {
        when(values.get("shop:1")).thenReturn("");
        assertNull(cache.queryWithPassThrough("shop:", 1L, Shop.class, database, 30L, TimeUnit.MINUTES));
        verifyNoInteractions(database);
    }

    @Test void missingRowGetsShortLivedNegativeCache() {
        when(database.apply(1L)).thenReturn(null);
        assertNull(cache.queryWithPassThrough("shop:", 1L, Shop.class, database, 30L, TimeUnit.MINUTES));
        verify(values).set("shop:1", "", RedisConstants.CACHE_NULL_TTL, TimeUnit.MINUTES);
    }

    @Test void databaseFailureIsNotCachedAsMissing() {
        when(database.apply(1L)).thenThrow(new IllegalStateException("database unavailable"));
        assertThrows(IllegalStateException.class,
                () -> cache.queryWithPassThrough("shop:", 1L, Shop.class, database, 30L, TimeUnit.MINUTES));
        verify(values, never()).set(anyString(), anyString(), anyLong(), any(TimeUnit.class));
    }

    @Test void cachedShopSkipsDatabase() {
        when(values.get("shop:1")).thenReturn("{\"id\":1,\"name\":\"Coffee\"}");
        Shop shop = cache.queryWithPassThrough("shop:", 1L, Shop.class, database, 30L, TimeUnit.MINUTES);
        assertEquals("Coffee", shop.getName());
        verifyNoInteractions(database);
    }
}
