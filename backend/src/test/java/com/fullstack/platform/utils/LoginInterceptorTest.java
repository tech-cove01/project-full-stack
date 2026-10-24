package com.fullstack.platform.utils;

import com.fullstack.platform.dto.UserDTO;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import static org.junit.jupiter.api.Assertions.*;

class LoginInterceptorTest {
    private final LoginInterceptor interceptor = new LoginInterceptor();

    @AfterEach void cleanup() { UserHolder.removeUser(); }

    @ParameterizedTest
    @CsvSource({"GET,/shop/1", "GET,/voucher/list/1", "GET,/shop-type/list", "GET,/blog/hot", "GET,/actuator/health", "POST,/user/login", "POST,/user/code"})
    void permitsPublicReadsAndLogin(String method, String path) throws Exception {
        assertTrue(interceptor.preHandle(new MockHttpServletRequest(method, path), new MockHttpServletResponse(), new Object()));
    }

    @ParameterizedTest
    @CsvSource({"POST,/shop", "PUT,/shop", "POST,/voucher/seckill", "DELETE,/voucher/1"})
    void deniesMerchantWritesEvenForLoggedInUsers(String method, String path) throws Exception {
        UserDTO user = new UserDTO(); user.setId(1L); UserHolder.saveUser(user);
        MockHttpServletResponse response = new MockHttpServletResponse();
        assertFalse(interceptor.preHandle(new MockHttpServletRequest(method, path), response, new Object()));
        assertEquals(403, response.getStatus());
    }

    @ParameterizedTest
    @CsvSource({"POST,/upload/blog", "GET,/voucher-order/status/1", "GET,/merchant/workspace", "GET,/user/me"})
    void requiresLoginForPrivateOperations(String method, String path) throws Exception {
        MockHttpServletResponse response = new MockHttpServletResponse();
        assertFalse(interceptor.preHandle(new MockHttpServletRequest(method, path), response, new Object()));
        assertEquals(401, response.getStatus());
    }

    @Test void authenticatedUserCanUpload() throws Exception {
        UserDTO user = new UserDTO(); user.setId(1L); UserHolder.saveUser(user);
        assertTrue(interceptor.preHandle(new MockHttpServletRequest("POST", "/upload/blog"), new MockHttpServletResponse(), new Object()));
    }
}
