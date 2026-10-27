package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
@EnableConfigurationProperties(AiProperties.class)
public class AiConfig {
    @Bean
    public AiProvider aiProvider(AiProperties properties, ObjectMapper objectMapper) {
        if (!properties.isEnabled()) {
            return new DisabledAiProvider("AI_ENABLED=false");
        }
        if ("stub".equalsIgnoreCase(properties.getProvider())) {
            return new StubAiProvider(properties);
        }
        return new OpenAiCompatibleProvider(properties, objectMapper);
    }
}
