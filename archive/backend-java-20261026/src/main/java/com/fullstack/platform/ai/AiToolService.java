package com.fullstack.platform.ai;

import com.fullstack.platform.dto.OrderStatusDTO;
import com.fullstack.platform.order.OrderQueryService;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Read-only tools; identity and shop scope always come from the authenticated context. */
@Service
public class AiToolService {
    private static final Pattern ORDER_ID = Pattern.compile("(?<!\\d)(\\d{6,})(?!\\d)");

    private final JdbcTemplate jdbc;
    private final OrderQueryService orders;

    public AiToolService(JdbcTemplate jdbc, OrderQueryService orders) {
        this.jdbc = jdbc;
        this.orders = orders;
    }

    public ToolResult route(long userId, Long shopId, String message) {
        String text = message == null ? "" : message;
        Matcher matcher = ORDER_ID.matcher(text);
        boolean orderIntent = text.contains("订单") || text.toLowerCase().contains("order");
        if (orderIntent && matcher.find()) {
            return orderById(userId, Long.parseLong(matcher.group(1)));
        }
        if (orderIntent && (text.contains("我的订单") || text.contains("查订单")
                || text.contains("订单状态怎么样") || text.contains("订单进度"))) {
            return recentOrders(userId);
        }
        if ((text.contains("有什么活动") || text.contains("活动库存") || text.contains("活动价格")
                || text.contains("优惠券") || text.contains("库存") || text.contains("价格")
                || text.contains("优惠"))
                && shopId != null) {
            return activities(shopId);
        }
        if (text.contains("附近") || text.contains("推荐") || text.contains("有什么店") || text.contains("找店")) {
            return shops(text);
        }
        return null;
    }

    private ToolResult orderById(long userId, long orderId) {
        try {
            OrderStatusDTO order = orders.detail(orderId, userId, false);
            Map<String, Object> card = new LinkedHashMap<>();
            card.put("type", "order");
            card.put("id", order.id());
            card.put("orderStatus", order.orderStatus());
            card.put("statusText", statusText(order.orderStatus()));
            card.put("payAmount", order.payAmount());
            card.put("payDeadlineAt", order.payDeadlineAt());
            card.put("createTime", order.createTime());
            card.put("href", "/orders/" + order.id());
            return new ToolResult(
                    "你的订单 " + order.id() + " 当前状态是「" + statusText(order.orderStatus())
                            + "」，金额 ¥" + yuan(order.payAmount()) + "。详情以订单页为准。",
                    card, List.of());
        } catch (RuntimeException e) {
            return new ToolResult("没有找到你的这笔订单，不能查看他人的订单信息。", null, List.of());
        }
    }

    private ToolResult recentOrders(long userId) {
        OrderQueryService.PageResult page = orders.list(userId, 1, 5);
        if (page.items().isEmpty()) {
            return new ToolResult("你当前还没有订单。", null, List.of());
        }
        List<Map<String, Object>> items = new ArrayList<>();
        for (OrderStatusDTO order : page.items()) {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("id", order.id());
            item.put("orderStatus", order.orderStatus());
            item.put("statusText", statusText(order.orderStatus()));
            item.put("payAmount", order.payAmount());
            item.put("createTime", order.createTime());
            item.put("href", "/orders/" + order.id());
            items.add(item);
        }
        Map<String, Object> card = Map.of("type", "orders", "items", items);
        return new ToolResult("你最近有 " + items.size() + " 笔订单，下面是实时状态。", card, List.of());
    }

    private ToolResult activities(long shopId) {
        List<Map<String, Object>> items = jdbc.query("""
                SELECT v.id, v.title, v.pay_value, v.actual_value, sv.stock, sv.begin_time, sv.end_time
                FROM tb_voucher v
                JOIN tb_seckill_voucher sv ON sv.voucher_id = v.id
                WHERE v.shop_id = ? AND v.status = 1 AND sv.publish_status = 'ACTIVE'
                ORDER BY v.id DESC LIMIT 10
                """, (rs, rowNum) -> {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("id", Long.toString(rs.getLong("id")));
            item.put("title", rs.getString("title"));
            item.put("payValue", Long.toString(rs.getLong("pay_value")));
            item.put("actualValue", Long.toString(rs.getLong("actual_value")));
            item.put("stock", rs.getInt("stock"));
            item.put("beginTime", String.valueOf(rs.getObject("begin_time")));
            item.put("endTime", String.valueOf(rs.getObject("end_time")));
            item.put("href", "/shops/" + shopId);
            return item;
        }, shopId);
        Map<String, Object> card = Map.of("type", "activities", "shopId", Long.toString(shopId), "items", items);
        return new ToolResult("这家店当前有 " + items.size() + " 个已发布活动；库存和可售状态以点击后的页面为准。",
                card, List.of());
    }

    private ToolResult shops(String message) {
        String keyword = message.contains("咖啡") ? "咖啡"
                : message.contains("食堂") ? "食堂"
                : message.contains("书店") || message.contains("书房") ? "书" : null;
        String sql = "SELECT id, name, area, address, avg_price, score FROM tb_shop"
                + (keyword == null ? "" : " WHERE name LIKE ?")
                + " ORDER BY score DESC LIMIT 5";
        List<Map<String, Object>> items = jdbc.query(sql, (rs, rowNum) -> {
            Map<String, Object> item = new LinkedHashMap<>();
            item.put("id", Long.toString(rs.getLong("id")));
            item.put("name", rs.getString("name"));
            item.put("area", rs.getString("area"));
            item.put("address", rs.getString("address"));
            item.put("avgPrice", rs.getObject("avg_price") == null ? null : Long.toString(rs.getLong("avg_price")));
            item.put("score", rs.getInt("score"));
            item.put("href", "/shops/" + rs.getLong("id"));
            return item;
        }, keyword == null ? new Object[] {} : new Object[] { "%" + keyword + "%" });
        Map<String, Object> card = Map.of("type", "shops", "items", items);
        return new ToolResult("按公开信息找到 " + items.size() + " 家门店。", card, List.of());
    }

    private static String statusText(Integer status) {
        if (status == null) return "处理中";
        return switch (status) {
            case 1 -> "待支付";
            case 2 -> "已支付，待核销";
            case 3 -> "已核销";
            case 4 -> "已取消";
            case 6 -> "已退款";
            default -> "未知状态";
        };
    }

    private static String yuan(String cents) {
        try {
            long value = Long.parseLong(cents);
            return (value / 100) + "." + String.format("%02d", Math.abs(value % 100));
        } catch (Exception e) {
            return cents;
        }
    }

    public record ToolResult(String text, Map<String, Object> card, List<String> citationIds) { }
}
