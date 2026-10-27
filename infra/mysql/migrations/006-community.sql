SET NAMES utf8mb4;

-- Community facts and reliable delivery. Blog rows already exist from 001; the
-- columns below let a note carry a publication version and the follow/like
-- tables keep one row per pair so a relation can be re-activated with a version
-- instead of being deleted and recreated.

ALTER TABLE tb_blog
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'PUBLISHED' AFTER content,
    ADD COLUMN publish_version INT NOT NULL DEFAULT 1 AFTER status;

-- One row per (note, user). active=0 keeps the historical fact, and version is
-- the fence applied by the projection worker.
CREATE TABLE IF NOT EXISTS tb_blog_like (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    blog_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    active TINYINT NOT NULL DEFAULT 1,
    version INT NOT NULL DEFAULT 1,
    create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_blog_like (blog_id, user_id),
    KEY idx_blog_like_user (user_id, active)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- tb_follow predates this migration; make the pair unique so follow/unfollow is
-- an update of one row instead of an append.
ALTER TABLE tb_follow
    ADD COLUMN active TINYINT NOT NULL DEFAULT 1 AFTER follow_user_id,
    ADD COLUMN version INT NOT NULL DEFAULT 1 AFTER active,
    ADD COLUMN update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP AFTER create_time,
    ADD UNIQUE KEY uk_follow (user_id, follow_user_id),
    ADD KEY idx_follow_target (follow_user_id, active);

-- Community events use their own outbox: transaction order events stay owned by
-- the trade relay, and Kafka delivery can be drained or paused independently.
CREATE TABLE IF NOT EXISTS tb_community_outbox (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    event_id VARCHAR(96) NOT NULL,
    aggregate_type VARCHAR(32) NOT NULL,
    aggregate_id VARCHAR(64) NOT NULL,
    aggregate_version INT NOT NULL,
    event_type VARCHAR(40) NOT NULL,
    payload JSON NOT NULL,
    delivery_status VARCHAR(16) NOT NULL DEFAULT 'PENDING',
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    lease_until TIMESTAMP(3) NULL,
    last_error VARCHAR(500) NULL,
    completed_at TIMESTAMP(3) NULL,
    create_time TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uk_community_event (event_id),
    KEY idx_community_claim (delivery_status, next_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
