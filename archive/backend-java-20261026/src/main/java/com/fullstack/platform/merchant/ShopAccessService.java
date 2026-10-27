package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.web.server.ResponseStatusException;

import java.util.List;

/** Resolves merchant roles and shop scope from the authenticated user, never from request fields. */
@Service
public class ShopAccessService {
    private final JdbcTemplate jdbc;

    public ShopAccessService(JdbcTemplate jdbc) {
        this.jdbc = jdbc;
    }

    public record Membership(long memberId, long merchantId, String role) {
        public boolean manager() {
            return "OWNER".equals(role) || "MANAGER".equals(role);
        }
    }

    public UserDTO requireUser() {
        UserDTO user = UserHolder.getUser();
        if (user == null || user.getId() == null) {
            throw new ResponseStatusException(HttpStatus.UNAUTHORIZED, "请先登录");
        }
        return user;
    }

    /** Returns the membership that grants access to the shop, or 403. */
    public Membership membershipForShop(long shopId, long userId) {
        List<Membership> rows = jdbc.query("""
                SELECT mm.id, s.merchant_id, mm.role_code
                FROM tb_shop s
                JOIN tb_merchant m ON m.id = s.merchant_id AND m.status = 'ACTIVE'
                JOIN tb_merchant_member mm ON mm.merchant_id = s.merchant_id
                     AND mm.user_id = ? AND mm.status = 'ACTIVE'
                WHERE s.id = ?
                """, (rs, rowNum) -> new Membership(rs.getLong("id"), rs.getLong("merchant_id"),
                rs.getString("role_code")), userId, shopId);
        if (rows.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.FORBIDDEN, "无权操作该门店");
        }
        Membership membership = rows.getFirst();
        if ("STAFF".equals(membership.role()) && !hasScope(membership.memberId(), shopId)) {
            throw new ResponseStatusException(HttpStatus.FORBIDDEN, "门店未授权");
        }
        return membership;
    }

    public Membership requireManager(long shopId, long userId) {
        Membership membership = membershipForShop(shopId, userId);
        if (!membership.manager()) {
            throw new ResponseStatusException(HttpStatus.FORBIDDEN, "需要商家管理权限");
        }
        return membership;
    }

    public boolean hasScope(long memberId, long shopId) {
        Integer count = jdbc.queryForObject(
                "SELECT COUNT(*) FROM tb_member_shop_scope WHERE member_id = ? AND shop_id = ?",
                Integer.class, memberId, shopId);
        return count != null && count > 0;
    }

    public boolean hasManagerRole(long userId) {
        Integer count = jdbc.queryForObject("""
                SELECT COUNT(*)
                FROM tb_merchant_member mm
                JOIN tb_merchant m ON m.id = mm.merchant_id AND m.status = 'ACTIVE'
                WHERE mm.user_id = ? AND mm.status = 'ACTIVE'
                  AND mm.role_code IN ('OWNER', 'MANAGER')
                """, Integer.class, userId);
        return count != null && count > 0;
    }

    /**
     * Shop IDs the user may manage (OWNER/MANAGER only).
     * STAFF scopes are deliberately excluded: a mixed A-OWNER/B-STAFF account must not read
     * B's audit trail just because it manages some other merchant.
     */
    public List<Long> managedShopIds(long userId) {
        return jdbc.queryForList("""
                SELECT s.id
                FROM tb_shop s
                JOIN tb_merchant m ON m.id = s.merchant_id AND m.status = 'ACTIVE'
                JOIN tb_merchant_member mm ON mm.merchant_id = s.merchant_id
                     AND mm.user_id = ? AND mm.status = 'ACTIVE'
                WHERE mm.role_code IN ('OWNER', 'MANAGER')
                ORDER BY s.id
                """, Long.class, userId);
    }

    /** Shop IDs visible to the user: all shops for OWNER/MANAGER, scoped shops for STAFF. */
    public List<Long> visibleShopIds(long userId) {
        return jdbc.queryForList("""
                SELECT s.id
                FROM tb_shop s
                JOIN tb_merchant m ON m.id = s.merchant_id AND m.status = 'ACTIVE'
                JOIN tb_merchant_member mm ON mm.merchant_id = s.merchant_id
                     AND mm.user_id = ? AND mm.status = 'ACTIVE'
                WHERE mm.role_code IN ('OWNER', 'MANAGER')
                   OR EXISTS (SELECT 1 FROM tb_member_shop_scope sc
                              WHERE sc.member_id = mm.id AND sc.shop_id = s.id)
                ORDER BY s.id
                """, Long.class, userId);
    }
}
