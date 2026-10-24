package com.fullstack.platform.outbox;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;

@Service
public class OutboxRepository {
    private static final int MAX_ATTEMPTS = 8;

    private final JdbcTemplate jdbc;
    private final ObjectMapper objectMapper;
    private final TransactionTemplate transactionTemplate;

    public OutboxRepository(JdbcTemplate jdbc, ObjectMapper objectMapper, PlatformTransactionManager txManager) {
        this.jdbc = jdbc;
        this.objectMapper = objectMapper;
        this.transactionTemplate = new TransactionTemplate(txManager);
    }

    public void enqueue(String eventId, String aggregateType, String aggregateId, int aggregateVersion,
                        String eventType, Map<String, Object> payload) {
        String json;
        try {
            json = objectMapper.writeValueAsString(payload == null ? Map.of() : payload);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("Cannot serialize outbox payload", e);
        }
        jdbc.update("""
                INSERT IGNORE INTO tb_outbox
                    (event_id, aggregate_type, aggregate_id, aggregate_version, event_type, payload)
                VALUES (?, ?, ?, ?, ?, ?)
                """, eventId, aggregateType, aggregateId, aggregateVersion, eventType, json);
    }

    /** Claims due rows and takes a short lease. SKIP LOCKED makes multiple instances safe. */
    public List<OutboxEvent> claim(int limit) {
        return transactionTemplate.execute(status -> {
            List<OutboxEvent> rows = jdbc.query("""
                    SELECT id, event_id, aggregate_type, aggregate_id, aggregate_version,
                           event_type, payload, attempts
                    FROM tb_outbox
                    WHERE (delivery_status = 'PENDING' AND next_attempt_at <= NOW(3))
                       OR (delivery_status = 'PROCESSING' AND lease_until < NOW(3))
                    ORDER BY id
                    LIMIT ?
                    FOR UPDATE SKIP LOCKED
                    """, (rs, rowNum) -> new OutboxEvent(
                    rs.getLong("id"), rs.getString("event_id"), rs.getString("aggregate_type"),
                    rs.getString("aggregate_id"), rs.getInt("aggregate_version"),
                    rs.getString("event_type"), rs.getString("payload"), rs.getInt("attempts")), limit);
            if (rows.isEmpty()) {
                return rows;
            }
            String ids = rows.stream().map(row -> Long.toString(row.id())).collect(Collectors.joining(","));
            jdbc.update("UPDATE tb_outbox SET delivery_status = 'PROCESSING', "
                    + "lease_until = DATE_ADD(NOW(3), INTERVAL 30 SECOND), attempts = attempts + 1 "
                    + "WHERE id IN (" + ids + ")");
            return rows;
        });
    }

    public void markDone(long id) {
        jdbc.update("UPDATE tb_outbox SET delivery_status = 'DONE', completed_at = NOW(3), "
                + "lease_until = NULL, last_error = NULL WHERE id = ?", id);
    }

    public void retry(OutboxEvent event, String error) {
        int attempts = event.attempts() + 1;
        String message = truncate(error);
        if (attempts >= MAX_ATTEMPTS) {
            needsReview(event, error);
            return;
        }
        long delaySeconds = Math.min(60, 1L << Math.min(attempts, 6));
        jdbc.update("UPDATE tb_outbox SET delivery_status = 'PENDING', lease_until = NULL, "
                        + "next_attempt_at = DATE_ADD(NOW(3), INTERVAL ? SECOND), last_error = ? WHERE id = ?",
                delaySeconds, message, event.id());
    }

    public void needsReview(OutboxEvent event, String error) {
        jdbc.update("UPDATE tb_outbox SET delivery_status = 'NEEDS_REVIEW', lease_until = NULL, "
                + "last_error = ? WHERE id = ?", truncate(error), event.id());
    }

    private static String truncate(String error) {
        if (error == null) return null;
        return error.length() <= 500 ? error : error.substring(0, 500);
    }
}
