SET NAMES utf8mb4;

-- The feed filters on status and orders by (create_time,id). Without this index
-- MySQL would sort the whole published set and then join follows, which grows
-- with the number of notes rather than with the page size.
ALTER TABLE tb_blog
    ADD KEY idx_blog_feed (status, create_time, id);
