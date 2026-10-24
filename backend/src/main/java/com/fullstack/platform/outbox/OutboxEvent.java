package com.fullstack.platform.outbox;

public record OutboxEvent(long id, String eventId, String aggregateType, String aggregateId,
                          int aggregateVersion, String eventType, String payload, int attempts) {
}
