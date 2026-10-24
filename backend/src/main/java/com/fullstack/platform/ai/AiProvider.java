package com.fullstack.platform.ai;

/** Small provider SPI so a real model can be swapped without changing business code. */
public interface AiProvider {
    boolean ready();
    String mode();
    String reason();
    /** Identifies the vectors written by this provider; incompatible vectors must not be mixed. */
    String embeddingModel();
    float[] embed(String text);
    String chat(String systemPrompt, String userPrompt);
}
