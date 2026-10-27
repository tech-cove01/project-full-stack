// Package ai owns the embedding boundary. Business code depends on Provider, so
// knowledge indexing and retrieval can run against a deterministic stub locally
// and against a real model in evaluation without changing the storage contract.
package ai

import (
	"context"
	"errors"
)

var (
	ErrNotConfigured = errors.New("AI provider is not configured")
	ErrBadInput      = errors.New("AI input is empty or too long")
)

type Provider interface {
	Mode() string
	Model() string
	Dimension() int
	// Embed returns one vector per input text, in the same order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
