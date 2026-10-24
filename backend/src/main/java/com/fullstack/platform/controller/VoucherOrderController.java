package com.fullstack.platform.controller;

import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.dto.Result;
import com.fullstack.platform.order.OrderLifecycleService;
import com.fullstack.platform.order.OrderQueryService;
import com.fullstack.platform.service.IVoucherOrderService;
import com.fullstack.platform.utils.UserHolder;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import jakarta.annotation.Resource;

@RestController
@RequestMapping("/voucher-order")
public class VoucherOrderController {

    @Resource
    private IVoucherOrderService voucherOrderService;

    @Resource
    private OrderQueryService orderQuery;

    @Resource
    private OrderLifecycleService lifecycle;

    @PostMapping("seckill/{id}")
    public Result seckillVoucher(@PathVariable("id") Long voucherId) {
        return voucherOrderService.seckillVoucher(voucherId);
    }

    /** DB-backed orders return the full snapshot; PROCESSING reservations keep the legacy shape. */
    @GetMapping("/{id}")
    public Result queryOrderStatus(@PathVariable("id") Long orderId) {
        Result result = voucherOrderService.queryOrderStatus(orderId);
        if (result.getSuccess() && result.getData() instanceof OrderStatusDTO dto
                && "CREATED".equals(dto.state())) {
            return Result.ok(orderQuery.detail(orderId, UserHolder.getUser().getId(), true));
        }
        return result;
    }

    @GetMapping
    public Result myOrders(@RequestParam(value = "current", defaultValue = "1") int current,
                           @RequestParam(value = "pageSize", defaultValue = "10") int pageSize) {
        OrderQueryService.PageResult page = orderQuery.list(UserHolder.getUser().getId(), current, pageSize);
        return Result.ok(page.items(), page.total());
    }

    @PostMapping("/{id}/pay")
    public Result pay(@PathVariable("id") Long orderId) {
        return Result.ok(lifecycle.pay(orderId, UserHolder.getUser().getId()));
    }

    @PostMapping("/{id}/cancel")
    public Result cancel(@PathVariable("id") Long orderId,
                         @RequestParam(value = "reason", required = false) String reason) {
        return Result.ok(lifecycle.cancel(orderId, UserHolder.getUser().getId(), reason));
    }
}
