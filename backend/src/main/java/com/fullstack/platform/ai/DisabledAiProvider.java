package com.fullstack.platform.ai;

public class DisabledAiProvider implements AiProvider {
    private final String reason;

    public DisabledAiProvider(String reason) { this.reason = reason; }

    @Override public boolean ready() { return false; }
    @Override public String mode() { return "disabled"; }
    @Override public String reason() { return reason; }
    @Override public float[] embed(String text) { throw new AiProviderException(reason); }
    @Override public String chat(String systemPrompt, String userPrompt) { throw new AiProviderException(reason); }
}
