package com.fullstack.platform.ai;

import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Service;

import java.time.Duration;
import java.util.List;
import java.util.Map;

@Service
public class AiDedupeService {
    /**
     * Atomic begin: the previous read-then-write could let two concurrent requests with the same
     * clientMessageId both believe they were first. Returns {code, answer} where 2 = new,
     * 0 = in progress, 1 = cached, -1 = same id with different content.
     */
    private static final DefaultRedisScript<List> BEGIN = new DefaultRedisScript<>("""
            local existing = redis.call('HGETALL', KEYS[1])
            if #existing > 0 then
              local hash, status = '', ''
              for i = 1, #existing, 2 do
                if existing[i] == 'hash' then hash = existing[i + 1] end
                if existing[i] == 'status' then status = existing[i + 1] end
              end
              if hash ~= ARGV[1] then return {-1, ''} end
              if status == 'DONE' then return {1, redis.call('HGET', KEYS[1], 'answer') or ''} end
              return {0, ''}
            end
            redis.call('HSET', KEYS[1], 'hash', ARGV[1], 'status', 'PROCESSING')
            redis.call('EXPIRE', KEYS[1], 3600)
            return {2, ''}
            """, List.class);

    private final StringRedisTemplate redis;

    public AiDedupeService(StringRedisTemplate redis) {
        this.redis = redis;
    }

    public Start begin(long userId, String conversationId, String clientMessageId, Long shopId,
                       String message) {
        String key = key(userId, conversationId, clientMessageId);
        // The effective shop is part of the digest: the same id on another shop must not replay
        // the first shop's cached answer.
        String hash = AiConversationService.sha256((shopId == null ? "none" : shopId.toString())
                + "|" + message);
        List<?> result = redis.execute(BEGIN, List.of(key), hash);
        if (result == null || result.isEmpty()) {
            return new Start(State.IN_PROGRESS, null);
        }
        int code = ((Number) result.get(0)).intValue();
        String answer = result.size() > 1 ? String.valueOf(result.get(1)) : null;
        return switch (code) {
            case 2 -> new Start(State.NEW, null);
            case 1 -> new Start(State.CACHED, answer);
            case -1 -> new Start(State.CONFLICT, null);
            default -> new Start(State.IN_PROGRESS, null);
        };
    }

    /**
     * Marks the turn done and stores the knowledge dependencies that produced it, so a replay
     * can be rejected once any source was disabled or replaced.
     */
    public void complete(long userId, String conversationId, String clientMessageId, String answerJson,
                         String dependencies) {
        String key = key(userId, conversationId, clientMessageId);
        redis.opsForHash().putAll(key, Map.of("status", "DONE", "answer", answerJson,
                "deps", dependencies == null ? "" : dependencies));
        redis.expire(key, Duration.ofHours(24));
    }

    public String dependencies(long userId, String conversationId, String clientMessageId) {
        Object value = redis.opsForHash().get(key(userId, conversationId, clientMessageId), "deps");
        return value == null ? "" : value.toString();
    }

    public void fail(long userId, String conversationId, String clientMessageId) {
        redis.delete(key(userId, conversationId, clientMessageId));
    }

    public void delete(long userId, String conversationId, String clientMessageId) {
        fail(userId, conversationId, clientMessageId);
    }

    private static String key(long userId, String conversationId, String clientMessageId) {
        return "ai:dedupe:" + userId + ":" + conversationId + ":" + clientMessageId;
    }

    public enum State { NEW, CACHED, IN_PROGRESS, CONFLICT }
    public record Start(State state, String answerJson) { }
}
