package com.fullstack.platform.ai;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;

@RestController
@RequestMapping("/ai/conversations")
public class AiConversationController {
    private final AiConversationService conversations;
    private final AiAssistantService assistant;

    public AiConversationController(AiConversationService conversations, AiAssistantService assistant) {
        this.conversations = conversations;
        this.assistant = assistant;
    }

    @PostMapping
    public Result create(@RequestBody(required = false) CreateConversationRequest request) {
        long userId = currentUserId();
        Long shopId = parseShop(request == null ? null : request.shopId());
        return Result.ok(conversations.create(userId, shopId));
    }

    @GetMapping("/{id}")
    public Result history(@PathVariable("id") String id) {
        return Result.ok(conversations.history(currentUserId(), id));
    }

    @DeleteMapping("/{id}")
    public Result delete(@PathVariable("id") String id) {
        conversations.delete(currentUserId(), id);
        return Result.ok();
    }

    @PostMapping(value = "/{id}/messages", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
    public SseEmitter message(@PathVariable("id") String id, @RequestBody SendMessageRequest request) {
        long userId = currentUserId();
        conversations.requireOwned(userId, id);
        Long shopId = parseShop(request.shopId());
        if (shopId != null) conversations.bindShop(userId, id, shopId);
        return assistant.start(new AiAuthContext(userId, shopId), id,
                request.clientMessageId(), request.message(), shopId);
    }

    private static long currentUserId() {
        UserDTO user = UserHolder.getUser();
        if (user == null || user.getId() == null) {
            throw new ResponseStatusException(HttpStatus.UNAUTHORIZED, "请先登录");
        }
        return user.getId();
    }

    private static Long parseShop(String value) {
        if (value == null || value.isBlank()) return null;
        try {
            long id = Long.parseLong(value);
            if (id <= 0) throw new NumberFormatException();
            return id;
        } catch (NumberFormatException e) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "shopId 必须为正整数");
        }
    }

    public record CreateConversationRequest(String shopId) { }
    public record SendMessageRequest(String clientMessageId, String message, String shopId) { }
}
