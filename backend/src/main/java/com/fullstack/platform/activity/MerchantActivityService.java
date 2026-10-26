package com.fullstack.platform.activity;

import com.fullstack.platform.audit.AuditService;
import com.fullstack.platform.dto.UserDTO;
import com.fullstack.platform.entity.SeckillVoucher;
import com.fullstack.platform.entity.Voucher;
import com.fullstack.platform.merchant.ShopAccessService;
import com.fullstack.platform.mapper.SeckillVoucherMapper;
import com.fullstack.platform.mapper.VoucherMapper;
import com.fullstack.platform.outbox.NeedsReviewException;
import com.fullstack.platform.outbox.OutboxRepository;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.web.server.ResponseStatusException;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.LocalDateTime;
import java.time.OffsetDateTime;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;

import static com.fullstack.platform.utils.RedisConstants.SECKILL_GATE_KEY;
import static com.fullstack.platform.utils.RedisConstants.SECKILL_RECONCILE_KEY;

/** Merchant activity lifecycle: create with idempotency, list, retry publication, offline. */
@Service
public class MerchantActivityService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private static final DateTimeFormatter DISPLAY_TIME = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");

    /**
     * Returns 0 when the order Stream has nothing in flight, 1 when a consumer group still has
     * pending entries, 2 when delivered-but-unread entries exist. Redis loss recovery must not
     * rebuild stock while either is non-zero.
     */
    private static final DefaultRedisScript<Long> INFLIGHT_SCRIPT = new DefaultRedisScript<>("""
            local ok, groups = pcall(redis.call, 'XINFO', 'GROUPS', KEYS[1])
            if not ok then return 0 end
            for _, group in ipairs(groups) do
              local pending, lag = 0, 0
              for i = 1, #group, 2 do
                if group[i] == 'pending' then pending = tonumber(group[i + 1]) or 0 end
                if group[i] == 'lag' then lag = tonumber(group[i + 1]) or 0 end
              end
              if pending > 0 then return 1 end
              if lag > 0 then return 2 end
            end
            return 0
            """, Long.class);

    private final JdbcTemplate jdbc;
    private final VoucherMapper voucherMapper;
    private final SeckillVoucherMapper seckillMapper;
    private final ShopAccessService access;
    private final ActivityPublicationService publication;
    private final AuditService audit;
    private final OutboxRepository outbox;
    private final StringRedisTemplate redis;

    public MerchantActivityService(JdbcTemplate jdbc, VoucherMapper voucherMapper,
                                   SeckillVoucherMapper seckillMapper, ShopAccessService access,
                                   ActivityPublicationService publication, AuditService audit,
                                   OutboxRepository outbox, StringRedisTemplate redis) {
        this.jdbc = jdbc;
        this.voucherMapper = voucherMapper;
        this.seckillMapper = seckillMapper;
        this.access = access;
        this.publication = publication;
        this.audit = audit;
        this.outbox = outbox;
        this.redis = redis;
    }

    @Transactional(rollbackFor = Exception.class)
    public ActivityView create(String idempotencyKey, CreateActivityRequest request) {
        UserDTO user = access.requireUser();
        if (idempotencyKey == null || !idempotencyKey.matches("[A-Za-z0-9_-]{8,64}")) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "Idempotency-Key 缺失或格式错误");
        }
        long shopId = positiveLong(request.shopId(), "shopId");
        ShopAccessService.Membership membership = access.requireManager(shopId, user.getId());
        ValidatedActivity validated = validate(request);
        String hash = requestHash(shopId, validated);

        List<Map<String, Object>> existing = jdbc.queryForList("""
                SELECT voucher_id, publish_request_hash
                FROM tb_seckill_voucher
                WHERE merchant_id = ? AND publish_request_key = ?
                """, membership.merchantId(), idempotencyKey);
        if (!existing.isEmpty()) {
            long existingId = ((Number) existing.getFirst().get("voucher_id")).longValue();
            String existingHash = (String) existing.getFirst().get("publish_request_hash");
            if (!hash.equals(existingHash)) {
                throw new ResponseStatusException(HttpStatus.CONFLICT, "相同请求键对应不同发布内容");
            }
            return viewFor(existingId);
        }

        Voucher voucher = new Voucher()
                .setShopId(shopId)
                .setTitle(validated.title())
                .setSubTitle(validated.subTitle())
                .setRules(validated.rules())
                .setPayValue(validated.payValue())
                .setActualValue(validated.actualValue())
                .setType(1)
                .setStatus(1);
        try {
            voucherMapper.insert(voucher);
        } catch (DuplicateKeyException e) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "发布请求重复，请用同一请求键重试");
        }

        SeckillVoucher activity = new SeckillVoucher()
                .setVoucherId(voucher.getId())
                .setMerchantId(membership.merchantId())
                .setStock(validated.totalStock())
                .setTotalStock(validated.totalStock())
                .setPublishStatus("INITIALIZING")
                .setPublishVersion(0)
                .setBeginTime(validated.beginTime())
                .setEndTime(validated.endTime())
                .setRedeemBeginTime(validated.redeemBeginTime())
                .setRedeemEndTime(validated.redeemEndTime())
                .setPublishRequestKey(idempotencyKey)
                .setPublishRequestHash(hash);
        try {
            seckillMapper.insert(activity);
        } catch (DuplicateKeyException e) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "发布请求重复，请用同一请求键查询");
        }

        audit.record(membership.merchantId(), shopId, user.getId(), "MERCHANT",
                "ACTIVITY_CREATE", Long.toString(voucher.getId()), Map.of(
                        "title", validated.title(),
                        "totalStock", validated.totalStock(),
                        "beginTime", validated.beginTime().format(DISPLAY_TIME),
                        "endTime", validated.endTime().format(DISPLAY_TIME)));
        outbox.enqueue("ACTIVITY:" + voucher.getId() + ":0:PREPARE", "ACTIVITY",
                Long.toString(voucher.getId()), 0, "ACTIVITY_PREPARE",
                Map.of("activityId", Long.toString(voucher.getId())));
        return viewFor(voucher.getId());
    }

    public Page list(int current, int pageSize, String shopId, String status) {
        UserDTO user = access.requireUser();
        int page = Math.max(current, 1);
        int size = Math.min(Math.max(pageSize, 1), 50);
        List<Long> visible = access.visibleShopIds(user.getId());
        if (visible.isEmpty()) {
            return new Page(List.of(), 0L);
        }
        StringBuilder where = new StringBuilder(" WHERE v.shop_id IN ("
                + String.join(",", java.util.Collections.nCopies(visible.size(), "?")) + ")");
        List<Object> args = new ArrayList<>(visible);
        if (shopId != null && !shopId.isBlank()) {
            long requested = positiveLong(shopId, "shopId");
            if (!visible.contains(requested)) {
                return new Page(List.of(), 0L);
            }
            where.append(" AND v.shop_id = ?");
            args.add(requested);
        }
        if (status != null && !status.isBlank()) {
            if (!List.of("INITIALIZING", "ACTIVE", "OFFLINE").contains(status)) {
                throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "未知活动状态");
            }
            where.append(" AND sv.publish_status = ?");
            args.add(status);
        }
        String from = """
                 FROM tb_seckill_voucher sv
                 JOIN tb_voucher v ON v.id = sv.voucher_id
                 JOIN tb_shop s ON s.id = v.shop_id
                """ + where;
        Long total = jdbc.queryForObject("SELECT COUNT(*)" + from, Long.class, args.toArray());
        args.add(size);
        args.add((page - 1) * size);
        List<ActivityView> items = jdbc.query("""
                        SELECT sv.voucher_id, v.shop_id, s.name AS shop_name, v.title, v.sub_title,
                               v.pay_value, v.actual_value, sv.total_stock, sv.stock,
                               sv.publish_status, sv.publish_version, sv.begin_time, sv.end_time,
                               sv.redeem_begin_time, sv.redeem_end_time
                        """ + from + " ORDER BY sv.voucher_id DESC LIMIT ? OFFSET ?",
                ROW_MAPPER, args.toArray())
                .stream().map(this::toView).toList();
        return new Page(items, total == null ? 0L : total);
    }

    public ActivityView retryPublication(long activityId) {
        UserDTO user = access.requireUser();
        SeckillVoucher activity = requireActivity(activityId);
        Voucher voucher = requireVoucher(activityId);
        access.requireManager(voucher.getShopId(), user.getId());
        if ("OFFLINE".equals(activity.getPublishStatus())) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "活动已下架");
        }
        try {
            publication.prepareAndOpen(activityId);
        } catch (NeedsReviewException e) {
            throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "活动准备需要人工核对");
        }
        return viewFor(activityId);
    }

    @Transactional(rollbackFor = Exception.class)
    public ActivityView offline(long activityId) {
        UserDTO user = access.requireUser();
        SeckillVoucher activity = requireActivity(activityId);
        Voucher voucher = requireVoucher(activityId);
        ShopAccessService.Membership membership = access.requireManager(voucher.getShopId(), user.getId());

        Map<String, Object> locked = jdbc.queryForMap(
                "SELECT publish_status, publish_version FROM tb_seckill_voucher WHERE voucher_id = ? FOR UPDATE",
                activityId);
        String status = (String) locked.get("publish_status");
        int version = ((Number) locked.get("publish_version")).intValue();
        if ("OFFLINE".equals(status)) {
            return viewFor(activityId);
        }
        int next = version + 1;
        long closed = publication.closeGate(activityId, next);
        if (closed < 0) {
            throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "Redis 门控关闭失败");
        }
        jdbc.update("UPDATE tb_seckill_voucher SET publish_status = 'OFFLINE', publish_version = ? "
                + "WHERE voucher_id = ?", next, activityId);
        audit.record(membership.merchantId(), voucher.getShopId(), user.getId(), "MERCHANT",
                "ACTIVITY_OFFLINE", Long.toString(activityId), Map.of("version", next));
        outbox.enqueue("ACTIVITY:" + activityId + ":" + next + ":CLOSE", "ACTIVITY",
                Long.toString(activityId), next, "ACTIVITY_CLOSE",
                Map.of("activityId", Long.toString(activityId), "version", next));
        return viewFor(activityId);
    }

    /**
     * Explicit recovery for an ACTIVE activity whose Redis stock/gate was lost.
     * It refuses to run while any Stream reservation is still in flight, rebuilds the gate as
     * CLOSED at a new version, and lets the outbox open it after the DB commit.
     */
    @Transactional(rollbackFor = Exception.class)
    public ActivityView reconcile(long activityId, boolean confirm) {
        UserDTO user = access.requireUser();
        Voucher voucher = requireVoucher(activityId);
        ShopAccessService.Membership membership = access.requireManager(voucher.getShopId(), user.getId());
        if (!confirm) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "需要显式确认已处理完在途预约");
        }
        Map<String, Object> locked = jdbc.queryForMap(
                "SELECT publish_status, publish_version FROM tb_seckill_voucher WHERE voucher_id = ? FOR UPDATE",
                activityId);
        String status = (String) locked.get("publish_status");
        int version = ((Number) locked.get("publish_version")).intValue();
        if (!"ACTIVE".equals(status)) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "只有 ACTIVE 活动需要重建 Redis 状态");
        }
        requireNoInflightReservations();

        int next = version + 1;
        long prepared = publication.prepareForReconcile(activityId, next);
        if (prepared == -2) {
            throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "Redis 状态无法安全重建，需人工核对");
        }
        if (prepared == 0) {
            return viewFor(activityId);
        }
        jdbc.update("UPDATE tb_seckill_voucher SET publish_version = ? "
                + "WHERE voucher_id = ? AND publish_version = ?", next, activityId, version);
        audit.record(membership.merchantId(), voucher.getShopId(), user.getId(), "MERCHANT",
                "ACTIVITY_RECONCILED", Long.toString(activityId),
                Map.of("version", next, "previousVersion", version));
        outbox.enqueue("ACTIVITY:" + activityId + ":" + next + ":OPEN", "ACTIVITY",
                Long.toString(activityId), next, "ACTIVITY_OPEN",
                Map.of("activityId", Long.toString(activityId), "version", next));
        redis.delete(SECKILL_RECONCILE_KEY + activityId);
        return viewFor(activityId);
    }

    private void requireNoInflightReservations() {
        Long inflight = redis.execute(INFLIGHT_SCRIPT, List.of("stream.orders"));
        if (inflight == null || inflight == 0) {
            return;
        }
        if (inflight == 1) {
            throw new ResponseStatusException(HttpStatus.CONFLICT,
                    "仍有未确认的订单消息，处理完成后才能重建");
        }
        throw new ResponseStatusException(HttpStatus.CONFLICT,
                "仍有未消费的订单消息，处理完成后才能重建");
    }

    public ActivityView viewFor(long activityId) {
        List<ActivityRow> rows = jdbc.query("""
                SELECT sv.voucher_id, v.shop_id, s.name AS shop_name, v.title, v.sub_title,
                       v.pay_value, v.actual_value, sv.total_stock, sv.stock,
                       sv.publish_status, sv.publish_version, sv.begin_time, sv.end_time,
                       sv.redeem_begin_time, sv.redeem_end_time
                FROM tb_seckill_voucher sv
                JOIN tb_voucher v ON v.id = sv.voucher_id
                JOIN tb_shop s ON s.id = v.shop_id
                WHERE sv.voucher_id = ?
                """, ROW_MAPPER, activityId);
        if (rows.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "活动不存在");
        }
        return toView(rows.getFirst());
    }

    private ActivityView toView(ActivityRow row) {
        return new ActivityView(Long.toString(row.id()), Long.toString(row.shopId()), row.shopName(),
                row.title(), row.subTitle(), Long.toString(row.payValue()), Long.toString(row.actualValue()),
                row.totalStock(), row.stock(), row.publishStatus(), row.publishVersion(),
                isReady(row.id(), row.publishVersion()), row.beginTime().format(DISPLAY_TIME),
                row.endTime().format(DISPLAY_TIME),
                row.redeemBeginTime() == null ? null : row.redeemBeginTime().format(DISPLAY_TIME),
                row.redeemEndTime() == null ? null : row.redeemEndTime().format(DISPLAY_TIME));
    }

    private boolean isReady(long activityId, int publishVersion) {
        try {
            Map<Object, Object> gate = redis.opsForHash().entries(SECKILL_GATE_KEY + activityId);
            return "OPEN".equals(gate.get("state"))
                    && Integer.toString(publishVersion).equals(String.valueOf(gate.get("version")));
        } catch (RuntimeException e) {
            return false;
        }
    }

    private SeckillVoucher requireActivity(long activityId) {
        SeckillVoucher activity = seckillMapper.selectById(activityId);
        if (activity == null) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "活动不存在");
        }
        return activity;
    }

    private Voucher requireVoucher(long activityId) {
        Voucher voucher = voucherMapper.selectById(activityId);
        if (voucher == null) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "活动不存在");
        }
        return voucher;
    }

    private static final RowMapper<ActivityRow> ROW_MAPPER = (rs, rowNum) -> new ActivityRow(
            rs.getLong("voucher_id"), rs.getLong("shop_id"), rs.getString("shop_name"),
            rs.getString("title"), rs.getString("sub_title"), rs.getLong("pay_value"),
            rs.getLong("actual_value"), rs.getInt("total_stock"), rs.getInt("stock"),
            rs.getString("publish_status"), rs.getInt("publish_version"),
            rs.getObject("begin_time", LocalDateTime.class),
            rs.getObject("end_time", LocalDateTime.class),
            rs.getObject("redeem_begin_time", LocalDateTime.class),
            rs.getObject("redeem_end_time", LocalDateTime.class));

    private record ActivityRow(long id, long shopId, String shopName, String title, String subTitle,
                               long payValue, long actualValue, int totalStock, int stock,
                               String publishStatus, int publishVersion, LocalDateTime beginTime,
                               LocalDateTime endTime, LocalDateTime redeemBeginTime,
                               LocalDateTime redeemEndTime) { }

    public record Page(List<ActivityView> items, long total) { }

    private record ValidatedActivity(String title, String subTitle, String rules, long payValue,
                                     long actualValue, int totalStock, LocalDateTime beginTime,
                                     LocalDateTime endTime, LocalDateTime redeemBeginTime,
                                     LocalDateTime redeemEndTime) { }

    private ValidatedActivity validate(CreateActivityRequest request) {
        String title = required(request.title(), "标题", 255);
        String subTitle = optional(request.subTitle(), 255);
        String rules = optional(request.rules(), 1024);
        long payValue = positiveLong(request.payValue(), "购买价格");
        long actualValue = positiveLong(request.actualValue(), "抵扣面值");
        if (payValue > actualValue) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "购买价格不能高于抵扣面值");
        }
        if (request.totalStock() == null || request.totalStock() <= 0 || request.totalStock() > 1_000_000) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "库存必须为 1 到 1000000 的整数");
        }
        LocalDateTime begin = parseTime(request.beginTime(), "销售开始时间");
        LocalDateTime end = parseTime(request.endTime(), "销售结束时间");
        LocalDateTime redeemBegin = parseTime(request.redeemBeginTime(), "核销开始时间");
        LocalDateTime redeemEnd = parseTime(request.redeemEndTime(), "核销结束时间");
        if (!begin.isBefore(end)) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "销售开始时间必须早于结束时间");
        }
        if (!redeemBegin.isBefore(redeemEnd)) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "核销开始时间必须早于结束时间");
        }
        if (!redeemEnd.isAfter(end)) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "核销结束时间必须晚于销售结束时间");
        }
        return new ValidatedActivity(title, subTitle, rules, payValue, actualValue,
                request.totalStock(), begin, end, redeemBegin, redeemEnd);
    }

    private static String required(String value, String field, int max) {
        if (value == null || value.isBlank()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "不能为空");
        }
        String trimmed = value.trim();
        if (trimmed.length() > max) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "过长");
        }
        return trimmed;
    }

    private static String optional(String value, int max) {
        if (value == null) return null;
        String trimmed = value.trim();
        if (trimmed.isEmpty()) return null;
        if (trimmed.length() > max) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "文本过长");
        }
        return trimmed;
    }

    private static long positiveLong(String value, String field) {
        try {
            long parsed = Long.parseLong(value == null ? "" : value.trim());
            if (parsed <= 0) throw new NumberFormatException();
            return parsed;
        } catch (NumberFormatException e) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "必须为正整数");
        }
    }

    private static LocalDateTime parseTime(String value, String field) {
        if (value == null || value.isBlank()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "不能为空");
        }
        try {
            return OffsetDateTime.parse(value).atZoneSameInstant(BUSINESS_ZONE).toLocalDateTime();
        } catch (DateTimeParseException ignored) {
            try {
                return LocalDateTime.parse(value);
            } catch (DateTimeParseException e) {
                throw new ResponseStatusException(HttpStatus.BAD_REQUEST, field + "格式错误");
            }
        }
    }

    private static String requestHash(long shopId, ValidatedActivity value) {
        String canonical = String.join("|", Long.toString(shopId), value.title(),
                value.subTitle() == null ? "" : value.subTitle(),
                value.rules() == null ? "" : value.rules(),
                Long.toString(value.payValue()), Long.toString(value.actualValue()),
                Integer.toString(value.totalStock()),
                value.beginTime().toString(), value.endTime().toString(),
                value.redeemBeginTime().toString(), value.redeemEndTime().toString());
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256")
                    .digest(canonical.getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(digest);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }
}
