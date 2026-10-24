package com.fullstack.platform.outbox;

/** A non-retryable or unsafe state that must be inspected before replay. */
public class NeedsReviewException extends RuntimeException {
    public NeedsReviewException(String message) {
        super(message);
    }
}
