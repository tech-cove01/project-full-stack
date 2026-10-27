package com.fullstack.platform.ai;

import org.springframework.stereotype.Service;

@Service
public class AiCapabilityService {
    private final AiProperties properties;
    private final AiProvider provider;

    public AiCapabilityService(AiProperties properties, AiProvider provider) {
        this.properties = properties;
        this.provider = provider;
    }

    public AiCapabilities capabilities() {
        boolean enabled = properties.isEnabled();
        boolean ready = enabled && provider.ready();
        return new AiCapabilities(enabled, ready, provider.mode(),
                ready ? provider.reason() : (enabled ? provider.reason() : "AI_ENABLED=false"),
                properties.getChatModel(), properties.getEmbeddingModel(),
                properties.getEmbeddingDimensions());
    }
}
