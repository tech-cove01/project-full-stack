package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.data.redis.core.StringRedisTemplate;
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
        if (!Boolean.TRUE.equals(redis.hasKey(conversationKey(userId, conversationId)))) return;
        redis.opsForHash().put(conversationKey(userId, conversationId), "shopId", shopId.toString());
        redis.opsForHash().put(conversationKey(userId, conversationId), "updatedAt", now());
        redis.expire(conversationKey(userId, conversationId), TTL);
    }

    public void append(long userId, String conversationId, String role, String text,
                       List<String> citationIds) {
        try {
            // A deleted conversation must not be recreated by an in-flight turn: require the
            // conversation hash to still exist before touching its message list.
            if (!Boolean.TRUE.equals(redis.hasKey(conversationKey(userId, conversationId)))) {
                return;
            }
            Message message = new Message(role, text, now(), citationIds == null ? List.of() : citationIds);
            String key = messagesKey(userId, conversationId);
            redis.opsForList().rightPush(key, objectMapper.writeValueAsString(message));
            redis.opsForList().trim(key, -MAX_MESSAGES, -1);
            redis.expire(key, TTL);
            redis.opsForHash().put(conversationKey(userId, conversationId), "updatedAt", now());
            redis.expire(conversationKey(userId, conversationId), TTL);
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
