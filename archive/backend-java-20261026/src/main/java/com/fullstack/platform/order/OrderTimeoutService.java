package com.fullstack.platform.order;

import lombok.extern.slf4j.Slf4j;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Service;

import java.util.List;

/** DB scan is the source of truth; a lost ZSet hint cannot keep an order unpaid forever. */
@Slf4j
@Service
public class OrderTimeoutService {
    private final JdbcTemplate jdbc;
    private final OrderLifecycleService lifecycle;

    public OrderTimeoutService(JdbcTemplate jdbc, OrderLifecycleService lifecycle) {
        this.jdbc = jdbc;
        this.lifecycle = lifecycle;
    }

    @Scheduled(fixedDelay = 5000)
    public void closeExpired() {
        List<Long> ids;
        try {
            ids = jdbc.queryForList("""
                    SELECT id FROM tb_voucher_order
                    WHERE status = 1 AND pay_deadline_at <= NOW(3)
                    ORDER BY pay_deadline_at, id
                    LIMIT 100
                    """, Long.class);
        } catch (Exception e) {
            log.warn("Order timeout scan failed; will retry", e);
            return;
        }
        for (Long id : ids) {
            try {
                lifecycle.cancelExpired(id);
            } catch (Exception e) {
                log.warn("Failed to close expired order {}; will retry", id, e);
            }
        }
    }
}
