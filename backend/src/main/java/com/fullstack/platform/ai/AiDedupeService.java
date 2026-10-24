package com.fullstack.platform.ai;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.util.Map;

@Service
public class AiDedupeService {
    private final StringRedisTemplate redis;

    public AiDedupeService(StringRedisTemplate redis) {
        this.redis = redis;
    }

    public Start begin(long userId, String conversationId, String clientMessageId, String message) {
        String key = key(userId, conversationId, clientMessageId);
        String hash = AiConversationService.sha256(message);
        Map<Object, Object> existing = redis.opsForHash().entries(key);
        if (!existing.isEmpty()) {
            String previousHash = String.valueOf(existing.get("hash"));
            if (!hash.equals(previousHash)) return new Start(State.CONFLICT, null);
            if ("DONE".equals(existing.get("status")) && existing.get("answer") != null) {
                return new Start(State.CACHED, String.valueOf(existing.get("answer")));
            }
            return new Start(State.IN_PROGRESS, null);
        }
        redis.opsForHash().putAll(key, Map.of("hash", hash, "status", "PROCESSING"));
        redis.expire(key, Duration.ofHours(1));
        return new Start(State.NEW, null);
    }

    public void complete(long userId, String conversationId, String clientMessageId, String answerJson) {
        String key = key(userId, conversationId, clientMessageId);
        redis.opsForHash().putAll(key, Map.of("status", "DONE", "answer", answerJson));
        redis.expire(key, Duration.ofHours(24));
    }

    public void fail(long userId, String conversationId, String clientMessageId) {
        redis.delete(key(userId, conversationId, clientMessageId));
    }

    private static String key(long userId, String conversationId, String clientMessageId) {
        return "ai:dedupe:" + userId + ":" + conversationId + ":" + clientMessageId;
    }

    public enum State { NEW, CACHED, IN_PROGRESS, CONFLICT }
    public record Start(State state, String answerJson) { }
}
