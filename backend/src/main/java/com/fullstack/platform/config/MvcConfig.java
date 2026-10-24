package com.fullstack.platform.config;

import com.fullstack.platform.utils.LoginInterceptor;
import com.fullstack.platform.utils.RefreshTokenInterceptor;
import org.springframework.context.annotation.Configuration;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.web.servlet.config.annotation.InterceptorRegistry;
import org.springframework.web.servlet.config.annotation.WebMvcConfigurer;
import org.springframework.web.servlet.config.annotation.ResourceHandlerRegistry;
import org.springframework.beans.factory.annotation.Value;
import java.nio.file.Path;

import jakarta.annotation.Resource;

@Configuration
public class MvcConfig implements WebMvcConfigurer {

    @Resource
    private StringRedisTemplate stringRedisTemplate;

    @Value("${app.upload-dir:./uploads}")
    private String uploadDirectory;

    @Override
    public void addResourceHandlers(ResourceHandlerRegistry registry) {
        String location = Path.of(uploadDirectory).toAbsolutePath().normalize().toUri().toString();
        registry.addResourceHandler("/uploads/**").addResourceLocations(location.endsWith("/") ? location : location + "/");
    }

    @Override
    public void addInterceptors(InterceptorRegistry registry) {
        // Method-aware policy: reads can be public; merchant writes stay closed until RBAC is ready.
        registry.addInterceptor(new LoginInterceptor()).addPathPatterns("/**").order(1);
        // token刷新的拦截器
        registry.addInterceptor(new RefreshTokenInterceptor(stringRedisTemplate)).addPathPatterns("/**").order(0);
    }
}
