package aiknowledge

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Hit is one retrieved chunk of the currently published version. Version and
// epoch travel with it so a later answer can be invalidated when the document is
// replaced or disabled.
type Hit struct {
	ChunkID    uint64  `json:"-"`
	DocumentID uint64  `json:"-"`
	Version    int     `json:"-"`
	Epoch      int64   `json:"-"`
	Title      string  `json:"title"`
	Scope      string  `json:"scope"`
	ShopID     *string `json:"shopId,omitempty"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}

// Search scores chunks of published, non-disabled documents against the query.
// Only the version currently pointed at by published_version is visible, so a
// superseded or disabled rule can never be retrieved.
func (service *Service) Search(ctx context.Context, query string, shopID *uint64,
	limit int, minScore float64) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrInput
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	vectors, err := service.provider.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, ErrUnsafe
	}
	where := `WHERE d.disabled=0 AND d.status='PUBLISHED' AND d.published_version=c.version
		AND c.embedding_model=? AND c.embedding_dimension=?`
	args := []any{service.provider.Model(), service.provider.Dimension()}
	if shopID == nil {
		where += " AND d.scope<>? "
		args = append(args, scopeShop)
	} else {
		where += " AND (d.scope<>? OR d.shop_id=?)"
		args = append(args, scopeShop, *shopID)
	}
	var rows []struct {
		ChunkID    uint64
		DocumentID uint64
		Version    int
		Epoch      int64
		Content    string
		Embedding  string
		Title      string
		Scope      string
		ShopID     *uint64
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT c.id AS chunk_id,c.document_id,c.version,c.epoch,
		c.content,c.embedding,d.title,d.scope,d.shop_id
		FROM tb_ai_chunk c JOIN tb_ai_document d ON d.id=c.document_id `+where, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(rows))
	for _, row := range rows {
		vector, err := parseVector(row.Embedding)
		if err != nil || len(vector) != len(vectors[0]) {
			// A chunk whose vector no longer matches the active model is skipped
			// rather than scored with a meaningless similarity.
			continue
		}
		score := cosine(vectors[0], vector)
		if score < minScore {
			continue
		}
		hit := Hit{ChunkID: row.ChunkID, DocumentID: row.DocumentID, Version: row.Version,
			Epoch: row.Epoch, Title: row.Title, Scope: row.Scope, Content: row.Content, Score: score}
		if row.ShopID != nil {
			shop := decimal(*row.ShopID)
			hit.ShopID = &shop
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ChunkID < hits[j].ChunkID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// parseVector accepts the JSON array stored in tb_ai_chunk.embedding.
func parseVector(raw string) ([]float32, error) {
	var vector []float32
	if err := json.Unmarshal([]byte(raw), &vector); err != nil {
		return nil, err
	}
	if len(vector) == 0 {
		return nil, ErrUnsafe
	}
	return vector, nil
}

// cosine is safe for both normalized and unnormalized vectors; a zero vector
// scores 0 instead of dividing by zero.
func cosine(left, right []float32) float64 {
	if len(left) != len(right) || len(left) == 0 {
		return 0
	}
	var dot, leftNorm, rightNorm float64
	for i := range left {
		l, r := float64(left[i]), float64(right[i])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}
