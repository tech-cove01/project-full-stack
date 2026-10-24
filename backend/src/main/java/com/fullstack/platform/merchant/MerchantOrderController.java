package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.ratelimit.RateLimitException;
import com.fullstack.platform.ratelimit.RateLimitService;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/merchant")
public class MerchantOrderController {
    private final MerchantOrderService service;
    private final RateLimitService rateLimit;

    @Value("${app.rate-limit.redeem-per-second:10}")
    private int redeemLimit = 10;

    public MerchantOrderController(MerchantOrderService service, RateLimitService rateLimit) {
        this.service = service;
        this.rateLimit = rateLimit;
    }

    @GetMapping("/orders")
    public Result orders(@RequestParam(value = "current", defaultValue = "1") int current,
                         @RequestParam(value = "pageSize", defaultValue = "10") int pageSize,
                         @RequestParam(value = "shopId", required = false) String shopId,
                         @RequestParam(value = "status", required = false) String status,
                         @RequestParam(value = "orderId", required = false) String orderId) {
        MerchantOrderService.PageResult<MerchantOrderView> page =
                service.list(current, pageSize, shopId, status, orderId);
        return Result.ok(page.items(), page.total());
    }

    @PostMapping("/orders/redeem")
    public Result redeem(@RequestBody RedeemRequest request) {
        if (!rateLimit.allow("redeem", UserHolder.getUser().getId(), redeemLimit)) {
            throw new RateLimitException("核销请求过于频繁，请稍后再试");
        }
        return Result.ok(service.redeem(request.shopId(), request.token()));
    }

    @PostMapping("/orders/{id}/refund")
    public Result refund(@PathVariable("id") long id,
                         @RequestBody(required = false) RefundRequest request) {
        return Result.ok(service.refund(id, request == null ? null : request.reason()));
    }

    @GetMapping("/audits")
    public Result audits(@RequestParam(value = "current", defaultValue = "1") int current,
                         @RequestParam(value = "pageSize", defaultValue = "10") int pageSize,
                         @RequestParam(value = "shopId", required = false) String shopId,
                         @RequestParam(value = "action", required = false) String action) {
        MerchantOrderService.PageResult<MerchantOrderService.AuditView> page =
                service.audits(current, pageSize, shopId, action);
        return Result.ok(page.items(), page.total());
    }

    public record RedeemRequest(String shopId, String token) { }
    public record RefundRequest(String reason) { }
}
