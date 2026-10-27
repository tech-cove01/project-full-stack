package com.fullstack.platform.service;

import com.fullstack.platform.dto.Result;
import com.fullstack.platform.entity.Voucher;
import com.baomidou.mybatisplus.spring.service.IService;

/**
 * <p>
 *  服务类
 * </p>
 *
 * @author 虎哥
 * @since 2021-12-22
 */
public interface IVoucherService extends IService<Voucher> {

    Result queryVoucherOfShop(Long shopId);

    void addSeckillVoucher(Voucher voucher);
}
