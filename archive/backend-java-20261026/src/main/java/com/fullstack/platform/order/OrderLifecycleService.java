package com.fullstack.platform.order;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fullstack.platform.audit.AuditService;
import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import com.fullstack.platform.outbox.OutboxRepository;
import org.mybatis.spring.SqlSessionTemplate;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.web.server.ResponseStatusException;

import java.security.SecureRandom;
import java.util.Map;

/** State transitions for payment and cancellation; each transition also writes outbox + audit. */
@Service
public class OrderLifecycleService {
    private static final SecureRandom RANDOM = new SecureRandom();

    private final VoucherOrderMapper orders;
    private final JdbcTemplate jdbc;
    private final AuditService audit;
    private final OutboxRepository outbox;
    private final OrderQueryService query;
    private final SqlSessionTemplate sqlSession;
    private final OrderCompensationService compensation;
    private final ObjectMapper objectMapper;

    public OrderLifecycleService(VoucherOrderMapper orders, JdbcTemplate jdbc, AuditService audit,
                                 OutboxRepository outbox, OrderQueryService query,
                                 SqlSessionTemplate sqlSession, OrderCompensationService compensation,
                                 ObjectMapper objectMapper) {
        this.orders = orders;
        this.jdbc = jdbc;
        this.audit = audit;
        this.outbox = outbox;
        this.query = query;
        this.sqlSession = sqlSession;
        this.compensation = compensation;
        this.objectMapper = objectMapper;
    }

    @Transactional(rollbackFor = Exception.class)
    public OrderStatusDTO pay(long orderId, long userId) {
        VoucherOrder order = query.requireOwned(orderId, userId);
        if (Integer.valueOf(2).equals(order.getStatus())) {
            return query.toDto(order, true);
        }
        if (!Integer.valueOf(1).equals(order.getStatus())) {
            throw conflict("当前订单状态不能支付");
        }
        String token = token();
        int changed = jdbc.update("""
                UPDATE tb_voucher_order
                SET status = 2, pay_time = NOW(3), redeem_token = ?, version = version + 1
                WHERE id = ? AND user_id = ? AND status = 1 AND pay_deadline_at > NOW(3)
                """, token, orderId, userId);
        if (changed != 1) {
            sqlSession.clearCache();
            VoucherOrder current = query.requireOwned(orderId, userId);
            if (Integer.valueOf(2).equals(current.getStatus())) {
                return query.toDto(current, true);
            }
            throw conflict("已超过支付期限或订单状态已变化");
        }
        // JdbcTemplate bypasses MyBatis, so drop the local cache before reloading the row.
        sqlSession.clearCache();
        VoucherOrder updated = query.requireOwned(orderId, userId);
        long merchantId = merchantOf(updated.getShopId());
        int version = updated.getVersion() == null ? 1 : updated.getVersion();
        audit.record(merchantId, updated.getShopId(), userId, "USER", "ORDER_PAID",
                Long.toString(orderId), Map.of("orderStatus", 2, "version", version));
        enqueue(updated, version, 0, false);
        return query.toDto(updated, true);
    }

    @Transactional(rollbackFor = Exception.class)
    public OrderStatusDTO cancel(long orderId, long userId, String reason) {
        VoucherOrder order = query.requireOwned(orderId, userId);
        if (Integer.valueOf(4).equals(order.getStatus())) {
            return query.toDto(order, false);
        }
        if (!Integer.valueOf(1).equals(order.getStatus())) {
            throw conflict("当前订单状态不能取消");
        }
        cancelInternal(order, userId, "USER", sanitize(reason, "USER_CANCEL"), true);
        return query.toDto(query.requireOwned(orderId, userId), false);
    }

    /** Called by the timeout scanner; the DB deadline is the authority. */
    @Transactional(rollbackFor = Exception.class)
    public void cancelExpired(long orderId) {
        VoucherOrder order = orders.selectById(orderId);
        if (order == null || !Integer.valueOf(1).equals(order.getStatus())) {
            return;
        }
        cancelInternal(order, 0L, "SYSTEM", "PAY_TIMEOUT", false);
    }

