package com.fullstack.platform.utils;

public class RedisConstants {
    public static final String LOGIN_CODE_KEY = "login:code:";
    public static final Long LOGIN_CODE_TTL = 2L;
    public static final String LOGIN_USER_KEY = "login:token:";
    public static final Long LOGIN_USER_TTL = 36000L;

    public static final Long CACHE_NULL_TTL = 2L;

    public static final Long CACHE_SHOP_TTL = 30L;
    public static final String CACHE_SHOP_KEY = "cache:shop:";

    public static final String LOCK_SHOP_KEY = "lock:shop:";
    public static final Long LOCK_SHOP_TTL = 10L;

    public static final String SECKILL_STOCK_KEY = "seckill:stock:";
    public static final String SECKILL_BEGIN_KEY = "seckill:begin:";
    public static final String SECKILL_END_KEY = "seckill:end:";
    public static final String SECKILL_GATE_KEY = "seckill:gate:";
    public static final String SECKILL_ACCEPTED_KEY = "seckill:accepted:";
    public static final String SECKILL_PURCHASE_KEY = "seckill:order:";
    /** Set when an ACTIVE activity's Redis admission state is missing or out of version. */
    public static final String SECKILL_RECONCILE_KEY = "seckill:reconcile:";
    public static final String BLOG_LIKED_KEY = "blog:liked:";
    public static final String FEED_KEY = "feed:";
    public static final String SHOP_GEO_KEY = "shop:geo:";
    public static final String USER_SIGN_KEY = "sign:";
}
