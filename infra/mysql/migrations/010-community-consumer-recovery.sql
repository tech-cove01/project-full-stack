SET NAMES utf8mb4;

-- One control row per independently rebuildable projection. Applying events
-- takes a shared current lock; a snapshot rebuild takes the exclusive lock.
CREATE TABLE IF NOT EXISTS tb_community_projection_control (
    projection VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    rebuilt_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT IGNORE INTO tb_community_projection_control(projection) VALUES ('follow-v1'),('feed-v1');

-- A failed record is a durable alternative to applying the effect, never an
-- in-memory dead letter. Source coordinates and content are immutable.
CREATE TABLE IF NOT EXISTS tb_community_consumer_failure (
    projection VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    topic VARCHAR(249) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    partition_id INT NOT NULL,
    source_offset BIGINT NOT NULL,
    message_hash BINARY(32) NOT NULL,
    message_key VARBINARY(1024) NOT NULL,
    message_body MEDIUMBLOB NOT NULL,
    message_headers JSON NOT NULL,
    reason VARCHAR(32) NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'PARKED',
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    resolved_at DATETIME(3) NULL,
    PRIMARY KEY(projection,topic,partition_id,source_offset),
    KEY idx_consumer_failure_state(projection,state,created_at),
    CONSTRAINT chk_consumer_source CHECK(partition_id>=0 AND source_offset>=0),
    CONSTRAINT chk_consumer_failure_state CHECK(state IN ('PARKED','RESOLVED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
