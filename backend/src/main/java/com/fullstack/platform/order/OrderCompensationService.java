package com.fullstack.platform.order;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fullstack.platform.outbox.NeedsReviewException;
import org.springframework.core.io.ClassPathResource;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Service;

import java.util.List;

import static com.fullstack.platform.utils.RedisConstants.*;

/** Applies Redis stock compensation idempotently, keyed by the outbox event ID. */
@Service
public class OrderCompensationService {
    private static final DefaultRedisScript<Long> RESTORE = script();

    private final StringRedisTemplate redis;
    private final ObjectMapper objectMapper;

    public OrderCompensationService(StringRedisTemplate redis, ObjectMapper objectMapper) {
        this.redis = redis;
        this.objectMapper = objectMapper;
    }

    private static DefaultRedisScript<Long> script() {
        DefaultRedisScript<Long> script = new DefaultRedisScript<>();
        script.setLocation(new ClassPathResource("order-restore.lua"));
        script.setResultType(Long.class);
        return script;
    }

    public void apply(String eventId, String payload) {
        JsonNode node;
        try {
            node = objectMapper.readTree(payload);
        } catch (Exception e) {
            throw new NeedsReviewException("Cannot parse outbox payload");
        }
        int stockRestore = node.path("stockRestore").asInt(0);
        if (stockRestore == 0) {
            return;
        }
        long voucherId = Long.parseLong(node.path("voucherId").asText());
        String userId = node.path("userId").asText();
        String orderId = node.path("orderId").asText();
        boolean releaseEligibility = node.path("releaseEligibility").asBoolean(false);
        List<String> keys = List.of(
                "comp:done:" + eventId,
                SECKILL_STOCK_KEY + voucherId,
                SECKILL_PURCHASE_KEY + voucherId,
                SECKILL_ACCEPTED_KEY + voucherId);
        Long result = redis.execute(RESTORE, keys, userId, orderId, releaseEligibility ? "1" : "0");
        if (result == null) {
            throw new IllegalStateException("restore script returned null");
        }
        if (result == -2) {
            throw new NeedsReviewException("Stock key missing or wrong type for voucher " + voucherId);
        }
    }
}
