package com.fullstack.platform.merchant;

import com.fullstack.platform.dto.Result;
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

    public MerchantOrderController(MerchantOrderService service) {
        this.service = service;
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
