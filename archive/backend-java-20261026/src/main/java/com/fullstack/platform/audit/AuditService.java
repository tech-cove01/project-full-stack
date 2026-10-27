package com.fullstack.platform.audit;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;

import java.util.Map;

/** Successful business changes and their audit rows share the caller's transaction. */
@Service
public class AuditService {
    private final JdbcTemplate jdbc;
    private final ObjectMapper objectMapper;

    public AuditService(JdbcTemplate jdbc, ObjectMapper objectMapper) {
        this.jdbc = jdbc;
        this.objectMapper = objectMapper;
    }

    public void record(long merchantId, Long shopId, long operatorUserId, String operatorType,
                       String action, String resourceId, Map<String, Object> detail) {
        String json;
        try {
            json = objectMapper.writeValueAsString(detail == null ? Map.of() : detail);
        } catch (JsonProcessingException e) {
            throw new IllegalStateException("Cannot serialize audit detail", e);
        }
        jdbc.update("""
                INSERT INTO tb_audit_log
                    (merchant_id, shop_id, operator_user_id, operator_type, action, resource_id, detail)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                """, merchantId, shopId, operatorUserId, operatorType, action, resourceId, json);
    }
}
