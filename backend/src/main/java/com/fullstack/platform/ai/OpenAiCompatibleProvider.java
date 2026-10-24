package com.fullstack.platform.ai;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.springframework.http.MediaType;
import org.springframework.http.client.SimpleClientHttpRequestFactory;
import org.springframework.web.client.RestClient;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** OpenAI-compatible chat and embedding adapter; key and model stay on the server. */
public class OpenAiCompatibleProvider implements AiProvider {
    private final AiProperties properties;
    private final ObjectMapper objectMapper;
    private final RestClient client;

    public OpenAiCompatibleProvider(AiProperties properties, ObjectMapper objectMapper) {
        this.properties = properties;
        this.objectMapper = objectMapper;
        SimpleClientHttpRequestFactory factory = new SimpleClientHttpRequestFactory();
        factory.setConnectTimeout(10_000);
        factory.setReadTimeout(Math.max(10, properties.getChatTimeoutSeconds()) * 1000);
        RestClient.Builder builder = RestClient.builder()
                .baseUrl(properties.getBaseUrl())
                .requestFactory(factory);
        if (properties.getApiKey() != null && !properties.getApiKey().isBlank()) {
            builder.defaultHeader("Authorization", "Bearer " + properties.getApiKey());
        }
        this.client = builder.build();
    }

    @Override public boolean ready() { return !properties.getApiKey().isBlank(); }
    @Override public String mode() { return "openai-compatible"; }
    @Override public String embeddingModel() { return properties.getEmbeddingModel(); }

    @Override public String reason() {
        return ready() ? "configured" : "AI_API_KEY is not configured";
    }

    @Override
    public float[] embed(String text) {
        if (!ready()) throw new AiProviderException(reason());
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("model", properties.getEmbeddingModel());
        body.put("input", text);
        body.put("dimensions", properties.getEmbeddingDimensions());
        try {
            String response = client.post().uri("/embeddings")
                    .contentType(MediaType.APPLICATION_JSON).body(body)
                    .retrieve().body(String.class);
            JsonNode root = objectMapper.readTree(response);
            JsonNode embedding = root.path("data").path(0).path("embedding");
            if (!embedding.isArray() || embedding.size() != properties.getEmbeddingDimensions()) {
                throw new AiProviderException("Embedding dimension mismatch");
            }
            float[] vector = new float[embedding.size()];
            for (int i = 0; i < embedding.size(); i++) vector[i] = (float) embedding.get(i).asDouble();
            return vector;
        } catch (AiProviderException e) {
            throw e;
        } catch (Exception e) {
            throw new AiProviderException("Embedding provider failed", e);
        }
    }

    @Override
    public String chat(String systemPrompt, String userPrompt) {
        if (!ready()) throw new AiProviderException(reason());
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("model", properties.getChatModel());
        body.put("temperature", 0.1);
        body.put("max_tokens", properties.getMaxOutputTokens());
        body.put("messages", List.of(
                Map.of("role", "system", "content", systemPrompt),
                Map.of("role", "user", "content", userPrompt)));
        try {
            String response = client.post().uri("/chat/completions")
                    .contentType(MediaType.APPLICATION_JSON).body(body)
                    .retrieve().body(String.class);
            JsonNode root = objectMapper.readTree(response);
            String content = root.path("choices").path(0).path("message").path("content").asText("");
            if (content.isBlank()) throw new AiProviderException("Chat provider returned an empty answer");
            return content;
        } catch (AiProviderException e) {
            throw e;
        } catch (Exception e) {
            throw new AiProviderException("Chat provider failed", e);
        }
    }
}
