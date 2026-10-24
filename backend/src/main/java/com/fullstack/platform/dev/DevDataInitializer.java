package com.fullstack.platform.dev;

import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.Shop;
import com.fullstack.platform.service.ISeckillVoucherService;
import com.fullstack.platform.service.IShopService;
import lombok.RequiredArgsConstructor;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.data.geo.Point;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.stereotype.Component;

import java.time.ZoneId;

import static com.fullstack.platform.utils.RedisConstants.SECKILL_STOCK_KEY;
import static com.fullstack.platform.utils.RedisConstants.SHOP_GEO_KEY;

/** Seeds Redis from the local demo database without resetting live reservations. */
@Component
@Profile("dev")
@RequiredArgsConstructor
public class DevDataInitializer implements ApplicationRunner {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

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
            String id = voucher.getVoucherId().toString();
            redis.opsForValue().setIfAbsent(SECKILL_STOCK_KEY + id, voucher.getStock().toString());
            redis.opsForValue().setIfAbsent("seckill:begin:" + id,
                    Long.toString(voucher.getBeginTime().atZone(BUSINESS_ZONE).toInstant().toEpochMilli()));
            redis.opsForValue().setIfAbsent("seckill:end:" + id,
                    Long.toString(voucher.getEndTime().atZone(BUSINESS_ZONE).toInstant().toEpochMilli()));
        }
    }
}
