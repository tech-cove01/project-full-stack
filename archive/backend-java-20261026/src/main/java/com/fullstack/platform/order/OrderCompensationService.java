package com.fullstack.platform.order;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fullstack.platform.outbox.NeedsReviewException;
import org.springframework.core.io.ClassPathResource;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;

import java.util.List;

import static com.fullstack.platform.utils.RedisConstants.*;

/** Applies Redis stock compensation idempotently, keyed by the outbox event ID. */
@Service
public class OrderCompensationService {
    private static final DefaultRedisScript<Long> RESTORE = script();

    private final StringRedisTemplate redis;
    private final ObjectMapper objectMapper;
    private final JdbcTemplate jdbc;

    public OrderCompensationService(StringRedisTemplate redis, ObjectMapper objectMapper, JdbcTemplate jdbc) {
        this.redis = redis;
        this.objectMapper = objectMapper;
        this.jdbc = jdbc;
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
        Integer totalStock = totalStock(voucherId);
        List<String> keys = List.of(
                "comp:done:" + eventId,
                SECKILL_STOCK_KEY + voucherId,
                SECKILL_PURCHASE_KEY + voucherId,
                SECKILL_ACCEPTED_KEY + voucherId);
        Long result = redis.execute(RESTORE, keys, userId, orderId, releaseEligibility ? "1" : "0",
                Integer.toString(totalStock));
        if (result == null) {
            throw new IllegalStateException("restore script returned null");
        }
        if (result == -2) {
            throw new NeedsReviewException("Stock key missing, wrong type or not numeric for voucher " + voucherId);
        }
        if (result == -3) {
            throw new NeedsReviewException("Restoring stock would exceed total_stock for voucher " + voucherId);
        }
    }

    /** Upper bound for the compact counter; compensation must never invent stock above the initial total. */
    private int totalStock(long voucherId) {
        List<Integer> totals = jdbc.queryForList(
                "SELECT total_stock FROM tb_seckill_voucher WHERE voucher_id = ?", Integer.class, voucherId);
        if (totals.isEmpty()) {
            throw new NeedsReviewException("Activity " + voucherId + " no longer exists");
        }
        return totals.getFirst();
    }
}
