package com.fullstack.platform.activity;

import com.fullstack.platform.audit.AuditService;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.Voucher;
import com.fullstack.platform.mapper.SeckillVoucherMapper;
import com.fullstack.platform.mapper.VoucherMapper;
import com.fullstack.platform.outbox.NeedsReviewException;
import com.fullstack.platform.outbox.OutboxRepository;
import org.springframework.core.io.ClassPathResource;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.ZoneId;
import java.util.List;
import java.util.Map;

import static com.fullstack.platform.utils.RedisConstants.*;

/** Implements the Redis admission gate protocol around DB publish state. */
@Service
public class ActivityPublicationService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private static final DefaultRedisScript<Long> PREPARE = script("activity-prepare.lua");
    private static final DefaultRedisScript<Long> OPEN = script("activity-open.lua");
    private static final DefaultRedisScript<Long> CLOSE = script("activity-close.lua");

    private final SeckillVoucherMapper seckillMapper;
    private final VoucherMapper voucherMapper;
    private final JdbcTemplate jdbc;
    private final StringRedisTemplate redis;
    private final AuditService audit;
    private final OutboxRepository outbox;

    public ActivityPublicationService(SeckillVoucherMapper seckillMapper, VoucherMapper voucherMapper,
                                      JdbcTemplate jdbc, StringRedisTemplate redis,
                                      AuditService audit, OutboxRepository outbox) {
        this.seckillMapper = seckillMapper;
        this.voucherMapper = voucherMapper;
        this.jdbc = jdbc;
        this.redis = redis;
        this.audit = audit;
        this.outbox = outbox;
    }

    private static DefaultRedisScript<Long> script(String file) {
        DefaultRedisScript<Long> script = new DefaultRedisScript<>();
        script.setLocation(new ClassPathResource(file));
        script.setResultType(Long.class);
        return script;
    }

    /**
     * Prepares Redis, marks the DB ACTIVE, then opens the exact prepared version.
     * Every step is idempotent; failures leave the gate closed and the event retryable.
     */
    @Transactional(rollbackFor = Exception.class)
    public void prepareAndOpen(long activityId) {
        SeckillVoucher activity = seckillMapper.selectById(activityId);
        if (activity == null) {
            throw new NeedsReviewException("Activity " + activityId + " disappeared");
        }
        if ("OFFLINE".equals(activity.getPublishStatus())) {
            return;
        }
        int version = activity.getPublishVersion() == null ? 0 : activity.getPublishVersion();
        int target = "INITIALIZING".equals(activity.getPublishStatus())
                ? version + 1 : Math.max(version, 1);

        Long prepared = redis.execute(PREPARE, gateKeys(activity), Integer.toString(target),
                Integer.toString(activity.getStock()), millis(activity.getBeginTime()),
                millis(activity.getEndTime()));
        if (prepared == null || prepared == -2) {
            throw new NeedsReviewException("Activity " + activityId + " prepare mismatch: " + prepared);
        }

        if ("INITIALIZING".equals(activity.getPublishStatus())) {
            int changed = jdbc.update("""
                    UPDATE tb_seckill_voucher
                    SET publish_status = 'ACTIVE', publish_version = ?
                    WHERE voucher_id = ? AND publish_status = 'INITIALIZING' AND publish_version = ?
                    """, target, activityId, version);
            if (changed == 1) {
                Voucher voucher = voucherMapper.selectById(activityId);
                if (voucher == null) {
                    throw new NeedsReviewException("Voucher " + activityId + " disappeared");
                }
                audit.record(activity.getMerchantId(), voucher.getShopId(), 0L, "SYSTEM",
                        "ACTIVITY_ACTIVE", Long.toString(activityId), Map.of("version", target));
                outbox.enqueue("ACTIVITY:" + activityId + ":" + target + ":OPEN", "ACTIVITY",
                        Long.toString(activityId), target, "ACTIVITY_OPEN",
                        Map.of("activityId", Long.toString(activityId), "version", target));
            }
            activity = seckillMapper.selectById(activityId);
        }
        if (activity == null || !"ACTIVE".equals(activity.getPublishStatus())) {
            return;
        }
        Long opened = redis.execute(OPEN, List.of(SECKILL_GATE_KEY + activityId),
                Integer.toString(activity.getPublishVersion()));
        if (opened == null || opened < 0) {
            throw new NeedsReviewException("Activity " + activityId + " gate open failed: " + opened);
        }
    }

    /** ACTIVE but not yet open: retry opening without touching stock. */
    public void openOnly(long activityId) {
        SeckillVoucher activity = seckillMapper.selectById(activityId);
        if (activity == null || "OFFLINE".equals(activity.getPublishStatus())) {
            return;
        }
        int version = Math.max(activity.getPublishVersion() == null ? 0 : activity.getPublishVersion(), 1);
        Long opened = redis.execute(OPEN, List.of(SECKILL_GATE_KEY + activityId), Integer.toString(version));
        if (opened == null || opened < 0) {
            throw new NeedsReviewException("Activity " + activityId + " gate open failed: " + opened);
        }
    }

    /** Synchronous close used by offline; returns -2 if the key has an unexpected type. */
    public long closeGate(long activityId, int nextVersion) {
        Long closed = redis.execute(CLOSE, List.of(SECKILL_GATE_KEY + activityId), Integer.toString(nextVersion));
        if (closed == null) {
            throw new IllegalStateException("Activity close returned no result");
        }
        return closed;
    }

    private static List<String> gateKeys(SeckillVoucher activity) {
        long id = activity.getVoucherId();
        return List.of(SECKILL_GATE_KEY + id, SECKILL_STOCK_KEY + id,
                SECKILL_BEGIN_KEY + id, SECKILL_END_KEY + id);
    }

    private static String millis(java.time.LocalDateTime time) {
        return Long.toString(time.atZone(BUSINESS_ZONE).toInstant().toEpochMilli());
    }
}
