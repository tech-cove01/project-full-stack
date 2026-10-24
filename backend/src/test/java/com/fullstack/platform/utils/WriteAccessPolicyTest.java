package com.fullstack.platform.utils;

import com.fullstack.platform.controller.BlogController;
import com.fullstack.platform.controller.FollowController;
import com.fullstack.platform.controller.ShopController;
import com.fullstack.platform.controller.VoucherController;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.entity.Blog;
import com.fullstack.platform.entity.Shop;
import com.fullstack.platform.entity.Voucher;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.http.HttpStatus;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import java.net.URI;
import static org.junit.jupiter.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

class WriteAccessPolicyTest {
    private MockMvc mvc;

    @BeforeEach void setup() {
        UserDTO user = new UserDTO(); user.setId(1L); UserHolder.saveUser(user);
        // Probe handlers deliberately allow writes. The interceptor alone must prevent execution.
        mvc = MockMvcBuilders.standaloneSetup(new WriteProbe())
                .addInterceptors(new LoginInterceptor()).build();
    }

    @AfterEach void cleanup() { UserHolder.removeUser(); }

    @ParameterizedTest
    @ValueSource(strings = {"/shop", "/shop;demo=1", "/%73hop", "/voucher/seckill", "/voucher;demo=1/seckill", "/%76oucher/seckill", "/blog", "/blog;demo=1", "/%62log"})
    void normalizedPostRoutesStayClosedForAuthenticatedUsers(String path) throws Exception {
        mvc.perform(post(URI.create(path))).andExpect(status().isForbidden());
    }

    @ParameterizedTest
    @ValueSource(strings = {"/follow/2/true", "/follow;demo=1/2/true", "/%66ollow/2/true", "/blog/like/1", "/blog;demo=1/like/1", "/%62log/like/1"})
    void normalizedPutRoutesStayClosedForAuthenticatedUsers(String path) throws Exception {
        mvc.perform(put(URI.create(path))).andExpect(status().isForbidden());
    }

    @Test void merchantControllersAlsoRejectWritesWithoutAnInterceptor() {
        assertForbidden(() -> new ShopController().saveShop(new Shop()));
        assertForbidden(() -> new ShopController().updateShop(new Shop()));
        assertForbidden(() -> new VoucherController().addVoucher(new Voucher()));
        assertForbidden(() -> new VoucherController().addSeckillVoucher(new Voucher()));
    }

    @Test void communityControllersAlsoRejectWritesWithoutAnInterceptor() {
        assertForbidden(() -> new BlogController().saveBlog(new Blog().setId(Long.MAX_VALUE).setLiked(Integer.MAX_VALUE)));
        assertForbidden(() -> new BlogController().likeBlog(1L));
        assertForbidden(() -> new FollowController().follow(2L, true));
    }

    private void assertForbidden(org.junit.jupiter.api.function.Executable action) {
        assertEquals(HttpStatus.FORBIDDEN, assertThrows(ResponseStatusException.class, action).getStatusCode());
    }

    @RestController
    static class WriteProbe {
        @PostMapping({"/shop", "/voucher/seckill", "/blog"}) String postWrite() { return "MUST NOT RUN"; }
        @PutMapping({"/follow/{id}/{isFollow}", "/blog/like/{id}"}) String putWrite() { return "MUST NOT RUN"; }
    }
}
