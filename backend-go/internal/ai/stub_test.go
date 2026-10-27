package ai

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestStubEmbeddingIsDeterministicAndNormalized(t *testing.T) {
	stub := NewStub(128)
	vectors, err := stub.Embed(context.Background(), []string{"核销后不可退款", "到店核销规则"})
	if err != nil || len(vectors) != 2 {
		t.Fatalf("embedding failed: %v", err)
	}
	for _, vector := range vectors {
		if len(vector) != 128 {
			t.Fatalf("dimension = %d, want 128", len(vector))
		}
		var norm float64
		for _, value := range vector {
			norm += float64(value) * float64(value)
		}
		if math.Abs(norm-1) > 1e-5 {
			t.Fatalf("vector must be L2-normalized, got norm %f", norm)
		}
	}
	again, err := stub.Embed(context.Background(), []string{"核销后不可退款"})
	if err != nil || len(again) != 1 {
		t.Fatal("re-embedding failed")
	}
	for i, value := range vectors[0] {
		if math.Abs(float64(value-again[0][i])) > 1e-6 {
			t.Fatal("the same text must always produce the same vector")
		}
	}
	if dot(vectors[0], vectors[1]) <= 0 {
		t.Fatal("related Chinese text should share some lexical weight")
	}
}

func TestStubRejectsEmptyInputAndTinyDimensions(t *testing.T) {
	if _, err := NewStub(4).Embed(context.Background(), []string{""}); !errors.Is(err, ErrBadInput) {
		t.Fatal("blank text must be rejected")
	}
	if _, err := NewStub(4).Embed(context.Background(), nil); !errors.Is(err, ErrBadInput) {
		t.Fatal("an empty batch must be rejected")
	}
	if NewStub(4).Dimension() != 256 {
		t.Fatal("a too-small dimension must fall back to the safe default")
	}
}

func TestStubModeAndModelAreReportedAsStub(t *testing.T) {
	stub := NewStub(64)
	if stub.Mode() != "stub" || stub.Model() != "stub-lexical" {
		t.Fatal("the stub must identify itself so results are never presented as real quality")
	}
}

func dot(left, right []float32) float64 {
	var sum float64
	for i := range left {
		sum += float64(left[i]) * float64(right[i])
	}
	return sum
}
