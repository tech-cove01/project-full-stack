SET NAMES utf8mb4;

-- The document row contains editable draft text. Freeze the public title on
-- the same immutable version as its body so a draft cannot alter citations.
-- Pre-migration title history is unavailable; backfill the title known now.
-- Nullable keeps old writers SQL-compatible, but new readers reject any
-- later version without a title snapshot instead of exposing a mutable draft.
ALTER TABLE tb_ai_document_version ADD COLUMN title VARCHAR(255) NULL AFTER epoch;
UPDATE tb_ai_document_version v
JOIN tb_ai_document d ON d.id=v.document_id
SET v.title=d.title
WHERE v.title IS NULL;
