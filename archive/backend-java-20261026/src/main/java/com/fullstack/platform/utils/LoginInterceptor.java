package com.fullstack.platform.utils;

import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.web.servlet.HandlerInterceptor;
import org.springframework.web.servlet.HandlerMapping;

public class LoginInterceptor implements HandlerInterceptor {
    @Override
    public boolean preHandle(HttpServletRequest request, HttpServletResponse response, Object handler) throws Exception {
        // Use Spring's matched route: raw URIs retain encoding and matrix parameters that
        // HandlerMapping removes, so raw-prefix checks can disagree with the routed controller.
        Object matched = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE);
        String path = matched instanceof String pattern ? pattern
                : request.getRequestURI().substring(request.getContextPath().length());
        String method = request.getMethod();
        boolean read = "GET".equals(method) || "HEAD".equals(method);
        if ("OPTIONS".equals(method) || "/error".equals(path)) return true;
        if (!read && (under(path, "/shop") || under(path, "/voucher") || under(path, "/shop-type"))) {
            return reject(response, 403, "商家写接口暂未开放");
        }
        if (!read && (under(path, "/blog") || under(path, "/follow") || under(path, "/blog-comments"))) {
            return reject(response, 403, "社区写接口暂未开放");
        }
        if (read && (under(path, "/shop") || under(path, "/voucher") || under(path, "/shop-type")
                || "/blog/hot".equals(path) || "/actuator/health".equals(path) || under(path, "/uploads"))) return true;
        if ("POST".equals(method) && ("/user/code".equals(path) || "/user/login".equals(path))) return true;
        if (UserHolder.getUser() == null) return reject(response, 401, "请先登录");
        return true;
    }

    private boolean under(String path, String root) {
        return path.equals(root) || path.startsWith(root + "/");
    }

    private boolean reject(HttpServletResponse response, int status, String message) throws Exception {
        response.setStatus(status);
        response.setContentType("application/json;charset=UTF-8");
        response.getWriter().write("{\"success\":false,\"errorMsg\":\"" + message + "\"}");
        return false;
    }
}
