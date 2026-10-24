package com.fullstack.platform.ai;

public record PublicKnowledgeView(String documentId, int version, String scope, String shopId,
                                  String title, String body, long epoch) {
}
