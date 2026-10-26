package com.fullstack.platform.service.impl;

import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.dto.Result;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import com.fullstack.platform.service.ISeckillVoucherService;
import com.fullstack.platform.service.IVoucherOrderService;
import com.fullstack.platform.order.OrderReviewRepository;
import com.fullstack.platform.service.order.OrderCreationService;
import com.fullstack.platform.service.order.OrderRejectedException;
import com.fullstack.platform.utils.RedisIdWorker;
import com.fullstack.platform.utils.UserHolder;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import lombok.extern.slf4j.Slf4j;
import org.springframework.core.io.ClassPathResource;
import org.springframework.data.domain.Range;
import org.springframework.data.redis.connection.stream.*;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Redis admission is atomic; only the separate transactional writer creates an order. */
@Slf4j
@Service
public class VoucherOrderServiceImpl extends ServiceImpl<VoucherOrderMapper, VoucherOrder>
        implements IVoucherOrderService {
    static final String STREAM = "stream.orders";
    static final String GROUP = "g1";
    static final String DEAD_STREAM = "stream.orders.dead";
    static final String RESERVATION_PREFIX = "order:reservation:";
    static final int MAX_ATTEMPTS = 5;
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private static final Duration RECLAIM_IDLE = Duration.ofSeconds(60);
    private static final Duration STATUS_TTL = Duration.ofHours(24);
    private static final DefaultRedisScript<Long> SECKILL_SCRIPT = script("seckill.lua");
    private static final DefaultRedisScript<Long> REJECT_SCRIPT = script("reject-order.lua");
    private static final DefaultRedisScript<Long> GROUP_SCRIPT = new DefaultRedisScript<>("""
            local result = redis.pcall('XGROUP', 'CREATE', KEYS[1], ARGV[1], '0', 'MKSTREAM')
            if type(result) == 'table' and result.err then
                if string.find(result.err, 'BUSYGROUP') then return 0 end
                return redis.error_reply(result.err)
            end
            return 1
            """, Long.class);

    private final ISeckillVoucherService seckillVoucherService;
    private final RedisIdWorker redisIdWorker;
    private final StringRedisTemplate redis;
    private final OrderCreationService orderCreationService;
    private final OrderReviewRepository orderReviewRepository;
    private final String consumerName = "order-" + UUID.randomUUID();
    private final ExecutorService executor = Executors.newSingleThreadExecutor(task -> {
        Thread thread = new Thread(task, "order-stream-consumer");
        thread.setDaemon(true);
        return thread;
    });
    private volatile boolean running;

    public VoucherOrderServiceImpl(ISeckillVoucherService seckillVoucherService,
                                   RedisIdWorker redisIdWorker, StringRedisTemplate redis,
                                   OrderCreationService orderCreationService,
                                   OrderReviewRepository orderReviewRepository) {
        this.seckillVoucherService = seckillVoucherService;
        this.redisIdWorker = redisIdWorker;
        this.redis = redis;
        this.orderCreationService = orderCreationService;
        this.orderReviewRepository = orderReviewRepository;
    }

    private static DefaultRedisScript<Long> script(String file) {
        DefaultRedisScript<Long> script = new DefaultRedisScript<>();
        script.setLocation(new ClassPathResource(file));
        script.setResultType(Long.class);
        return script;
    }

    @PostConstruct
    void startConsumer() {
        running = true;
        executor.submit(this::consume);
    }

    @PreDestroy
    void stopConsumer() {
        running = false;
        executor.shutdownNow();
        // Keep the consumer's unacknowledged messages reclaimable after a restart.
    }

    /** Dev fault switch: stop reading new Stream messages without killing the API. */
    public void pauseConsumer() {
        running = false;
        log.warn("Order consumer paused by dev switch");
    }

    /** Restart the consumer in the same JVM; pending messages are reclaimed by the normal path. */
    public synchronized void resumeConsumer() {
        if (running) return;
        running = true;
        executor.submit(this::consume);
        log.warn("Order consumer resumed by dev switch");
    }

    private void consume() {
        boolean initialized = false;
        long nextRecovery = 0;
        long retryDelay = 1_000;
        while (running && !Thread.currentThread().isInterrupted()) {
            try {
                if (!initialized) {
                    redis.execute(GROUP_SCRIPT, List.of(STREAM), GROUP);
                    initialized = true;
                }
                if (System.currentTimeMillis() >= nextRecovery) {
                    recoverAbandonedMessages();
                    nextRecovery = System.currentTimeMillis() + 30_000;
                }
                List<MapRecord<String, Object, Object>> records = read(ReadOffset.from("0"), false);
                if (records == null || records.isEmpty()) {
                    records = read(ReadOffset.lastConsumed(), true);
                }
                if (records != null) {
                    for (MapRecord<String, Object, Object> record : records) {
                        if (processRecord(record)) {
                            retryDelay = 1_000;
                        } else {
                            // Exponential backoff: the message stays pending until a durable outcome exists.
                            pause(retryDelay);
                            retryDelay = Math.min(retryDelay * 2, 30_000);
                        }
                    }
                }
            } catch (Exception e) {
                initialized = false;
                log.error("Order consumer unavailable; pending messages will be retried", e);
                pause(retryDelay);
                retryDelay = Math.min(retryDelay * 2, 30_000);
            }
        }
    }

    private List<MapRecord<String, Object, Object>> read(ReadOffset offset, boolean block) {
        StreamReadOptions options = StreamReadOptions.empty().count(1);
        if (block) options = options.block(Duration.ofSeconds(2));
        return redis.<Object, Object>opsForStream().read(Consumer.from(GROUP, consumerName), options,
                StreamOffset.create(STREAM, offset));
    }

    private void recoverAbandonedMessages() {
        // Claim across consumer names after restart. Idle threshold must exceed DB transaction time.
        PendingMessages pending = redis.opsForStream().pending(STREAM, GROUP, Range.unbounded(), 100);
        if (pending == null) return;
        for (PendingMessage message : pending) {
            if (!consumerName.equals(message.getConsumerName())
                    && message.getElapsedTimeSinceLastDelivery().compareTo(RECLAIM_IDLE) >= 0) {
                redis.opsForStream().claim(STREAM, GROUP, consumerName, RECLAIM_IDLE, message.getId());
            }
        }
    }

    /** Returns false on retryable failure. ACK always follows a durable outcome. */
    boolean processRecord(MapRecord<String, Object, Object> record) {
        VoucherOrder order;
        try {
            Map<Object, Object> value = record.getValue();
            order = new VoucherOrder().setId(parseId(value.get("id")))
                    .setUserId(parseId(value.get("userId"))).setVoucherId(parseId(value.get("voucherId")));
        } catch (RuntimeException invalidPayload) {
            deadLetter(record, "INVALID_PAYLOAD");
            acknowledge(record);
            return true;
        }

        String key = RESERVATION_PREFIX + order.getId();
        if ("FAILED".equals(redis.opsForHash().get(key, "state"))) {
            // A previous rejection may have been persisted before the ACK connection failed.
            acknowledge(record);
            return true;
        }
        try {
            // Separate Spring bean: transaction interception includes both stock and order writes.
            orderCreationService.create(order);
            redis.opsForHash().putAll(key, Map.of("userId", order.getUserId().toString(),
                    "voucherId", order.getVoucherId().toString(), "state", "CREATED", "message", "订单已创建"));
            redis.expire(key, STATUS_TTL);
            acknowledge(record);
            return true;
        } catch (OrderRejectedException rejected) {
            redis.execute(REJECT_SCRIPT, List.of(key, "seckill:stock:" + order.getVoucherId(),
                            "seckill:order:" + order.getVoucherId(),
                            "seckill:accepted:" + order.getVoucherId()),
                    order.getUserId().toString(), rejected.getMessage(),
                    rejected.keepPurchaseMarker() ? "1" : "0", order.getId().toString());
            acknowledge(record);
            return true;
        } catch (Exception retryable) {
            long attempts = Objects.requireNonNull(redis.opsForHash().increment(key, "retries", 1));
            redis.expire(key, STATUS_TTL);
            log.warn("Order {} attempt {} failed", order.getId(), attempts, retryable);
            if (attempts >= MAX_ATTEMPTS) {
                // Connection/commit errors are uncertain. Releasing stock could oversell an order
                // which already committed. The message may only be acknowledged once a durable
                // record exists; if the DB is still down this insert fails and we keep it pending.
                try {
                    orderReviewRepository.record(order, record.getId().getValue(), attempts, retryable);
                } catch (Exception notDurable) {
                    log.error("Order {} needs review but no durable record could be written; "
                            + "keeping message {} pending", order.getId(), record.getId().getValue(), notDurable);
                    return false;
                }
                deadLetter(record, retryable.getClass().getSimpleName());
                redis.opsForHash().putAll(key, Map.of("userId", order.getUserId().toString(),
                        "voucherId", order.getVoucherId().toString(), "state", "PROCESSING",
                        "message", "处理异常，等待核对，请勿重复下单", "manualReview", "true"));
                acknowledge(record);
                return true;
            }
            return false;
        }
    }

    private static Long parseId(Object value) {
        long id = Long.parseLong(Objects.requireNonNull(value).toString());
        if (id <= 0) throw new IllegalArgumentException("Invalid identifier");
        return id;
    }

    private void deadLetter(MapRecord<String, Object, Object> record, String reason) {
        Map<Object, Object> evidence = new HashMap<>(record.getValue());
        evidence.put("sourceId", record.getId().getValue());
        evidence.put("reason", reason);
        evidence.put("failedAt", Long.toString(System.currentTimeMillis()));
        redis.opsForStream().add(DEAD_STREAM, evidence);
    }

    private void acknowledge(MapRecord<String, Object, Object> record) {
        redis.opsForStream().acknowledge(STREAM, GROUP, record.getId());
    }

    private void pause(long millis) {
        try {
            Thread.sleep(millis);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    @Override
    public Result seckillVoucher(Long voucherId) {
        if (UserHolder.getUser() == null) return Result.fail("请先登录");
        if (voucherId == null || voucherId <= 0) return Result.fail("活动不存在");
        SeckillVoucher voucher = seckillVoucherService.getById(voucherId);
        if (voucher == null || voucher.getBeginTime() == null || voucher.getEndTime() == null) {
            return Result.fail("活动不存在或时间配置不完整");
        }
        if (!"ACTIVE".equals(voucher.getPublishStatus())) {
            return Result.fail("活动尚未发布或已下架");
        }
        LocalDateTime now = LocalDateTime.now(BUSINESS_ZONE);
        if (now.isBefore(voucher.getBeginTime())) return Result.fail("活动尚未开始");
        if (!now.isBefore(voucher.getEndTime())) return Result.fail("活动已经结束");

        Long userId = UserHolder.getUser().getId();
        String orderId = Long.toString(redisIdWorker.nextId("order"));
        int publishVersion = voucher.getPublishVersion() == null ? 0 : voucher.getPublishVersion();
        List<String> keys = List.of(
                "seckill:gate:" + voucherId,
                "seckill:stock:" + voucherId,
                "seckill:begin:" + voucherId,
                "seckill:end:" + voucherId,
                "seckill:order:" + voucherId,
                RESERVATION_PREFIX + orderId,
                "seckill:accepted:" + voucherId,
                STREAM);
        Long result = redis.execute(SECKILL_SCRIPT, keys, voucherId.toString(),
                userId.toString(), orderId, Long.toString(System.currentTimeMillis()),
                Long.toString(voucher.getBeginTime().atZone(BUSINESS_ZONE).toInstant().toEpochMilli()),
                Long.toString(voucher.getEndTime().atZone(BUSINESS_ZONE).toInstant().toEpochMilli()),
                Integer.toString(publishVersion));
        if (result == null) return Result.fail("系统繁忙，请稍后再试");
        return switch (result.intValue()) {
            case 0 -> Result.ok(orderId);
            case 1 -> Result.fail("库存不足");
            case 2, 8 -> acceptedOrder(userId, voucherId);
            case 3 -> Result.fail("活动库存尚未就绪");
            case 4 -> Result.fail("活动尚未开始");
            case 5 -> Result.fail("活动已经结束");
            case 7 -> Result.fail("活动尚未就绪或已下架");
            default -> Result.fail("系统繁忙，请稍后再试");
        };
    }

    /** A retry of an accepted request returns the original order ID, never a second reservation. */
    private Result acceptedOrder(Long userId, Long voucherId) {
        Object accepted = redis.opsForHash().get("seckill:accepted:" + voucherId, userId.toString());
        if (accepted != null) {
            return Result.ok(accepted.toString());
        }
        return Result.fail("不能重复下单");
    }

    @Override
    public Result queryOrderStatus(Long orderId) {
        if (UserHolder.getUser() == null) return Result.fail("请先登录");
        if (orderId == null || orderId <= 0) return Result.fail("订单不存在");
        Long userId = UserHolder.getUser().getId();
        VoucherOrder order = orderCreationService.findById(orderId);
        if (order != null) {
            if (!userId.equals(order.getUserId())) return Result.fail("订单不存在");
            return Result.ok(new OrderStatusDTO(orderId.toString(), "CREATED", "订单已创建", order.getStatus()));
        }
        Map<Object, Object> reservation = redis.opsForHash().entries(RESERVATION_PREFIX + orderId);
        if (!userId.toString().equals(reservation.get("userId"))) return Result.fail("订单不存在");
        String state = Objects.toString(reservation.get("state"), "PROCESSING");
        String message = Objects.toString(reservation.get("message"), "订单处理中");
        return Result.ok(new OrderStatusDTO(orderId.toString(), state, message, null));
    }
}
