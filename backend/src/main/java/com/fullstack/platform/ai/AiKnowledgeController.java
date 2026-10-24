package com.fullstack.platform.ai;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.merchant.ShopAccessService;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

@RestController
@RequestMapping("/merchant/knowledge")
public class AiKnowledgeController {
    private final AiKnowledgeService knowledge;
    private final ShopAccessService access;

    public AiKnowledgeController(AiKnowledgeService knowledge, ShopAccessService access) {
        this.knowledge = knowledge;
        this.access = access;
    }

    @GetMapping
    public Result list(@RequestParam("shopId") String shopId) {
        UserDTO user = access.requireUser();
        long id = positiveLong(shopId);
        ShopAccessService.Membership membership = access.membershipForShop(id, user.getId());
        List<AiKnowledgeView> rows = knowledge.list(id, membership.manager());
        return Result.ok(rows, (long) rows.size());
    }

    @PostMapping
    public Result create(@RequestBody CreateKnowledgeRequest request) {
        UserDTO user = access.requireUser();
        long shopId = positiveLong(request.shopId());
        access.requireManager(shopId, user.getId());
        long id = knowledge.createDraft(shopId, request.title(), request.body(), user.getId());
        return Result.ok(knowledge.view(id));
    }

    @PutMapping("/{id}")
    public Result update(@PathVariable("id") long id, @RequestBody UpdateKnowledgeRequest request) {
        UserDTO user = access.requireUser();
        AiKnowledgeService.Document document = knowledge.requireDocument(id);
        requireShopDocument(document);
        access.requireManager(document.shopId(), user.getId());
        knowledge.updateDraft(id, request.title(), request.body(), user.getId());
        return Result.ok(knowledge.view(id));
    }

    @PostMapping("/{id}/publish")
    public Result publish(@PathVariable("id") long id) {
        UserDTO user = access.requireUser();
        AiKnowledgeService.Document document = knowledge.requireDocument(id);
        requireShopDocument(document);
        access.requireManager(document.shopId(), user.getId());
        return Result.ok(knowledge.publish(id, user.getId(), "MERCHANT"));
    }

    @PostMapping("/{id}/disable")
    public Result disable(@PathVariable("id") long id) {
        UserDTO user = access.requireUser();
        AiKnowledgeService.Document document = knowledge.requireDocument(id);
        requireShopDocument(document);
        access.requireManager(document.shopId(), user.getId());
        knowledge.disable(id, user.getId(), "MERCHANT");
        return Result.ok(knowledge.view(id));
    }

    @PostMapping("/{id}/rebuild")
    public Result rebuild(@PathVariable("id") long id) {
        UserDTO user = access.requireUser();
        AiKnowledgeService.Document document = knowledge.requireDocument(id);
        requireShopDocument(document);
        access.requireManager(document.shopId(), user.getId());
        return Result.ok(knowledge.publish(id, user.getId(), "MERCHANT"));
    }

    private static void requireShopDocument(AiKnowledgeService.Document document) {
        if (!"SHOP_PUBLIC".equals(document.scope()) || document.shopId() == null) {
            throw new org.springframework.web.server.ResponseStatusException(
                    org.springframework.http.HttpStatus.FORBIDDEN, "只能维护门店公开知识");
        }
    }

    private static long positiveLong(String value) {
        try {
            long parsed = Long.parseLong(value);
            if (parsed <= 0) throw new NumberFormatException();
            return parsed;
        } catch (RuntimeException e) {
            throw new org.springframework.web.server.ResponseStatusException(
                    org.springframework.http.HttpStatus.BAD_REQUEST, "shopId 必须为正整数");
        }
    }

    public record CreateKnowledgeRequest(String shopId, String title, String body) { }
    public record UpdateKnowledgeRequest(String title, String body) { }
}
