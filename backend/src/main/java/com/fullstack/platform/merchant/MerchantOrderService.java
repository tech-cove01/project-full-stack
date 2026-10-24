package com.fullstack.platform.merchant;

import com.fullstack.platform.audit.AuditService;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import com.fullstack.platform.order.OrderLifecycleService;
import org.mybatis.spring.SqlSessionTemplate;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.web.server.ResponseStatusException;

import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;

/** Merchant order list, redemption and simulated refund. All scope checks happen server-side. */
@Service
public class MerchantOrderService {
    private static final DateTimeFormatter DISPLAY = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    private final JdbcTemplate jdbc;
    private final VoucherOrderMapper orders;
    private final ShopAccessService access;
    private final AuditService audit;
    private final OrderLifecycleService lifecycle;
    private final SqlSessionTemplate sqlSession;

    public MerchantOrderService(JdbcTemplate jdbc, VoucherOrderMapper orders, ShopAccessService access,
                                AuditService audit, OrderLifecycleService lifecycle,
                                SqlSessionTemplate sqlSession) {
        this.jdbc = jdbc;
        this.orders = orders;
        this.access = access;
        this.audit = audit;
        this.lifecycle = lifecycle;
        this.sqlSession = sqlSession;
    }

    public PageResult<MerchantOrderView> list(int current, int pageSize, String shopId,
                                              String status, String orderId) {
        UserDTO user = access.requireUser();
        List<Long> visible = access.visibleShopIds(user.getId());
        if (visible.isEmpty()) {
            return new PageResult<>(List.of(), 0L);
        }
        StringBuilder where = new StringBuilder(" WHERE o.shop_id IN ("
                + String.join(",", Collections.nCopies(visible.size(), "?")) + ")");
        List<Object> args = new ArrayList<>(visible);
        if (shopId != null && !shopId.isBlank()) {
            long requested = positiveLong(shopId, "shopId");
            if (!visible.contains(requested)) {
                return new PageResult<>(List.of(), 0L);
            }
            where.append(" AND o.shop_id = ?");
            args.add(requested);
        }
        if (status != null && !status.isBlank()) {
            int parsed;
            try {
                parsed = Integer.parseInt(status.trim());
            } catch (NumberFormatException e) {
                throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "未知订单状态");
            }
            if (!List.of(1, 2, 3, 4, 6).contains(parsed)) {
                throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "未知订单状态");
            }
            where.append(" AND o.status = ?");
            args.add(parsed);
        }
        if (orderId != null && !orderId.isBlank()) {
            where.append(" AND o.id = ?");
            args.add(positiveLong(orderId, "orderId"));
        }
        String from = """
                 FROM tb_voucher_order o
                 JOIN tb_shop s ON s.id = o.shop_id
                 JOIN tb_user u ON u.id = o.user_id
                 JOIN tb_voucher v ON v.id = o.voucher_id
                """ + where;
        Long total = jdbc.queryForObject("SELECT COUNT(*)" + from, Long.class, args.toArray());
        int page = Math.max(current, 1);
        int size = Math.min(Math.max(pageSize, 1), 50);
        args.add(size);
        args.add((page - 1) * size);
        List<MerchantOrderView> items = jdbc.query("""
                        SELECT o.id, o.shop_id, s.name AS shop_name, o.user_id, u.phone,
                               o.voucher_id, v.title AS voucher_title, o.status, o.pay_amount,
                               o.create_time, o.pay_time, o.use_time, o.refund_time, o.redeem_end_time
                        """ + from + " ORDER BY o.create_time DESC, o.id DESC LIMIT ? OFFSET ?",
                ROW_MAPPER, args.toArray());
        return new PageResult<>(items, total == null ? 0L : total);
    }

    @Transactional(rollbackFor = Exception.class)
    public RedeemResult redeem(String shopId, String token) {
        UserDTO user = access.requireUser();
        long requestedShop = positiveLong(shopId, "shopId");
        if (token == null || !token.matches("[a-fA-F0-9]{64}")) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "核销凭证无效");
        }
        VoucherOrder order = orders.selectOne(new com.baomidou.mybatisplus.core.conditions.query.QueryWrapper<VoucherOrder>()
                .eq("redeem_token", token));
        if (order == null || !Long.valueOf(requestedShop).equals(order.getShopId())) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "核销凭证无效");
        }
        ShopAccessService.Membership membership = access.membershipForShop(order.getShopId(), user.getId());
        if (Integer.valueOf(3).equals(order.getStatus())) {
            return new RedeemResult(viewFor(order.getId()), true);
        }
        if (!Integer.valueOf(2).equals(order.getStatus())) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "订单当前状态不可核销");
        }
        int changed = jdbc.update("""
                UPDATE tb_voucher_order
                SET status = 3, use_time = NOW(3), version = version + 1
                WHERE id = ? AND status = 2
                  AND redeem_begin_time <= NOW(3) AND redeem_end_time > NOW(3)
                """, order.getId());
        if (changed != 1) {
            sqlSession.clearCache();
            VoucherOrder current = orders.selectById(order.getId());
            if (current != null && Integer.valueOf(3).equals(current.getStatus())) {
                return new RedeemResult(viewFor(order.getId()), true);
            }
            throw new ResponseStatusException(HttpStatus.CONFLICT, "不在核销期或订单状态已变化");
        }
        sqlSession.clearCache();
        VoucherOrder updated = orders.selectById(order.getId());
        int version = updated.getVersion() == null ? 1 : updated.getVersion();
        audit.record(membership.merchantId(), updated.getShopId(), user.getId(), "MERCHANT",
                "ORDER_REDEEMED", Long.toString(updated.getId()),
                Map.of("orderStatus", 3, "version", version));
        lifecycle.publishTransition(updated, version);
        return new RedeemResult(viewFor(updated.getId()), false);
    }

    @Transactional(rollbackFor = Exception.class)
    public MerchantOrderView refund(long orderId, String reason) {
        UserDTO user = access.requireUser();
        VoucherOrder order = orders.selectById(orderId);
        if (order == null) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "订单不存在");
        }
        ShopAccessService.Membership membership = access.requireManager(order.getShopId(), user.getId());
        if (Integer.valueOf(6).equals(order.getStatus())) {
            return viewFor(orderId);
        }
        if (!Integer.valueOf(2).equals(order.getStatus())) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "只有已支付且未核销的订单可以退款");
        }
        String safeReason = reason == null || reason.isBlank() ? "MERCHANT_REFUND" : reason.trim();
        if (safeReason.length() > 64) safeReason = safeReason.substring(0, 64);
        int changed = jdbc.update("""
                UPDATE tb_voucher_order
                SET status = 6, refund_time = NOW(3), version = version + 1
                WHERE id = ? AND status = 2 AND redeem_end_time > NOW(3)
                """, orderId);
        if (changed != 1) {
            sqlSession.clearCache();
            VoucherOrder current = orders.selectById(orderId);
            if (current != null && Integer.valueOf(6).equals(current.getStatus())) {
                return viewFor(orderId);
            }
            throw new ResponseStatusException(HttpStatus.CONFLICT, "订单已核销、已过期或状态已变化");
        }
        jdbc.update("UPDATE tb_seckill_voucher SET stock = stock + 1 "
                + "WHERE voucher_id = ? AND stock < total_stock", order.getVoucherId());
        sqlSession.clearCache();
        VoucherOrder updated = orders.selectById(orderId);
        int version = updated.getVersion() == null ? 1 : updated.getVersion();
        audit.record(membership.merchantId(), updated.getShopId(), user.getId(), "MERCHANT",
                "ORDER_REFUNDED", Long.toString(orderId),
                Map.of("orderStatus", 6, "reason", safeReason, "version", version));
        lifecycle.publishRefund(updated, version);
        return viewFor(orderId);
    }

    public PageResult<AuditView> audits(int current, int pageSize, String shopId, String action) {
        UserDTO user = access.requireUser();
        if (!access.hasManagerRole(user.getId())) {
            throw new ResponseStatusException(HttpStatus.FORBIDDEN, "需要商家管理权限");
        }
        List<Long> visible = access.visibleShopIds(user.getId());
        if (visible.isEmpty()) {
            return new PageResult<>(List.of(), 0L);
        }
        StringBuilder where = new StringBuilder(" WHERE shop_id IN ("
                + String.join(",", Collections.nCopies(visible.size(), "?")) + ")");
        List<Object> args = new ArrayList<>(visible);
        if (shopId != null && !shopId.isBlank()) {
            long requested = positiveLong(shopId, "shopId");
            if (!visible.contains(requested)) {
                return new PageResult<>(List.of(), 0L);
            }
            where.append(" AND shop_id = ?");
            args.add(requested);
        }
        if (action != null && !action.isBlank()) {
            where.append(" AND action = ?");
            args.add(action);
        }
        Long total = jdbc.queryForObject("SELECT COUNT(*) FROM tb_audit_log" + where, Long.class, args.toArray());
        int page = Math.max(current, 1);
        int size = Math.min(Math.max(pageSize, 1), 50);
        args.add(size);
        args.add((page - 1) * size);
        List<AuditView> items = jdbc.query("""
                        SELECT id, shop_id, operator_user_id, operator_type, action, resource_id,
                               CAST(detail AS CHAR) AS detail, create_time
                        FROM tb_audit_log
                        """ + where + " ORDER BY id DESC LIMIT ? OFFSET ?",
                (rs, rowNum) -> new AuditView(
                        Long.toString(rs.getLong("id")),
                        rs.getObject("shop_id") == null ? null : Long.toString(rs.getLong("shop_id")),
                        Long.toString(rs.getLong("operator_user_id")),
                        rs.getString("operator_type"),
                        rs.getString("action"),
                        rs.getString("resource_id"),
                        rs.getString("detail"),
                        rs.getObject("create_time", LocalDateTime.class).format(DISPLAY)),
                args.toArray());
        return new PageResult<>(items, total == null ? 0L : total);
    }

    private MerchantOrderView viewFor(long orderId) {
        List<MerchantOrderView> rows = jdbc.query("""
                SELECT o.id, o.shop_id, s.name AS shop_name, o.user_id, u.phone,
                       o.voucher_id, v.title AS voucher_title, o.status, o.pay_amount,
                       o.create_time, o.pay_time, o.use_time, o.refund_time, o.redeem_end_time
                FROM tb_voucher_order o
                JOIN tb_shop s ON s.id = o.shop_id
                JOIN tb_user u ON u.id = o.user_id
                JOIN tb_voucher v ON v.id = o.voucher_id
                WHERE o.id = ?
                """, ROW_MAPPER, orderId);
        if (rows.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "订单不存在");
        }
        return rows.getFirst();
    }

    private static final RowMapper<MerchantOrderView> ROW_MAPPER = (rs, rowNum) -> new MerchantOrderView(
            Long.toString(rs.getLong("id")),
            Long.toString(rs.getLong("shop_id")),
            rs.getString("shop_name"),
            Long.toString(rs.getLong("user_id")),
            mask(rs.getString("phone")),
            Long.toString(rs.getLong("voucher_id")),
            rs.getString("voucher_title"),
            rs.getInt("status"),
            Long.toString(rs.getLong("pay_amount")),
            time(rs.getObject("create_time", LocalDateTime.class)),
            time(rs.getObject("pay_time", LocalDateTime.class)),
            time(rs.getObject("use_time", LocalDateTime.class)),
            time(rs.getObject("refund_time", LocalDateTime.class)),
            time(rs.getObject("redeem_end_time", LocalDateTime.class)));

    private static String time(LocalDateTime value) {
        return value == null ? null : value.format(DISPLAY);
    }

    private static String mask(String phone) {
        if (phone == null || phone.length() < 7) return phone;
        return phone.substring(0, 3) + "****" + phone.substring(phone.length() - 4);
    }

    private static long positiveLong(String value, String field) {
        try {
            long parsed = Long.parseLong(value == null ? "" : value.trim());
            if (parsed <= 0) throw new NumberFormatException();
            return parsed;
        } catch (NumberFormatException e) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "必须为正整数");
        }
    }

    public record PageResult<T>(List<T> items, long total) { }
    public record RedeemResult(MerchantOrderView order, boolean alreadyRedeemed) { }
    public record AuditView(String id, String shopId, String operatorUserId, String operatorType,
                            String action, String resourceId, String detail, String createTime) { }
}
