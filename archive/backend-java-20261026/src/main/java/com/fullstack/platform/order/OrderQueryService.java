package com.fullstack.platform.order;

import com.baomidou.mybatisplus.core.conditions.query.QueryWrapper;
import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import org.springframework.http.HttpStatus;
import org.springframework.stereotype.Service;
import org.springframework.web.server.ResponseStatusException;

import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.List;

@Service
public class OrderQueryService {
    private static final DateTimeFormatter DISPLAY = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    private final VoucherOrderMapper orders;

    public OrderQueryService(VoucherOrderMapper orders) {
        this.orders = orders;
    }

    public VoucherOrder requireOwned(long orderId, long userId) {
        VoucherOrder order = orders.selectById(orderId);
        if (order == null) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "订单不存在");
        }
        if (!order.getUserId().equals(userId)) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "订单不存在");
        }
        return order;
    }

    public VoucherOrder find(long orderId) {
        return orders.selectById(orderId);
    }

    public OrderStatusDTO detail(long orderId, long userId, boolean includeToken) {
        return toDto(requireOwned(orderId, userId), includeToken);
    }

    public PageResult list(long userId, int current, int pageSize) {
        Page<VoucherOrder> page = new Page<>(Math.max(current, 1), Math.min(Math.max(pageSize, 1), 50));
        orders.selectPage(page, new QueryWrapper<VoucherOrder>()
                .eq("user_id", userId)
                .orderByDesc("create_time")
                .orderByDesc("id"));
        List<OrderStatusDTO> items = page.getRecords().stream()
                .map(order -> toDto(order, false))
                .toList();
        return new PageResult(items, page.getTotal());
    }

    public OrderStatusDTO toDto(VoucherOrder order, boolean includeToken) {
        Integer status = order.getStatus();
        return new OrderStatusDTO(
                Long.toString(order.getId()),
                "CREATED",
                message(status),
                status,
                order.getShopId() == null ? null : Long.toString(order.getShopId()),
                order.getPayAmount() == null ? null : Long.toString(order.getPayAmount()),
                time(order.getPayDeadlineAt()),
                time(order.getRedeemBeginTime()),
                time(order.getRedeemEndTime()),
                includeToken && Integer.valueOf(2).equals(status) ? order.getRedeemToken() : null,
                time(order.getCreateTime()),
                time(order.getPayTime()),
                time(order.getUseTime()),
                time(order.getRefundTime()));
    }

    private static String message(Integer status) {
        if (status == null) return "订单处理中";
        return switch (status) {
            case 1 -> "待支付";
            case 2 -> "已支付，待核销";
            case 3 -> "已核销";
            case 4 -> "已取消";
            case 6 -> "已退款";
            default -> "订单状态未知";
        };
    }

    private static String time(LocalDateTime value) {
        return value == null ? null : value.format(DISPLAY);
    }

    public record PageResult(List<OrderStatusDTO> items, long total) { }
}