    private void cancelInternal(VoucherOrder order, long operatorId, String operatorType,
                                String reason, boolean requireOwner) {
        String sql = """
                UPDATE tb_voucher_order
                SET status = 4, cancel_time = NOW(3), cancel_reason = ?, version = version + 1
                WHERE id = ? AND status = 1
                """ + (requireOwner ? " AND user_id = ?" : " AND pay_deadline_at <= NOW(3)");
        Object[] args = requireOwner
                ? new Object[] { reason, order.getId(), operatorId }
                : new Object[] { reason, order.getId() };
        int changed = jdbc.update(sql, args);
        if (changed != 1) {
            sqlSession.clearCache();
            VoucherOrder current = orders.selectById(order.getId());
            if (current != null && Integer.valueOf(4).equals(current.getStatus())) {
                return;
            }
            throw conflict("订单状态已变化，无法取消");
        }
        jdbc.update("UPDATE tb_seckill_voucher SET stock = stock + 1 "
                + "WHERE voucher_id = ? AND stock < total_stock", order.getVoucherId());
        sqlSession.clearCache();
        VoucherOrder updated = orders.selectById(order.getId());
        int version = updated.getVersion() == null ? 1 : updated.getVersion();
        long merchantId = merchantOf(updated.getShopId());
        audit.record(merchantId, updated.getShopId(), operatorId, operatorType, "ORDER_CANCELLED",
                Long.toString(order.getId()), Map.of("orderStatus", 4, "reason", reason, "version", version));
        enqueue(updated, version, 1, true);
    }

    /** Redeem has no Redis stock effect, but its transition is still traceable in the outbox. */
    public void publishTransition(VoucherOrder order, int version) {
        enqueue(order, version, 0, false);
    }

    /** Refund restores stock but deliberately keeps the purchase marker and accepted mapping. */
    public void publishRefund(VoucherOrder order, int version) {
        enqueue(order, version, 1, false);
    }

    private void enqueue(VoucherOrder order, int version, int stockRestore, boolean releaseEligibility) {
        String eventId = "ORDER:" + order.getId() + ":" + version + ":CHANGED";
        Map<String, Object> payload = Map.of(
                "orderId", Long.toString(order.getId()),
                "voucherId", Long.toString(order.getVoucherId()),
                "shopId", Long.toString(order.getShopId()),
                "userId", Long.toString(order.getUserId()),
                "targetStatus", Integer.toString(order.getStatus()),
                "stockRestore", Integer.toString(stockRestore),
                "releaseEligibility", Boolean.toString(releaseEligibility),
                "version", Integer.toString(version));
        outbox.enqueue(eventId, "ORDER", Long.toString(order.getId()), version, "ORDER_CHANGED", payload);
        if (stockRestore == 0) {
            return;
        }
        // Best effort after commit: the API response then reflects a usable purchase marker.
        // The outbox row remains the source of truth if this call fails.
        String json;
        try {
            json = objectMapper.writeValueAsString(payload);
        } catch (JsonProcessingException e) {
            return;
        }
        Runnable apply = () -> {
            try {
                compensation.apply(eventId, json);
            } catch (RuntimeException ignored) {
                // The outbox worker retries with the same event ID.
            }
        };
        if (TransactionSynchronizationManager.isSynchronizationActive()) {
            TransactionSynchronizationManager.registerSynchronization(new TransactionSynchronization() {
                @Override
                public void afterCommit() {
                    apply.run();
                }
            });
        } else {
            apply.run();
        }
    }

    private long merchantOf(Long shopId) {
        Long merchantId = jdbc.queryForObject("SELECT merchant_id FROM tb_shop WHERE id = ?", Long.class, shopId);
        return merchantId == null ? 0L : merchantId;
    }

    private static String token() {
        byte[] bytes = new byte[32];
        RANDOM.nextBytes(bytes);
        return java.util.HexFormat.of().formatHex(bytes);
    }

    private static String sanitize(String value, String fallback) {
        if (value == null || value.isBlank()) return fallback;
        String trimmed = value.trim();
        return trimmed.length() > 64 ? trimmed.substring(0, 64) : trimmed;
    }

    private static ResponseStatusException conflict(String message) {
        return new ResponseStatusException(HttpStatus.CONFLICT, message);
    }
}
