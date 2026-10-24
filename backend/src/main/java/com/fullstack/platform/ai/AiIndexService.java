package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.ObjectMapper;
import lombok.extern.slf4j.Slf4j;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Service;

import jakarta.annotation.PostConstruct;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

/** Exact in-memory scan over the current published chunks, with metadata filtering before topK. */
@Slf4j
@Service
public class AiIndexService {
    private final JdbcTemplate jdbc;
    private final ObjectMapper objectMapper;
    private final AiProperties properties;
    private final AiProvider provider;
    private volatile List<IndexedChunk> chunks = List.of();

    public AiIndexService(JdbcTemplate jdbc, ObjectMapper objectMapper, AiProperties properties,
                          AiProvider provider) {
        this.jdbc = jdbc;
        this.objectMapper = objectMapper;
        this.properties = properties;
        this.provider = provider;
    }

    @PostConstruct
    public void refresh() {
        try {
            List<IndexedChunk> loaded = new ArrayList<>();
            jdbc.query("""
                    SELECT c.id, c.document_id, c.version, c.epoch, c.content, c.embedding,
                           c.embedding_model, d.title, d.scope, d.shop_id
                    FROM tb_ai_chunk c
                    JOIN tb_ai_document d ON d.id = c.document_id
                    JOIN tb_ai_document_version v ON v.document_id = c.document_id AND v.version = c.version
                    WHERE d.disabled = 0 AND d.published_version = c.version AND v.status = 'READY'
                    """, rs -> {
                float[] embedding = readVector(rs.getString("embedding"));
                if (embedding.length != properties.getEmbeddingDimensions()) {
                    log.warn("Skip AI chunk {} with dimension {}", rs.getLong("id"), embedding.length);
                    return;
                }
                if (!provider.embeddingModel().equals(rs.getString("embedding_model"))) {
                    log.warn("Skip AI chunk {} with embedding model {}", rs.getLong("id"),
                            rs.getString("embedding_model"));
                    return;
                }
                loaded.add(new IndexedChunk(
                        rs.getLong("id"), rs.getLong("document_id"), rs.getInt("version"),
                        rs.getLong("epoch"), rs.getString("content"), embedding,
                        rs.getString("title"), rs.getString("scope"),
                        rs.getObject("shop_id") == null ? null : rs.getLong("shop_id")));
            });
            this.chunks = List.copyOf(loaded);
            log.info("AI index loaded {} chunks", this.chunks.size());
        } catch (Exception e) {
            log.warn("AI index refresh failed; keeping previous snapshot", e);
        }
    }

    public List<Retrieved> search(float[] query, Long shopId, int topK, double minScore) {
        if (query == null || query.length == 0 || chunks.isEmpty()) return List.of();
        List<Retrieved> hits = new ArrayList<>();
        for (IndexedChunk chunk : chunks) {
            if (!"PLATFORM".equals(chunk.scope())
                    && !("SHOP_PUBLIC".equals(chunk.scope()) && shopId != null && shopId.equals(chunk.shopId()))) {
                continue;
            }
            double score = cosine(query, chunk.embedding());
            if (score >= minScore) {
                hits.add(new Retrieved(chunk, score));
            }
        }
        hits.sort(Comparator.comparingDouble(Retrieved::score).reversed());
        List<Retrieved> top = hits.stream().limit(Math.max(1, topK)).toList();
        List<Retrieved> valid = validateCurrent(top);
        if (valid.size() != top.size()) {
            refresh();
            return validateCurrent(top);
        }
        return valid;
    }

    private List<Retrieved> validateCurrent(List<Retrieved> candidates) {
        if (candidates.isEmpty()) return candidates;
        Set<Long> ids = new HashSet<>();
        candidates.forEach(candidate -> ids.add(candidate.chunk().documentId()));
        String placeholders = String.join(",", java.util.Collections.nCopies(ids.size(), "?"));
        List<Map<String, Object>> rows = jdbc.queryForList(
                "SELECT id, published_version, disabled FROM tb_ai_document WHERE id IN (" + placeholders + ")",
                ids.toArray());
        List<Retrieved> valid = new ArrayList<>();
        for (Retrieved candidate : candidates) {
            for (Map<String, Object> row : rows) {
                long id = ((Number) row.get("id")).longValue();
                if (id != candidate.chunk().documentId()) continue;
                Object version = row.get("published_version");
                boolean disabled = ((Number) row.get("disabled")).intValue() != 0;
                if (!disabled && version != null
                        && ((Number) version).intValue() == candidate.chunk().version()) {
                    valid.add(candidate);
                }
            }
        }
        return valid;
    }

    private float[] readVector(String json) {
        try {
            double[] values = objectMapper.readValue(json, double[].class);
            float[] vector = new float[values.length];
            for (int i = 0; i < values.length; i++) vector[i] = (float) values[i];
            return vector;
        } catch (Exception e) {
            return new float[0];
        }
    }

    private static double cosine(float[] left, float[] right) {
        if (left.length != right.length) return 0;
        double dot = 0, leftNorm = 0, rightNorm = 0;
        for (int i = 0; i < left.length; i++) {
            dot += left[i] * right[i];
            leftNorm += left[i] * left[i];
            rightNorm += right[i] * right[i];
        }
        if (leftNorm == 0 || rightNorm == 0) return 0;
        return dot / (Math.sqrt(leftNorm) * Math.sqrt(rightNorm));
    }

    public record IndexedChunk(long chunkId, long documentId, int version, long epoch, String content,
                               float[] embedding, String title, String scope, Long shopId) { }
    public record Retrieved(IndexedChunk chunk, double score) { }
}
