package com.fullstack.platform.ai;

/** Immutable identity captured before the SSE worker starts; never read from model input. */
public record AiAuthContext(long userId, Long shopId) { }
