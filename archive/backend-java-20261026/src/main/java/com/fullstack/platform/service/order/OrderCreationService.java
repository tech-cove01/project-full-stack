package com.fullstack.platform.service.order;

import com.baomidou.mybatisplus.core.conditions.query.QueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.UpdateWrapper;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.Voucher;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.SeckillVoucherMapper;
import com.fullstack.platform.mapper.VoucherMapper;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.time.LocalDateTime;
import java.time.ZoneId;

/** Database is the final stock authority. Requires UNIQUE(user_id, voucher_id, active_flag). */
@Service
public class OrderCreationService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final VoucherOrderMapper orderMapper;
    private final SeckillVoucherMapper voucherMapper;
    private final VoucherMapper catalogMapper;

    @Value("${app.pay-timeout-minutes:15}")
    private int payTimeoutMinutes = 15;

    public OrderCreationService(VoucherOrderMapper orderMapper, SeckillVoucherMapper voucherMapper,
                                VoucherMapper catalogMapper) {
        this.orderMapper = orderMapper;
        this.voucherMapper = voucherMapper;
        this.catalogMapper = catalogMapper;
    }

    public VoucherOrder findById(Long id) {
        return orderMapper.selectById(id);
    }

    @Transactional(rollbackFor = Exception.class)
    public void create(VoucherOrder order) {
        VoucherOrder existing = orderMapper.selectById(order.getId());
        if (existing != null) {
            if (!existing.getUserId().equals(order.getUserId())
                    || !existing.getVoucherId().equals(order.getVoucherId())) {
                throw new IllegalStateException("Order identifier collision");
            }
            return;
        }
        VoucherOrder activeOrder = orderMapper.selectOne(new QueryWrapper<VoucherOrder>()
                .eq("user_id", order.getUserId())
                .eq("voucher_id", order.getVoucherId())
                .eq("active_flag", 1));
        if (activeOrder != null) {
            // A cancelled order has active_flag = NULL and does not block a new purchase.
            throw new OrderRejectedException("该活动已有有效订单", true);
        }

        Voucher voucher = catalogMapper.selectById(order.getVoucherId());
        SeckillVoucher activity = voucherMapper.selectById(order.getVoucherId());
        if (voucher == null || activity == null) {
            throw new IllegalStateException("Voucher configuration disappeared");
        }

        int changed = voucherMapper.update(null, new UpdateWrapper<SeckillVoucher>()
                .setSql("stock = stock - 1").eq("voucher_id", order.getVoucherId()).gt("stock", 0));
        if (changed != 1) throw new OrderRejectedException("活动库存不足", false);

        LocalDateTime deadline = LocalDateTime.now(BUSINESS_ZONE).plusMinutes(payTimeoutMinutes);
        if (activity.getRedeemEndTime() != null && deadline.isAfter(activity.getRedeemEndTime())) {
            deadline = activity.getRedeemEndTime();
        }
        order.setStatus(1)
                .setShopId(voucher.getShopId())
                .setPayAmount(voucher.getPayValue())
                .setRedeemBeginTime(activity.getRedeemBeginTime())
                .setRedeemEndTime(activity.getRedeemEndTime())
                .setPayDeadlineAt(deadline)
                .setVersion(0);
        try {
            if (orderMapper.insert(order) != 1) {
                throw new IllegalStateException("Order insert failed; stock update must roll back");
            }
        } catch (DuplicateKeyException e) {
            // The unique active-order constraint won a concurrent race.
            throw new OrderRejectedException("该活动已有有效订单", true);
        }
    }
}
