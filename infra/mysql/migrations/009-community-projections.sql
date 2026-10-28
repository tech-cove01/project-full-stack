SET NAMES utf8mb4;

-- Independent Kafka read models. Existing community fact tables are unchanged.
-- Case-sensitive identities prevent event IDs/groups from colliding under the
-- database's default accent/case-insensitive collation.
CREATE TABLE IF NOT EXISTS tb_community_projection_event (
    projection VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    event_hash BINARY(32) NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (projection, event_id),
    CONSTRAINT chk_community_projection_state CHECK (state IN ('APPLIED','IGNORED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- One durable target state per actual relation, including inactive tombstones.
-- The reverse index supports follower lists/counts without a second mutable row.
CREATE TABLE IF NOT EXISTS tb_community_follow_projection (
    user_id BIGINT UNSIGNED NOT NULL,
    target_id BIGINT UNSIGNED NOT NULL,
    active TINYINT NOT NULL DEFAULT 0,
    version INT NOT NULL DEFAULT 0,
    event_hash BINARY(32) NOT NULL,
    last_event_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (user_id, target_id),
    KEY idx_community_follow_reverse (target_id, active, user_id),
    CONSTRAINT chk_community_follow_active CHECK (active IN (0,1)),
    CONSTRAINT chk_community_follow_version CHECK (version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Shared published-note catalog, not a per-user fan-out queue. Public-state and
-- current-follow authorization are still checked against the business facts.
CREATE TABLE IF NOT EXISTS tb_community_note_projection (
    note_id BIGINT UNSIGNED NOT NULL,
    author_id BIGINT UNSIGNED NOT NULL,
    version INT NOT NULL DEFAULT 0,
    event_hash BINARY(32) NOT NULL,
    last_event_id VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    published_at DATETIME NOT NULL,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (note_id),
    KEY idx_community_note_author_time (author_id, published_at, note_id),
    CONSTRAINT chk_community_note_version CHECK (version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
