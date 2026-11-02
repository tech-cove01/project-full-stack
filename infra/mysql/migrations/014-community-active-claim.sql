SET NAMES utf8mb4;

-- The active bucket preserves global id order while skipping completed/review
-- history. Due/retry/lease predicates and SKIP LOCKED remain in the application.
-- Apply before the relay version that FORCEs this index. Older binaries remain
-- compatible with this additive schema; roll the binary back before any drop.
ALTER TABLE tb_community_outbox
    ADD COLUMN claim_active TINYINT GENERATED ALWAYS AS
        (delivery_status IN ('PENDING', 'PROCESSING')) VIRTUAL,
    ADD KEY idx_community_active_claim (claim_active, id);
