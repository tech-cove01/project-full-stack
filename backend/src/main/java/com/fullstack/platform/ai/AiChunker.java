package com.fullstack.platform.ai;

import java.util.ArrayList;
import java.util.List;

/** Character-based chunker; keeps a small overlap so a rule is not cut in half. */
public final class AiChunker {
    private AiChunker() { }

    public static List<String> split(String text, int maxChars, int overlap) {
        List<String> chunks = new ArrayList<>();
        if (text == null || text.isBlank()) return chunks;
        String normalized = text.replace("\r\n", "\n").replaceAll("[ \t]+", " ").trim();
        int start = 0;
        while (start < normalized.length()) {
            int end = Math.min(start + maxChars, normalized.length());
            if (end < normalized.length()) {
                int punctuation = lastBreak(normalized, start + maxChars / 2, end);
                if (punctuation > start) end = punctuation + 1;
            }
            String chunk = normalized.substring(start, end).trim();
            if (!chunk.isEmpty()) chunks.add(chunk);
            if (end >= normalized.length()) break;
            start = Math.max(end - overlap, start + 1);
        }
        return chunks;
    }

    private static int lastBreak(String text, int from, int to) {
        for (int i = to - 1; i >= from; i--) {
            char c = text.charAt(i);
            if (c == '。' || c == '！' || c == '？' || c == '；' || c == '\n'
                    || c == '.' || c == '!' || c == '?' || c == ';') {
                return i;
            }
        }
        return -1;
    }
}
