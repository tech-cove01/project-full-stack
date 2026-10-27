package com.fullstack.platform.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import lombok.experimental.Accessors;

import java.io.Serializable;
import java.time.LocalDateTime;

/**
 * <p>
 * 秒杀优惠券表，与优惠券是一对一关系
 * </p>
 *
 * @author 虎哥
 * @since 2022-01-04
 */
@Data
@EqualsAndHashCode(callSuper = false)
@Accessors(chain = true)
@TableName("tb_seckill_voucher")
public class SeckillVoucher implements Serializable {

    private static final long serialVersionUID = 1L;

    /**
     * 关联的优惠券的id
     */
    @TableId(value = "voucher_id", type = IdType.INPUT)
    private Long voucherId;

    /** Snapshot of the owning merchant, used for publish idempotency. */
    private Long merchantId;

    /**
     * 库存
     */
    private Integer stock;

    /** Initial total stock; stock never exceeds this value. */
    private Integer totalStock;

    /** INITIALIZING / ACTIVE / OFFLINE. */
    private String publishStatus;

    /** Monotonic version used by the Redis admission gate. */
    private Integer publishVersion;

    /** Redemption window snapshot; independent from the sale window. */
    private LocalDateTime redeemBeginTime;
    private LocalDateTime redeemEndTime;

    /** Client idempotency key and canonical request hash. */
    private String publishRequestKey;
    private String publishRequestHash;

    /**
     * 创建时间
     */
    private LocalDateTime createTime;

    /**
     * 生效时间
     */
    private LocalDateTime beginTime;

    /**
     * 失效时间
     */
    private LocalDateTime endTime;

    /**
     * 更新时间
     */
    private LocalDateTime updateTime;


}
