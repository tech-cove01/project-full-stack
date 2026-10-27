package com.fullstack.platform.dev;

import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.Shop;
import com.fullstack.platform.service.ISeckillVoucherService;
import com.fullstack.platform.service.IShopService;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.data.geo.Point;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;

import java.util.LinkedHashMap;
import java.util.Map;

import static com.fullstack.platform.utils.RedisConstants.*;

/** Seeds shop GEO data and guards existing activity admission state on dev startup. */
@Slf4j
@Component
@Profile("dev")
@RequiredArgsConstructor
public class DevDataInitializer implements ApplicationRunner {
    private final IShopService shops;
    private final ISeckillVoucherService vouchers;
    private final StringRedisTemplate redis;

    @Override
    public void run(ApplicationArguments args) {
        for (Shop shop : shops.list()) {
            if (shop.getX() != null && shop.getY() != null) {
                redis.opsForGeo().add(SHOP_GEO_KEY + shop.getTypeId(),
                        new Point(shop.getX(), shop.getY()), shop.getId().toString());
            }
        }
        for (SeckillVoucher voucher : vouchers.list()) {
            // Only ACTIVE activities have a live admission gate. INITIALIZING/OFFLINE state is
            // owned by the publication protocol and must not be created from the database here.
            if (!"ACTIVE".equals(voucher.getPublishStatus())) {
                continue;
            }
            guardActive(voucher);
        }
    }

    /**
     * Fail closed on restart. An ACTIVE activity whose Redis stock or gate is missing is never
     * rebuilt from the DB stock: accepted-but-not-created reservations are not reflected in that
     * number, so rebuilding would admit stock that is already promised. Mark it for explicit
     * reconciliation and keep admission closed instead of writing OPEN.
     */
    private void guardActive(SeckillVoucher voucher) {
        String id = voucher.getVoucherId().toString();
        String gateKey = SECKILL_GATE_KEY + id;
        int version = voucher.getPublishVersion() == null ? 1 : voucher.getPublishVersion();

        boolean stockReady = Boolean.TRUE.equals(redis.hasKey(SECKILL_STOCK_KEY + id));
        Object gateState = redis.opsForHash().get(gateKey, "state");
        Object gateVersion = redis.opsForHash().get(gateKey, "version");
        boolean gateReady = "OPEN".equals(gateState) || "CLOSED".equals(gateState);
        boolean versionReady = gateVersion != null && Integer.toString(version).equals(gateVersion.toString());

        if (stockReady && gateReady && versionReady) {
            redis.delete(SECKILL_RECONCILE_KEY + id);
            return;
        }
        String reason = !stockReady ? "STOCK_MISSING" : !gateReady ? "GATE_MISSING" : "VERSION_MISMATCH";
        Map<String, String> marker = new LinkedHashMap<>();
        marker.put("reason", reason);
        marker.put("dbStock", String.valueOf(voucher.getStock()));
        marker.put("publishVersion", Integer.toString(version));
        marker.put("detectedAt", Long.toString(System.currentTimeMillis()));
        redis.opsForHash().putAll(SECKILL_RECONCILE_KEY + id, marker);

        // Admission stays closed; the stock key is deliberately left untouched.
        redis.opsForHash().put(gateKey, "state", "CLOSED");
        log.error("Activity {} is ACTIVE but Redis is not ready ({}); admission kept closed. "
                + "Run the explicit reconciliation endpoint after all reservations are settled.", id, reason);
    }
}
