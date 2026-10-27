package ai

import (
	"context"
	"math"
	"strings"
	"unicode"
)

// Stub is a deterministic lexical embedding. It exists so protocol, versioning
// and indexing can be tested without a paid model; it is reported as stub and
// must never be presented as real model quality.
type Stub struct{ dimension int }

func NewStub(dimension int) *Stub {
	if dimension < 16 {
		dimension = 256
	}
	return &Stub{dimension: dimension}
}

func (stub *Stub) Mode() string   { return "stub" }
func (stub *Stub) Model() string  { return "stub-lexical" }
func (stub *Stub) Dimension() int { return stub.dimension }

func (stub *Stub) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrBadInput
	}
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if strings.TrimSpace(text) == "" {
			return nil, ErrBadInput
		}
		vectors = append(vectors, stub.vector(text))
	}
	return vectors, nil
}

// vector mirrors the Java stub: Chinese characters carry unigram and bigram
// weight, ASCII words carry their own, and the result is L2-normalized so
// cosine distance is a dot product.
func (stub *Stub) vector(text string) []float32 {
	vector := make([]float32, stub.dimension)
	runes := []rune(strings.ToLower(text))
	for i, current := range runes {
		if !unicode.Is(unicode.Han, current) {
			continue
		}
		add(vector, "c:"+string(current), 1.0)
		if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
			add(vector, "b:"+string(current)+string(runes[i+1]), 1.6)
		}
	}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_'
	}) {
		if len(word) >= 2 {
			add(vector, "w:"+word, 1.2)
		}
	}
	var norm float64
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm > 0 {
		scale := float32(math.Sqrt(norm))
		for i := range vector {
			vector[i] /= scale
		}
	}
	return vector
}

func add(vector []float32, token string, weight float32) {
	hash := uint32(2166136261)
	for i := 0; i < len(token); i++ {
		hash ^= uint32(token[i])
		hash *= 16777619
	}
	vector[hash%uint32(len(vector))] += weight
}
