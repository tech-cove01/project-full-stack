package com.fullstack.platform.order;

import com.fullstack.platform.entity.VoucherOrder;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Repository;

/**
 * Durable "needs review" record for accepted orders whose creation outcome is unknown.
 *
 * <p>It exists so the consumer never acknowledges a Stream message on the strength of a
 * short-lived Redis hash alone: when the database is unreachable this insert also fails and
 * the caller must keep the message pending instead of acknowledging it.
 */
@Repository
public class OrderReviewRepository {
    private final JdbcTemplate jdbc;

    public OrderReviewRepository(JdbcTemplate jdbc) {
        this.jdbc = jdbc;
    }

    /**
     * Inserts or refreshes the open review row for an order. Repeated attempts keep one row.
     * Throws when the database is unavailable: callers must treat that as "not durable yet".
     */
    public void record(VoucherOrder order, String streamMessageId, long attempts, Throwable cause) {
        String reason = cause.getClass().getSimpleName();
        if (cause.getMessage() != null && !cause.getMessage().isBlank()) {
            reason = reason + ": " + cause.getMessage();
        }
        if (reason.length() > 255) {
            reason = reason.substring(0, 255);
        }
        jdbc.update("""
                INSERT INTO tb_order_review
                    (order_id, voucher_id, user_id, stream_message_id, attempts, reason, status)
                VALUES (?, ?, ?, ?, ?, ?, 'OPEN')
                ON DUPLICATE KEY UPDATE
                    stream_message_id = VALUES(stream_message_id),
                    attempts = GREATEST(attempts, VALUES(attempts)),
                    reason = VALUES(reason),
                    -- A replayed message that failed again must become replayable again:
                    -- REQUEUED goes back to OPEN, RESOLVED keeps its terminal state.
                    status = IF(status = 'REQUEUED', 'OPEN', status),
                    updated_at = NOW(3)
                """, Long.toString(order.getId()), order.getVoucherId(), order.getUserId(),
                streamMessageId, attempts, reason);
    }

    /**
     * Terminal outcome reached: the order now exists (or was definitively rejected), so the row
     * must stop blocking reconciliation and stop being replayable.
     */
    public void resolve(long orderId) {
        jdbc.update("""
                UPDATE tb_order_review
                SET status = 'RESOLVED', resolved_at = NOW(3), updated_at = NOW(3)
                WHERE order_id = ? AND status <> 'RESOLVED'
                """, Long.toString(orderId));
    }

    public long openCount() {
        Long count = jdbc.queryForObject(
                "SELECT COUNT(*) FROM tb_order_review WHERE status = 'OPEN'", Long.class);
        return count == null ? 0L : count;
    }
}
