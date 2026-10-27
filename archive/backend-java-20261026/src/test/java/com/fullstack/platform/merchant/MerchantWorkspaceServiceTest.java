package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.utils.UserHolder;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DriverManagerDataSource;
import org.springframework.web.server.ResponseStatusException;

import java.util.UUID;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class MerchantWorkspaceServiceTest {
    private MerchantWorkspaceService service;
    private JdbcTemplate jdbc;

    @BeforeEach
    void setUp() {
        jdbc = new JdbcTemplate(new DriverManagerDataSource(
                "jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1", "sa", ""));
        jdbc.execute("CREATE TABLE tb_merchant (id BIGINT PRIMARY KEY, name VARCHAR(128), status VARCHAR(16))");
        jdbc.execute("CREATE TABLE tb_merchant_member (id BIGINT PRIMARY KEY, merchant_id BIGINT, user_id BIGINT, role_code VARCHAR(16), status VARCHAR(16))");
        jdbc.execute("CREATE TABLE tb_shop (id BIGINT PRIMARY KEY, merchant_id BIGINT, name VARCHAR(128), address VARCHAR(255))");
        jdbc.execute("CREATE TABLE tb_member_shop_scope (member_id BIGINT, shop_id BIGINT)");
        jdbc.update("INSERT INTO tb_merchant VALUES (10, 'A', 'ACTIVE'), (20, 'B', 'ACTIVE')");
        jdbc.update("INSERT INTO tb_merchant_member VALUES (1, 10, 100, 'OWNER', 'ACTIVE'), (2, 10, 200, 'STAFF', 'ACTIVE')");
        jdbc.update("INSERT INTO tb_shop VALUES (101, 10, 'A1', 'one'), (102, 10, 'A2', 'two'), (201, 20, 'B1', 'three')");
        jdbc.update("INSERT INTO tb_member_shop_scope VALUES (2, 101), (2, 201)");
        service = new MerchantWorkspaceService(jdbc);
    }

    @AfterEach
    void tearDown() {
        UserHolder.removeUser();
    }

    @Test
    void ownerSeesOnlyShopsOfTheirOwnMerchant() {
        signIn(100L);
        var merchants = service.workspace().merchants();
        assertThat(merchants).hasSize(1);
        assertThat(merchants.getFirst().shops()).extracting(MerchantWorkspaceService.ShopSummary::id)
                .containsExactly("101", "102");
    }

    @Test
    void staffScopeCannotGrantAccessToAnotherMerchant() {
        signIn(200L);
        var shops = service.workspace().merchants().getFirst().shops();
        assertThat(shops).extracting(MerchantWorkspaceService.ShopSummary::id).containsExactly("101");
    }

    @Test
    void disabledMembershipDoesNotGrantAccess() {
        jdbc.update("UPDATE tb_merchant_member SET status='DISABLED' WHERE user_id=100");
        signIn(100L);
        assertThat(service.workspace().merchants()).isEmpty();
    }

    @Test
    void userWithoutMembershipHasNoMerchantWorkspace() {
        signIn(300L);
        assertThat(service.workspace().merchants()).isEmpty();
    }

    @Test
    void anonymousUserCannotResolveMerchantData() {
        assertThatThrownBy(service::workspace).isInstanceOf(ResponseStatusException.class);
    }

    private void signIn(Long id) {
        UserDTO user = new UserDTO();
        user.setId(id);
        UserHolder.saveUser(user);
    }
}
