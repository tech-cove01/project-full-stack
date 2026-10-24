package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fullstack.platform.audit.AuditService;
import org.springframework.http.HttpStatus;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.support.GeneratedKeyHolder;
import org.springframework.jdbc.support.KeyHolder;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionTemplate;
import org.springframework.web.server.ResponseStatusException;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.sql.PreparedStatement;
import java.sql.Statement;
import java.time.LocalDateTime;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;

/** Draft -> immutable version -> chunk/embedding -> READY publication pointer. */
@Service
public class AiKnowledgeService {
    private static final DateTimeFormatter DISPLAY = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss");
    private static final int MAX_CHUNKS = 2000;

    private final JdbcTemplate jdbc;
    private final ObjectMapper objectMapper;
    private final AiProvider provider;
    private final AiProperties properties;
    private final AiIndexService index;
    private final AuditService audit;
    private final TransactionTemplate transactionTemplate;

    public AiKnowledgeService(JdbcTemplate jdbc, ObjectMapper objectMapper, AiProvider provider,
                              AiProperties properties, AiIndexService index, AuditService audit,
                              PlatformTransactionManager txManager) {
        this.jdbc = jdbc;
        this.objectMapper = objectMapper;
        this.provider = provider;
        this.properties = properties;
        this.index = index;
        this.audit = audit;
        this.transactionTemplate = new TransactionTemplate(txManager);
    }

    @Transactional(rollbackFor = Exception.class)
    public long createDraft(long shopId, String title, String body, long userId) {
        long documentId = insertDocument(null, "SHOP_PUBLIC", shopId, title, body, "DRAFT", userId);
        audit.record(merchantOf(shopId), shopId, userId, "MERCHANT", "AI_KNOWLEDGE_CREATE",
                Long.toString(documentId), Map.of("title", title));
        return documentId;
    }

