SET NAMES utf8mb4;

-- 2026-10-24 P1 trade migration.
-- Applies to the c99e0b1 baseline. Run through scripts/migrate.mjs, not by restarting the container.

ALTER TABLE tb_seckill_voucher
    ADD COLUMN merchant_id BIGINT UNSIGNED NULL AFTER voucher_id,
    ADD COLUMN total_stock INT NOT NULL DEFAULT 0 AFTER stock,
    ADD COLUMN publish_status VARCHAR(16) NOT NULL DEFAULT 'INITIALIZING' AFTER total_stock,
    ADD COLUMN publish_version INT NOT NULL DEFAULT 0 AFTER publish_status,
    ADD COLUMN redeem_begin_time DATETIME NULL AFTER end_time,
    ADD COLUMN redeem_end_time DATETIME NULL AFTER redeem_begin_time,
    ADD COLUMN publish_request_key VARCHAR(64) NULL,
    ADD COLUMN publish_request_hash CHAR(64) NULL;

UPDATE tb_seckill_voucher sv
JOIN tb_voucher v ON v.id = sv.voucher_id
JOIN tb_shop s ON s.id = v.shop_id
SET sv.merchant_id = s.merchant_id,
    sv.total_stock = sv.stock + (
        SELECT COUNT(*) FROM tb_voucher_order o
        WHERE o.voucher_id = sv.voucher_id AND o.status IN (1, 2, 3)
    ),
    sv.redeem_begin_time = COALESCE(sv.redeem_begin_time, sv.begin_time),
    sv.redeem_end_time = COALESCE(sv.redeem_end_time, sv.end_time),
    sv.publish_status = 'ACTIVE',
    sv.publish_version = 1;

ALTER TABLE tb_seckill_voucher
    MODIFY COLUMN merchant_id BIGINT UNSIGNED NOT NULL,
    ADD UNIQUE KEY uk_activity_request (merchant_id, publish_request_key),
    ADD KEY idx_activity_status (publish_status, merchant_id),
    ADD CONSTRAINT chk_total_stock_nonnegative CHECK (total_stock >= 0),
    ADD CONSTRAINT chk_redeem_window CHECK (
        redeem_begin_time IS NULL OR redeem_end_time IS NULL OR redeem_begin_time < redeem_end_time
    );

ALTER TABLE tb_voucher_order
    ADD COLUMN shop_id BIGINT UNSIGNED NULL AFTER voucher_id,
    ADD COLUMN pay_amount BIGINT UNSIGNED NULL AFTER pay_type,
    ADD COLUMN redeem_begin_time DATETIME NULL AFTER pay_time,
    ADD COLUMN redeem_end_time DATETIME NULL AFTER redeem_begin_time,
    ADD COLUMN pay_deadline_at DATETIME NULL AFTER redeem_end_time,
    ADD COLUMN cancel_time DATETIME NULL AFTER pay_deadline_at,
    ADD COLUMN cancel_reason VARCHAR(64) NULL AFTER cancel_time,
    ADD COLUMN redeem_token VARCHAR(64) NULL AFTER cancel_reason,
    ADD COLUMN version INT NOT NULL DEFAULT 0 AFTER redeem_token;

UPDATE tb_voucher_order o
JOIN tb_voucher v ON v.id = o.voucher_id
JOIN tb_seckill_voucher sv ON sv.voucher_id = o.voucher_id
SET o.shop_id = v.shop_id,
    o.pay_amount = v.pay_value,
    o.redeem_begin_time = sv.redeem_begin_time,
    o.redeem_end_time = sv.redeem_end_time,
    o.pay_deadline_at = LEAST(DATE_ADD(o.create_time, INTERVAL 15 MINUTE), sv.redeem_end_time);

ALTER TABLE tb_voucher_order
    MODIFY COLUMN shop_id BIGINT UNSIGNED NOT NULL,
    MODIFY COLUMN pay_amount BIGINT UNSIGNED NOT NULL,
    MODIFY COLUMN pay_deadline_at DATETIME NOT NULL,
    DROP INDEX uk_order_user_voucher,
    DROP INDEX idx_order_user_time,
    ADD COLUMN active_flag TINYINT
        GENERATED ALWAYS AS (CASE WHEN status = 4 THEN NULL ELSE 1 END) STORED,
    ADD UNIQUE KEY uk_order_active (user_id, voucher_id, active_flag),
    ADD UNIQUE KEY uk_order_redeem_token (redeem_token),
    ADD KEY idx_order_due (status, pay_deadline_at, id),
    ADD KEY idx_order_shop (shop_id, create_time, id),
    ADD KEY idx_order_user_time (user_id, create_time, id);

ALTER TABLE tb_audit_log
    ADD COLUMN operator_type VARCHAR(16) NOT NULL DEFAULT 'USER' AFTER operator_user_id,
    ADD KEY idx_audit_shop_time (shop_id, create_time);

CREATE TABLE IF NOT EXISTS tb_outbox (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    event_id VARCHAR(96) NOT NULL,
    aggregate_type VARCHAR(32) NOT NULL,
    aggregate_id VARCHAR(64) NOT NULL,
    aggregate_version INT NOT NULL,
    event_type VARCHAR(40) NOT NULL,
    payload JSON NOT NULL,
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    lease_until DATETIME(3) NULL,
    last_error VARCHAR(500) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    completed_at DATETIME(3) NULL,
    UNIQUE KEY uk_outbox_event (event_id),
    UNIQUE KEY uk_outbox_transition (aggregate_type, aggregate_id, aggregate_version, event_type),
    KEY idx_outbox_due (delivery_status, next_attempt_at, id),
    KEY idx_outbox_lease (delivery_status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
