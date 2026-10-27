package com.fullstack.platform.activity;

/** Whitelisted publish payload. IDs and money stay strings to preserve precision. */
public record CreateActivityRequest(
        String shopId,
        String title,
        String subTitle,
        String rules,
        String payValue,
        String actualValue,
        Integer totalStock,
        String beginTime,
        String endTime,
        String redeemBeginTime,
        String redeemEndTime) {
}
