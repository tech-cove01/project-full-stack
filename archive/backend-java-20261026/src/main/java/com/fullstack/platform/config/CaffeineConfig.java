package com.fullstack.platform.config;

import com.fullstack.platform.entity.Shop;
import com.github.benmanes.caffeine.cache.Cache;
import com.github.benmanes.caffeine.cache.Caffeine;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import java.time.Duration;

/** Bounded, short-lived L1 cache, adapted from the reference item-service cache pattern. */
@Configuration
public class CaffeineConfig {
    @Bean
    public Cache<Long, Shop> shopCache(
            @Value("${app.cache.shop-max-size:10000}") long maximumSize,
            @Value("${app.cache.shop-ttl-seconds:10}") long ttlSeconds) {
        return Caffeine.newBuilder().initialCapacity(100)
                .maximumSize(maximumSize).expireAfterWrite(Duration.ofSeconds(ttlSeconds))
                .recordStats().build();
    }
}
