SET NAMES utf8mb4;

-- 2026-10-26 H0/T1: durable record for orders whose DB outcome could not be confirmed.
-- This row is the only reason a message may be acknowledged without a confirmed order.
CREATE TABLE IF NOT EXISTS tb_order_review (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    order_id VARCHAR(32) NOT NULL,
    voucher_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    stream_message_id VARCHAR(64) NULL,
    attempts INT NOT NULL DEFAULT 0,
    reason VARCHAR(255) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'OPEN',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    resolved_at DATETIME(3) NULL,
    UNIQUE KEY uk_review_order (order_id),
    KEY idx_review_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
