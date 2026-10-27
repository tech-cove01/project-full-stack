package com.fullstack.platform.ai;

public record AiKnowledgeView(String id, String seedKey, String scope, String shopId, String title,
                              String body, String status, Integer publishedVersion, long epoch,
                              boolean disabled, Integer latestVersion, String latestStatus,
                              String latestError, String updateTime) {
}
