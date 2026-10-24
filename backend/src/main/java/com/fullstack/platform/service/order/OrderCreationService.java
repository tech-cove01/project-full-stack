package com.fullstack.platform.service.order;

import com.baomidou.mybatisplus.core.conditions.query.QueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.UpdateWrapper;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.VoucherOrder;
import com.fullstack.platform.mapper.SeckillVoucherMapper;
import com.fullstack.platform.mapper.VoucherOrderMapper;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/** Database is the final stock authority. Requires UNIQUE(user_id, voucher_id). */
@Service
public class OrderCreationService {
    private final VoucherOrderMapper orderMapper;
    private final SeckillVoucherMapper voucherMapper;

    public OrderCreationService(VoucherOrderMapper orderMapper, SeckillVoucherMapper voucherMapper) {
        this.orderMapper = orderMapper;
        this.voucherMapper = voucherMapper;
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
        VoucherOrder userOrder = orderMapper.selectOne(new QueryWrapper<VoucherOrder>()
                .eq("user_id", order.getUserId()).eq("voucher_id", order.getVoucherId()));
        if (userOrder != null) throw new OrderRejectedException("该活动已有订单", true);

        int changed = voucherMapper.update(null, new UpdateWrapper<SeckillVoucher>()
                .setSql("stock = stock - 1").eq("voucher_id", order.getVoucherId()).gt("stock", 0));
        if (changed != 1) throw new OrderRejectedException("活动库存不足", false);
        order.setStatus(1);
        if (orderMapper.insert(order) != 1) {
            throw new IllegalStateException("Order insert failed; stock update must roll back");
        }
    }
}
