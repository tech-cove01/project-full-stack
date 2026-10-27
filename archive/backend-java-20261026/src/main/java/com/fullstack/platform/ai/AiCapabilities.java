package com.fullstack.platform.ai;

public record AiCapabilities(boolean enabled, boolean ready, String mode, String reason,
                             String chatModel, String embeddingModel, int embeddingDimensions) {
}
