package com.fullstack.platform.dto;

/** String IDs avoid JavaScript precision loss. PROCESSING means accepted, not committed. */
public record OrderStatusDTO(String id, String state, String message, Integer orderStatus) {
}
