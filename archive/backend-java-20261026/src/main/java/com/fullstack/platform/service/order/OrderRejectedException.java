package com.fullstack.platform.service.order;

/** A definite rejection: no database mutation committed for this reservation. */
public class OrderRejectedException extends RuntimeException {
    private final boolean keepPurchaseMarker;

    public OrderRejectedException(String message, boolean keepPurchaseMarker) {
        super(message);
        this.keepPurchaseMarker = keepPurchaseMarker;
    }

    public boolean keepPurchaseMarker() {
        return keepPurchaseMarker;
    }
}
