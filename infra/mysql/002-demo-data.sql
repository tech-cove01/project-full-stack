SET NAMES utf8mb4;

-- Synthetic local demo data. No external SMS or payment is used.
-- Keep SET NAMES at the top: the container entrypoint starts a new mysql client
-- per init file, so a POSIX locale would otherwise read UTF-8 seed text as latin1.
INSERT IGNORE INTO tb_user (id, phone, nick_name) VALUES
    (1, '13800000000', '演示店主'),
    (2, '13900000000', '演示店员');

INSERT IGNORE INTO tb_merchant (id, name) VALUES
    (1, '街区生活'), (2, '城市书店');
INSERT IGNORE INTO tb_merchant_member (id, merchant_id, user_id, role_code) VALUES
    (1, 1, 1, 'OWNER'), (2, 1, 2, 'STAFF');

INSERT IGNORE INTO tb_shop_type (id, name, icon, sort) VALUES
    (1, '咖啡', '', 1), (2, '餐饮', '', 2), (3, '书店', '', 3);

INSERT IGNORE INTO tb_shop
    (id, merchant_id, name, type_id, area, address, x, y, avg_price, score, open_hours)
VALUES
    (1, 1, '街角咖啡', 1, '示范街区', '演示路 1 号', 120.15, 30.28, 30, 48, '09:00-21:00'),
    (2, 1, '青禾食堂', 2, '示范街区', '演示路 2 号', 120.151, 30.281, 45, 47, '10:00-22:00'),
    (3, 2, '城市书房', 3, '书香街区', '演示路 3 号', 120.152, 30.282, 25, 49, '10:00-20:00');

INSERT IGNORE INTO tb_member_shop_scope (member_id, shop_id) VALUES (2, 1);

INSERT IGNORE INTO tb_voucher
    (id, shop_id, title, sub_title, rules, pay_value, actual_value, type)
VALUES
    (1001, 1, '咖啡体验券', '限量体验活动', '演示活动，每位用户限领一次。', 1900, 3000, 1),
    (1002, 2, '午间双人券', '工作日午间体验', '演示活动，每位用户限领一次。', 3900, 5900, 1);

INSERT IGNORE INTO tb_seckill_voucher (voucher_id, stock, begin_time, end_time) VALUES
    (1001, 100, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 30 DAY)),
    (1002, 50, DATE_SUB(NOW(), INTERVAL 1 DAY), DATE_ADD(NOW(), INTERVAL 30 DAY));
