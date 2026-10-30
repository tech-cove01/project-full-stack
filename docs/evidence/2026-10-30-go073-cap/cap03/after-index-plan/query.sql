SELECT b.id,b.user_id,b.shop_id,s.name AS shop_name,
       b.title,b.images,b.content,b.liked,b.comments,b.create_time
FROM tb_blog b LEFT JOIN tb_shop s ON s.id=b.shop_id
WHERE b.status='PUBLISHED' AND b.user_id IN (3)
  AND EXISTS(SELECT 1 FROM tb_follow f
    WHERE f.user_id=35 AND f.follow_user_id=b.user_id AND f.active=1)
ORDER BY b.create_time DESC,b.id DESC LIMIT 20;
