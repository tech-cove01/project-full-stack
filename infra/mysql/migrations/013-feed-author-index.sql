SET NAMES utf8mb4;

-- Narrow Feed candidates by author and publication state before considering
-- its (create_time,id) page order. Keep 007's status-first index for other plans.
-- This is not a covering index: note content/images and shop data stay out.
ALTER TABLE tb_blog
    ADD KEY idx_blog_author_feed (user_id, status, create_time, id);
