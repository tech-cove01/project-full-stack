package com.fullstack.platform.ai;

import java.util.Locale;

/**
 * Deterministic local provider for protocol, permission and retrieval tests.
 * It is explicitly reported as stub; it is not evidence of real model quality.
 */
public class StubAiProvider implements AiProvider {
    private final int dimensions;

    public StubAiProvider(AiProperties properties) {
        this.dimensions = properties.getEmbeddingDimensions();
    }

    @Override public boolean ready() { return true; }
    @Override public String mode() { return "stub"; }
    @Override public String reason() { return "deterministic stub provider; real model not configured"; }
    @Override public String embeddingModel() { return "stub-lexical"; }

    @Override
    public float[] embed(String text) {
        float[] vector = new float[dimensions];
        if (text == null || text.isBlank()) return vector;
        String normalized = text.toLowerCase(Locale.ROOT);
        for (int i = 0; i < normalized.length(); i++) {
            char c = normalized.charAt(i);
            if (isChinese(c)) {
                add(vector, "c:" + c, 1.0f);
                if (i + 1 < normalized.length() && isChinese(normalized.charAt(i + 1))) {
                    add(vector, "b:" + c + normalized.charAt(i + 1), 1.6f);
                }
            }
        }
        for (String word : normalized.split("[^a-z0-9_]+")) {
            if (word.length() >= 2) add(vector, "w:" + word, 1.2f);
        }
        double norm = 0;
        for (float value : vector) norm += value * value;
        norm = Math.sqrt(norm);
        if (norm > 0) {
            for (int i = 0; i < vector.length; i++) vector[i] = (float) (vector[i] / norm);
        }
        return vector;
    }

    @Override
    public String chat(String systemPrompt, String userPrompt) {
        String evidence = between(userPrompt, "【证据】", "【问题】");
        if (evidence.isBlank()) {
            return "（stub）没有检索到可用依据，无法回答。";
        }
        String first = evidence.strip().split("\n")[0];
        if (first.length() > 220) first = first.substring(0, 220);
        return "（stub 模式）根据已发布资料：" + first;
    }

    private static String between(String text, String start, String end) {
        if (text == null) return "";
        int from = text.indexOf(start);
        if (from < 0) return "";
        from += start.length();
        int to = text.indexOf(end, from);
        return to < 0 ? text.substring(from) : text.substring(from, to);
    }

    private static boolean isChinese(char c) {
        return c >= 0x4E00 && c <= 0x9FFF;
    }

    private static void add(float[] vector, String token, float weight) {
        int index = (token.hashCode() & 0x7FFFFFFF) % vector.length;
        vector[index] += weight;
    }
}
