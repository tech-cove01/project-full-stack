SET NAMES utf8mb4;

-- P1-D AI knowledge store. Drafts, immutable published versions, chunks and vectors.
CREATE TABLE IF NOT EXISTS tb_ai_document (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    seed_key VARCHAR(64) NULL,
    scope VARCHAR(16) NOT NULL,
    shop_id BIGINT UNSIGNED NULL,
    title VARCHAR(255) NOT NULL,
    body TEXT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
    published_version INT NULL,
    epoch BIGINT NOT NULL DEFAULT 0,
    disabled TINYINT NOT NULL DEFAULT 0,
    created_by BIGINT UNSIGNED NULL,
    updated_by BIGINT UNSIGNED NULL,
    create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_ai_seed_key (seed_key),
    KEY idx_ai_document_scope (scope, shop_id, disabled, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tb_ai_document_version (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    document_id BIGINT UNSIGNED NOT NULL,
    version INT NOT NULL,
    epoch BIGINT NOT NULL,
    body MEDIUMTEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'INDEXING',
    embedding_model VARCHAR(64) NULL,
    embedding_dimension INT NULL,
    error VARCHAR(500) NULL,
    create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ready_time TIMESTAMP NULL,
    UNIQUE KEY uk_ai_version (document_id, version),
    KEY idx_ai_version_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tb_ai_chunk (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    document_id BIGINT UNSIGNED NOT NULL,
    version INT NOT NULL,
    epoch BIGINT NOT NULL,
    ordinal INT NOT NULL,
    content TEXT NOT NULL,
    embedding JSON NOT NULL,
    embedding_model VARCHAR(64) NOT NULL,
    embedding_dimension INT NOT NULL,
    create_time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_ai_chunk (document_id, version, ordinal),
    KEY idx_ai_chunk_doc (document_id, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
