package com.fullstack.platform.merchant;

/** Merchant-facing order projection. Never includes the redeem token. */
public record MerchantOrderView(
        String id,
        String shopId,
        String shopName,
        String userId,
        String userPhone,
        String voucherId,
        String voucherTitle,
        Integer orderStatus,
        String payAmount,
        String createTime,
        String payTime,
        String useTime,
        String refundTime,
        String redeemEndTime) {
}
