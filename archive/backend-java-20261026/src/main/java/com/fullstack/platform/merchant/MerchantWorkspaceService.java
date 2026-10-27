package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.web.server.ResponseStatusException;

import java.util.List;

/** Resolves merchant and shop access from the authenticated user, never from the client. */
@Service
public class MerchantWorkspaceService {
    private final JdbcTemplate jdbc;

    public MerchantWorkspaceService(JdbcTemplate jdbc) {
        this.jdbc = jdbc;
    }

    public Workspace workspace() {
        UserDTO user = UserHolder.getUser();
        if (user == null || user.getId() == null) {
            throw new ResponseStatusException(HttpStatus.UNAUTHORIZED, "请先登录");
        }
        List<Membership> memberships = jdbc.query("""
                SELECT mm.id, mm.merchant_id, mm.role_code, m.name
                FROM tb_merchant_member mm
                JOIN tb_merchant m ON m.id = mm.merchant_id
                WHERE mm.user_id = ? AND mm.status = 'ACTIVE' AND m.status = 'ACTIVE'
                ORDER BY mm.merchant_id
                """, (rs, rowNum) -> new Membership(rs.getLong("id"), rs.getLong("merchant_id"),
                rs.getString("role_code"), rs.getString("name")), user.getId());
        return new Workspace(memberships.stream().map(member -> {
            List<ShopSummary> shops = jdbc.query("""
                    SELECT s.id, s.name, s.address FROM tb_shop s
                    WHERE s.merchant_id = ?
                    AND (? IN ('OWNER', 'MANAGER') OR EXISTS (
                        SELECT 1 FROM tb_member_shop_scope scope
                        WHERE scope.member_id = ? AND scope.shop_id = s.id
                    ))
                    ORDER BY s.id
                    """, (rs, rowNum) -> new ShopSummary(Long.toString(rs.getLong("id")),
                    rs.getString("name"), rs.getString("address")),
                    member.merchantId(), member.role(), member.id());
            return new MerchantSummary(Long.toString(member.merchantId()), member.name(), member.role(), shops);
        }).toList());
    }

    private record Membership(long id, long merchantId, String role, String name) { }
    public record ShopSummary(String id, String name, String address) { }
    public record MerchantSummary(String id, String name, String role, List<ShopSummary> shops) { }
    public record Workspace(List<MerchantSummary> merchants) { }
}
