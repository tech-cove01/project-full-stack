package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;
import org.springframework.web.server.ResponseStatusException;

import java.time.Duration;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.UUID;

@Service
public class AiConversationService {
    private static final Duration TTL = Duration.ofHours(24);
    private static final int MAX_MESSAGES = 40;
    private static final DateTimeFormatter DISPLAY = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    /**
     * Checks the conversation and appends the message in one script, so a delete that lands
     * between "does the conversation exist" and "push the message" can no longer resurrect it.
     */
    private static final DefaultRedisScript<Long> APPEND = new DefaultRedisScript<>("""
            if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
            redis.call('RPUSH', KEYS[2], ARGV[1])
            redis.call('LTRIM', KEYS[2], -tonumber(ARGV[2]), -1)
            redis.call('EXPIRE', KEYS[2], tonumber(ARGV[3]))
            redis.call('HSET', KEYS[1], 'updatedAt', ARGV[4])
            redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
            return 1
            """, Long.class);

    private static final DefaultRedisScript<Long> BIND_SHOP = new DefaultRedisScript<>("""
            if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
            redis.call('HSET', KEYS[1], 'shopId', ARGV[1], 'updatedAt', ARGV[2])
            redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
            return 1
            """, Long.class);

    private final StringRedisTemplate redis;
    private final ObjectMapper objectMapper;

    public AiConversationService(StringRedisTemplate redis, ObjectMapper objectMapper) {
        this.redis = redis;
        this.objectMapper = objectMapper;
    }

    public ConversationView create(long userId, Long shopId) {
        String id = UUID.randomUUID().toString();
        String key = conversationKey(userId, id);
        redis.opsForHash().putAll(key, Map.of(
                "id", id,
                "createdAt", now(),
                "updatedAt", now(),
                "shopId", shopId == null ? "" : shopId.toString()));
        redis.expire(key, TTL);
        redis.expire(messagesKey(userId, id), TTL);
        redis.opsForSet().add(indexKey(userId), id);
        redis.expire(indexKey(userId), TTL);
        return new ConversationView(id, shopId == null ? null : shopId.toString(), now(), List.of());
    }

    public void requireOwned(long userId, String conversationId) {
        if (conversationId == null || !Boolean.TRUE.equals(redis.hasKey(conversationKey(userId, conversationId)))) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "会话不存在");
        }
    }

    public Long shopOf(long userId, String conversationId) {
        Object value = redis.opsForHash().get(conversationKey(userId, conversationId), "shopId");
        if (value == null || value.toString().isBlank()) return null;
        try {
            return Long.valueOf(value.toString());
        } catch (NumberFormatException e) {
            return null;
        }
    }

    public void bindShop(long userId, String conversationId, Long shopId) {
        if (shopId == null) return;
        // Same "exists then write" hazard as append: a delete between the check and the two
        // HSET calls would recreate the conversation. Check and write inside one script.
        redis.execute(BIND_SHOP, List.of(conversationKey(userId, conversationId)),
                shopId.toString(), now(), Long.toString(TTL.toSeconds()));
    }

    public boolean exists(long userId, String conversationId) {
        return Boolean.TRUE.equals(redis.hasKey(conversationKey(userId, conversationId)));
    }

    public void append(long userId, String conversationId, String role, String text,
                       List<String> citationIds) {
        try {
            Message message = new Message(role, text, now(), citationIds == null ? List.of() : citationIds);
            // Returns 0 when the conversation was deleted by a concurrent request: the message
            // is dropped instead of recreating the conversation.
            redis.execute(APPEND,
                    List.of(conversationKey(userId, conversationId), messagesKey(userId, conversationId)),
                    objectMapper.writeValueAsString(message), Integer.toString(MAX_MESSAGES),
                    Long.toString(TTL.toSeconds()), now());
        } catch (Exception e) {
            throw new IllegalStateException("Cannot persist AI message", e);
        }
    }

    public ConversationView history(long userId, String conversationId) {
        requireOwned(userId, conversationId);
        List<String> raw = redis.opsForList().range(messagesKey(userId, conversationId), 0, -1);
        List<Message> messages = new ArrayList<>();
        if (raw != null) {
            for (String item : raw) {
                try {
                    messages.add(objectMapper.readValue(item, Message.class));
                } catch (Exception ignored) {
                    // A corrupt local message must not block the rest of the history.
                }
            }
        }
        Object createdAt = redis.opsForHash().get(conversationKey(userId, conversationId), "createdAt");
        Object shopId = redis.opsForHash().get(conversationKey(userId, conversationId), "shopId");
        return new ConversationView(conversationId,
                shopId == null || shopId.toString().isBlank() ? null : shopId.toString(),
                createdAt == null ? null : createdAt.toString(), messages);
    }

    public void delete(long userId, String conversationId) {
        requireOwned(userId, conversationId);
        redis.delete(conversationKey(userId, conversationId));
        redis.delete(messagesKey(userId, conversationId));
        redis.opsForSet().remove(indexKey(userId), conversationId);
    }

    public List<Message> recent(long userId, String conversationId, int limit) {
        List<String> raw = redis.opsForList().range(messagesKey(userId, conversationId), -limit, -1);
        List<Message> messages = new ArrayList<>();
        if (raw != null) {
            for (String item : raw) {
                try {
                    messages.add(objectMapper.readValue(item, Message.class));
                } catch (Exception ignored) { }
            }
        }
        return messages;
    }

    public static String sha256(String value) {
        try {
            byte[] digest = java.security.MessageDigest.getInstance("SHA-256")
                    .digest(value.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            return java.util.HexFormat.of().formatHex(digest);
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    private static String now() {
        return LocalDateTime.now().format(DISPLAY);
    }

    private static String conversationKey(long userId, String id) {
        return "ai:conv:" + userId + ":" + id;
    }
    private static String messagesKey(long userId, String id) {
        return "ai:conv:" + userId + ":" + id + ":messages";
    }
    private static String indexKey(long userId) {
        return "ai:conversations:" + userId;
    }

    public record Message(String role, String text, String time, List<String> citationIds) { }
    public record ConversationView(String id, String shopId, String createdAt, List<Message> messages) { }
}
