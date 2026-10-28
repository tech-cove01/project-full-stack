SET NAMES utf8mb4;

-- Poison Kafka keys can be larger than our domain key. Parking must retain
-- the original bytes instead of requiring the same validation that rejected it.
ALTER TABLE tb_community_consumer_failure MODIFY COLUMN message_key MEDIUMBLOB NOT NULL;
