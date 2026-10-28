package aiknowledge

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestParseVectorRejectsMalformedEmbeddings(t *testing.T) {
	vector, err := parseVector("[0.5,0.25,-1]")
	if err != nil || len(vector) != 3 || math.Abs(float64(vector[1])-0.25) > 1e-6 {
		t.Fatalf("valid vector rejected: %v %v", vector, err)
	}
	vector, err = parseVector("[1,0,-3]")
	if err != nil || len(vector) != 3 || vector[1] != 0 {
		t.Fatal("a numeric zero component is valid in a nonzero vector")
	}
	for _, raw := range []string{"", "[]", "not-json", `{"0":0.1}`, "null", "[0,0]", "[1e100]", "[1,null]"} {
		if _, err = parseVector(raw); err == nil {
			t.Fatalf("embedding %q must be rejected", raw)
		}
	}
}

func TestCosineHandlesDegenerateVectors(t *testing.T) {
	if score := cosine([]float32{1, 0}, []float32{1, 0}); math.Abs(score-1) > 1e-6 {
		t.Fatalf("identical vectors must score 1, got %f", score)
	}
	if score := cosine([]float32{1, 0}, []float32{-1, 0}); math.Abs(score+1) > 1e-6 {
		t.Fatalf("opposite vectors must score -1, got %f", score)
	}
	if cosine([]float32{0, 0}, []float32{1, 0}) != 0 {
		t.Fatal("a zero vector must score 0 instead of dividing by zero")
	}
	if cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Fatal("vectors of different lengths must not be compared")
	}
}

func TestSearchRejectsBlankQueryBeforeTouchingStorage(t *testing.T) {
	service := NewService(nil, nil)
	if _, err := service.Search(context.Background(), "   ", nil, 5, 0.1); !errors.Is(err, ErrInput) {
		t.Fatal("a blank query must be rejected before embedding or SQL")
	}
}
