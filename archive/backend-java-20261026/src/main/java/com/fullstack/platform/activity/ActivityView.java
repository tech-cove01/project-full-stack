package com.fullstack.platform.activity;

/** Activity projection for merchant pages; times use the business timezone. */
public record ActivityView(
        String id,
        String shopId,
        String shopName,
        String title,
        String subTitle,
        String payValue,
        String actualValue,
        int totalStock,
        int stock,
        String publishStatus,
        int publishVersion,
        boolean ready,
        String beginTime,
        String endTime,
        String redeemBeginTime,
        String redeemEndTime) {
}
