package ai

import (
	"context"
	"time"
)

// Usage is the provider-reported token accounting, never a billing guarantee.
// Missing, malformed or unavailable usage is represented by Known=false.
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
	Known        bool  `json:"known"`
}

// Observation contains no request text, credentials, response body or endpoint.
// One observation is delivered for each HTTP attempt, including failures.
type Observation struct {
	Operation string        `json:"operation"`
	Model     string        `json:"model"`
	Outcome   string        `json:"outcome"`
	Duration  time.Duration `json:"duration"`
	Usage     Usage         `json:"usage"`
}

type observerKey struct{}

// WithObserver attaches request-local accounting. A callback shared between
// concurrent requests must synchronize its own state. The adapter never logs it.
func WithObserver(ctx context.Context, observer func(Observation)) context.Context {
	return context.WithValue(ctx, observerKey{}, observer)
}

func observe(ctx context.Context, observation Observation) {
	if callback, ok := ctx.Value(observerKey{}).(func(Observation)); ok && callback != nil {
		callback(observation)
	}
}