    @Transactional(rollbackFor = Exception.class)
    public void updateDraft(long documentId, String title, String body, long userId) {
        Document document = requireDocument(documentId);
        if (document.disabled()) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "已停用知识不可编辑");
        }
        if (document.publishedVersion() != null) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "已发布版本不可原地修改，请新建草稿");
        }
        jdbc.update("UPDATE tb_ai_document SET title = ?, body = ?, updated_by = ?, status = 'DRAFT' "
                + "WHERE id = ?", requireText(title, 255), requireText(body, 20000), userId, documentId);
        audit.record(merchantOf(document.shopId()), document.shopId(), userId, "MERCHANT",
                "AI_KNOWLEDGE_UPDATE", Long.toString(documentId), Map.of("title", title));
    }

    public synchronized AiKnowledgeView publish(long documentId, long operatorId, String operatorType) {
        Document document = requireDocument(documentId);
        if (document.disabled()) {
            throw new ResponseStatusException(HttpStatus.CONFLICT, "已停用知识不可发布");
        }
        if (document.body() == null || document.body().isBlank()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "知识正文不能为空");
        }
        if (!provider.ready()) {
            throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "AI provider 未就绪：" + provider.reason());
        }
        int version = nextVersion(documentId);
        long epoch = document.epoch() + 1;
        List<String> chunks = AiChunker.split(document.body(), 400, 50);
        if (chunks.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "知识正文无法分块");
        }
        Integer existing = jdbc.queryForObject("SELECT COUNT(*) FROM tb_ai_chunk", Integer.class);
        if ((existing == null ? 0 : existing) + chunks.size() > MAX_CHUNKS) {
            throw new ResponseStatusException(HttpStatus.CONFLICT,
                    "知识块超过 " + MAX_CHUNKS + " 上限，拒绝发布");
        }
        String model = provider.embeddingModel();
        int dimension = properties.getEmbeddingDimensions();
        String hash = hash(document.body());
        try {
            jdbc.update("""
                    INSERT INTO tb_ai_document_version
                        (document_id, version, epoch, body, content_hash, status, embedding_model, embedding_dimension)
                    VALUES (?, ?, ?, ?, ?, 'INDEXING', ?, ?)
                    """, documentId, version, epoch, document.body(), hash, model, dimension);

            List<float[]> vectors = new ArrayList<>(chunks.size());
            for (String chunk : chunks) vectors.add(provider.embed(chunk));

            transactionTemplate.executeWithoutResult(status -> {
                for (int i = 0; i < chunks.size(); i++) {
                    jdbc.update("""
                            INSERT INTO tb_ai_chunk
                                (document_id, version, epoch, ordinal, content, embedding,
                                 embedding_model, embedding_dimension)
                            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                            """, documentId, version, epoch, i, chunks.get(i),
                            toJson(vectors.get(i)), model, dimension);
                }
                jdbc.update("UPDATE tb_ai_document_version SET status = 'READY', ready_time = NOW() "
                        + "WHERE document_id = ? AND version = ?", documentId, version);
                jdbc.update("""
                        UPDATE tb_ai_document
                        SET published_version = ?, epoch = ?, status = 'PUBLISHED', updated_by = ?
                        WHERE id = ? AND disabled = 0
                        """, version, epoch, operatorId, documentId);
            });
            audit.record(merchantOf(document.shopId()), document.shopId(), operatorId, operatorType,
                    "AI_KNOWLEDGE_PUBLISHED", Long.toString(documentId),
                    Map.of("version", version, "chunks", chunks.size(), "model", model));
            index.refresh();
            return view(documentId);
        } catch (RuntimeException e) {
            String message = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
            jdbc.update("UPDATE tb_ai_document_version SET status = 'FAILED', error = ? "
                    + "WHERE document_id = ? AND version = ?", truncate(message), documentId, version);
            if (e instanceof ResponseStatusException) throw e;
            throw new ResponseStatusException(HttpStatus.SERVICE_UNAVAILABLE, "知识发布失败：" + message);
        }
    }

    @Transactional(rollbackFor = Exception.class)
    public void disable(long documentId, long operatorId, String operatorType) {
        Document document = requireDocument(documentId);
        jdbc.update("""
                UPDATE tb_ai_document
                SET disabled = 1, status = 'DISABLED', published_version = NULL, epoch = epoch + 1,
                    updated_by = ?
                WHERE id = ?
                """, operatorId, documentId);
        audit.record(merchantOf(document.shopId()), document.shopId(), operatorId, operatorType,
                "AI_KNOWLEDGE_DISABLED", Long.toString(documentId), Map.of("previousVersion",
                        document.publishedVersion() == null ? "" : document.publishedVersion().toString()));
        index.refresh();
    }

    public List<AiKnowledgeView> list(long shopId, boolean includeDrafts) {
        String sql = """
                SELECT d.id, d.seed_key, d.scope, d.shop_id, d.title, d.body, d.status,
                       d.published_version, d.epoch, d.disabled, d.update_time
                FROM tb_ai_document d
                WHERE d.scope = 'SHOP_PUBLIC' AND d.shop_id = ?
                """ + (includeDrafts ? "" : " AND d.published_version IS NOT NULL AND d.disabled = 0")
                + " ORDER BY d.id DESC";
        return jdbc.query(sql, (rs, rowNum) -> {
            long id = rs.getLong("id");
            Map<String, Object> latest = latestVersion(id);
            return new AiKnowledgeView(
                    Long.toString(id), rs.getString("seed_key"), rs.getString("scope"),
                    Long.toString(rs.getLong("shop_id")), rs.getString("title"), rs.getString("body"),
                    rs.getString("status"),
                    rs.getObject("published_version") == null ? null : rs.getInt("published_version"),
                    rs.getLong("epoch"), rs.getBoolean("disabled"),
                    latest == null ? null : ((Number) latest.get("version")).intValue(),
                    latest == null ? null : (String) latest.get("status"),
                    latest == null ? null : (String) latest.get("error"),
                    rs.getObject("update_time", LocalDateTime.class).format(DISPLAY));
        }, shopId);
    }

    public List<AiKnowledgeView> listPlatform() {
        return jdbc.query("""
                SELECT id, seed_key, scope, shop_id, title, body, status, published_version, epoch,
                       disabled, update_time
                FROM tb_ai_document WHERE scope = 'PLATFORM' ORDER BY id
                """, (rs, rowNum) -> new AiKnowledgeView(
                Long.toString(rs.getLong("id")), rs.getString("seed_key"), rs.getString("scope"),
                null, rs.getString("title"), rs.getString("body"), rs.getString("status"),
                rs.getObject("published_version") == null ? null : rs.getInt("published_version"),
                rs.getLong("epoch"), rs.getBoolean("disabled"), null, null, null,
                rs.getObject("update_time", LocalDateTime.class).format(DISPLAY)));
    }

    public PublicKnowledgeView publicDocument(long documentId, Integer requestedVersion) {
        List<PublicKnowledgeView> rows = jdbc.query("""
                SELECT id, published_version, scope, shop_id, title, body, epoch
                FROM tb_ai_document
                WHERE id = ? AND disabled = 0 AND published_version IS NOT NULL
                  AND scope IN ('PLATFORM', 'SHOP_PUBLIC')
                """, (rs, rowNum) -> new PublicKnowledgeView(
                Long.toString(rs.getLong("id")), rs.getInt("published_version"),
                rs.getString("scope"),
                rs.getObject("shop_id") == null ? null : Long.toString(rs.getLong("shop_id")),
                rs.getString("title"), rs.getString("body"), rs.getLong("epoch")), documentId);
        if (rows.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "知识不存在或未发布");
        }
        PublicKnowledgeView view = rows.getFirst();
        if (requestedVersion != null && requestedVersion != view.version()) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "该版本不是当前发布版本");
        }
        return view;
    }

    public Document requireDocument(long id) {
        List<Document> rows = jdbc.query("""
                SELECT id, seed_key, scope, shop_id, title, body, status, published_version, epoch, disabled
                FROM tb_ai_document WHERE id = ?
                """, (rs, rowNum) -> new Document(
                rs.getLong("id"), rs.getString("seed_key"), rs.getString("scope"),
                rs.getObject("shop_id") == null ? null : rs.getLong("shop_id"),
                rs.getString("title"), rs.getString("body"), rs.getString("status"),
                rs.getObject("published_version") == null ? null : rs.getInt("published_version"),
                rs.getLong("epoch"), rs.getBoolean("disabled")), id);
        if (rows.isEmpty()) {
            throw new ResponseStatusException(HttpStatus.NOT_FOUND, "知识不存在");
        }
        return rows.getFirst();
    }

    public long ensureSeed(String seedKey, String scope, Long shopId, String title, String body) {
        List<Long> ids = jdbc.queryForList("SELECT id FROM tb_ai_document WHERE seed_key = ?",
                Long.class, seedKey);
        if (!ids.isEmpty()) return ids.getFirst();
        return insertDocument(seedKey, scope, shopId, title, body, "DRAFT", 0L);
    }

    public void publishSeedIfReady(long documentId) {
        Document document = requireDocument(documentId);
        if (!provider.ready() || document.disabled()) return;
        if (document.publishedVersion() == null) {
            publishSeed(documentId);
            return;
        }
        try {
            String model = jdbc.queryForObject("""
                    SELECT embedding_model FROM tb_ai_document_version
                    WHERE document_id = ? AND version = ?
                    """, String.class, documentId, document.publishedVersion());
            if (!provider.embeddingModel().equals(model)) {
                publishSeed(documentId);
            }
        } catch (RuntimeException e) {
            // A missing/old version will be republished on the next start; keep the old pointer.
        }
    }

    private void publishSeed(long documentId) {
        try {
            publish(documentId, 0L, "SYSTEM");
        } catch (RuntimeException e) {
            // Seed publication is best effort; the capability/status remains visible.
        }
    }

    public AiKnowledgeView view(long documentId) {
        Document document = requireDocument(documentId);
        Map<String, Object> latest = latestVersion(documentId);
        return new AiKnowledgeView(Long.toString(document.id()), document.seedKey(), document.scope(),
                document.shopId() == null ? null : Long.toString(document.shopId()),
                document.title(), document.body(), document.status(),
                document.publishedVersion(), document.epoch(), document.disabled(),
                latest == null ? null : ((Number) latest.get("version")).intValue(),
                latest == null ? null : (String) latest.get("status"),
                latest == null ? null : (String) latest.get("error"), null);
    }

    private long insertDocument(String seedKey, String scope, Long shopId, String title, String body,
                                String status, long userId) {
        String safeTitle = requireText(title, 255);
        String safeBody = requireText(body, 20000);
        KeyHolder holder = new GeneratedKeyHolder();
        jdbc.update(connection -> {
            PreparedStatement statement = connection.prepareStatement("""
                    INSERT INTO tb_ai_document
                        (seed_key, scope, shop_id, title, body, status, created_by, updated_by)
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                    """, Statement.RETURN_GENERATED_KEYS);
            statement.setString(1, seedKey);
            statement.setString(2, scope);
            if (shopId == null) statement.setNull(3, java.sql.Types.BIGINT);
            else statement.setLong(3, shopId);
            statement.setString(4, safeTitle);
            statement.setString(5, safeBody);
            statement.setString(6, status);
            statement.setLong(7, userId);
            statement.setLong(8, userId);
            return statement;
        }, holder);
        Number key = holder.getKey();
        if (key == null) throw new IllegalStateException("AI document id not generated");
        return key.longValue();
    }

    private Map<String, Object> latestVersion(long documentId) {
        List<Map<String, Object>> rows = jdbc.queryForList("""
                SELECT version, status, error FROM tb_ai_document_version
                WHERE document_id = ? ORDER BY version DESC LIMIT 1
                """, documentId);
        return rows.isEmpty() ? null : rows.getFirst();
    }

    private int nextVersion(long documentId) {
        Integer max = jdbc.queryForObject(
                "SELECT COALESCE(MAX(version), 0) FROM tb_ai_document_version WHERE document_id = ?",
                Integer.class, documentId);
        return (max == null ? 0 : max) + 1;
    }

    private String toJson(float[] vector) {
        try {
            return objectMapper.writeValueAsString(vector);
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    private long merchantOf(Long shopId) {
        if (shopId == null) return 0L;
        Long merchant = jdbc.queryForObject("SELECT merchant_id FROM tb_shop WHERE id = ?", Long.class, shopId);
        return merchant == null ? 0L : merchant;
    }

    private static String requireText(String value, int max) {
        if (value == null || value.isBlank()) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "内容不能为空");
        }
        String trimmed = value.trim();
        if (trimmed.length() > max) {
            throw new ResponseStatusException(HttpStatus.BAD_REQUEST, "内容过长");
        }
        return trimmed;
    }

    private static String hash(String body) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(body.getBytes(StandardCharsets.UTF_8));
            return HexFormat.of().formatHex(digest);
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static String truncate(String value) {
        if (value == null) return null;
        return value.length() <= 500 ? value : value.substring(0, 500);
    }

    public record Document(long id, String seedKey, String scope, Long shopId, String title, String body,
                           String status, Integer publishedVersion, long epoch, boolean disabled) { }
}
