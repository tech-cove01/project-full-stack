SET NAMES utf8mb4;

-- Search has an independent serialization barrier: it never widens GO-072's
-- follow/feed control rows or resets their receipts and Kafka offsets.
CREATE TABLE IF NOT EXISTS tb_note_search_control (
    name VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    index_alias VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    active_generation VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    rebuilt_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT IGNORE INTO tb_note_search_control(name) VALUES ('search-v1');

-- Bind a business version to the exact public document. ES external_gte is safe
-- to redo only when the same version carries the same canonical document.
CREATE TABLE IF NOT EXISTS tb_note_search_state (
    note_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    version INT NOT NULL,
    document_hash BINARY(32) NOT NULL,
    applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CONSTRAINT chk_note_search_version CHECK(version > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
