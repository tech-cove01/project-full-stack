package com.fullstack.platform.dto;

/** String IDs avoid JavaScript precision loss. PROCESSING means accepted, not committed. */
public record OrderStatusDTO(
        String id,
        String state,
        String message,
        Integer orderStatus,
        String shopId,
        String payAmount,
        String payDeadlineAt,
        String redeemBeginTime,
        String redeemEndTime,
        String redeemToken,
        String createTime,
        String payTime,
        String useTime,
        String refundTime) {

    /** Compatibility constructor used by pending-reservation paths and existing tests. */
    public OrderStatusDTO(String id, String state, String message, Integer orderStatus) {
        this(id, state, message, orderStatus, null, null, null, null, null, null, null, null, null, null);
    }
}
