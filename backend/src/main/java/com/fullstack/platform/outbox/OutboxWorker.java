package com.fullstack.platform.outbox;

import com.fullstack.platform.activity.ActivityPublicationService;
import com.fullstack.platform.order.OrderCompensationService;
import lombok.extern.slf4j.Slf4j;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

import java.util.List;

/** Delivers outbox events; business effects are idempotent by event ID or aggregate version. */
@Slf4j
@Component
public class OutboxWorker {
    private final OutboxRepository outbox;
    private final ActivityPublicationService publicationService;
    private final OrderCompensationService compensationService;

    public OutboxWorker(OutboxRepository outbox, ActivityPublicationService publicationService,
                        OrderCompensationService compensationService) {
        this.outbox = outbox;
        this.publicationService = publicationService;
        this.compensationService = compensationService;
    }

    @Scheduled(fixedDelay = 1000)
    public void poll() {
        List<OutboxEvent> events;
        try {
            events = outbox.claim(50);
        } catch (Exception e) {
            log.warn("Outbox claim failed; will retry", e);
            return;
        }
        for (OutboxEvent event : events) {
            try {
                dispatch(event);
                outbox.markDone(event.id());
            } catch (NeedsReviewException e) {
                log.error("Outbox event {} needs review: {}", event.eventId(), e.getMessage());
                outbox.needsReview(event, e.getMessage());
            } catch (Exception e) {
                log.warn("Outbox event {} failed; retry scheduled: {}", event.eventId(), e.toString());
                outbox.retry(event, e.toString());
            }
        }
    }

    private void dispatch(OutboxEvent event) {
        switch (event.eventType()) {
            case "ACTIVITY_PREPARE" -> publicationService.prepareAndOpen(Long.parseLong(event.aggregateId()));
            case "ACTIVITY_OPEN" -> publicationService.openOnly(Long.parseLong(event.aggregateId()));
            case "ACTIVITY_CLOSE" -> {
                // The offline path closes the gate before committing; this row is retained as evidence.
            }
            case "ORDER_CHANGED" -> compensationService.apply(event.eventId(), event.payload());
            default -> throw new NeedsReviewException("Unsupported event type " + event.eventType());
        }
    }
}
