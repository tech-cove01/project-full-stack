package aiknowledge

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/cityloop/fullstack-go/internal/ai"
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
	if query == "" || ai.SensitiveInput(query) {
		return nil, ErrInput
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	before, err := service.Snapshot(ctx, shopID)
	if err != nil {
		return nil, err
	}
	where := `WHERE d.disabled=0 AND d.status='PUBLISHED'`
	scope, scopeArgs := publicScope(shopID)
	where += " AND " + scope
	var rows []struct {
		ChunkID            uint64
		DocumentID         uint64
		Version            int
		Epoch              int64
		Content            string
		Embedding          string
		Title              string
		Scope              string
		ShopID             *uint64
		EmbeddingModel     string
		EmbeddingDimension int
		VersionModel       string
		VersionDimension   int
		VersionStatus      string
		VersionEpoch       int64
		VersionBody        string
		Ordinal            int
	}
	if err := service.db.WithContext(ctx).Raw(`SELECT c.id AS chunk_id,d.id AS document_id,d.published_version AS version,d.epoch,
		c.content,c.embedding,v.title,d.scope,d.shop_id,c.embedding_model,c.embedding_dimension,
		v.embedding_model AS version_model,v.embedding_dimension AS version_dimension,v.status AS version_status,v.epoch AS version_epoch,v.body AS version_body,c.ordinal
		FROM tb_ai_document d LEFT JOIN tb_ai_document_version v ON v.document_id=d.id AND v.version=d.published_version
		LEFT JOIN tb_ai_chunk c ON c.document_id=d.id AND c.version=d.published_version AND c.epoch=d.epoch `+where+` LIMIT 2001`, scopeArgs...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) > maxChunks {
		return nil, ErrUnsafe
	}
	// Validate the whole selected scope before buying a query embedding. An
	// incompatible or incomplete index cannot masquerade as empty/partial hits.
	stored := make([][]float32, len(rows))
	expected := make(map[uint64][]string)
	ordinals := make(map[uint64]map[int]bool)
	for i, row := range rows {
		if row.ChunkID == 0 || row.Version < 1 || row.VersionStatus != "READY" || row.Title == "" || row.VersionEpoch != row.Epoch ||
			row.EmbeddingModel != ai.IndexModel(service.provider) || row.EmbeddingDimension != service.provider.Dimension() ||
			row.VersionModel != row.EmbeddingModel || row.VersionDimension != row.EmbeddingDimension {
			return nil, ErrUnsafe
		}
		pieces, ok := expected[row.DocumentID]
		if !ok {
			pieces = chunks(row.VersionBody)
			expected[row.DocumentID] = pieces
			ordinals[row.DocumentID] = make(map[int]bool)
		}
		if row.Ordinal < 0 || row.Ordinal >= len(pieces) || ordinals[row.DocumentID][row.Ordinal] || row.Content != pieces[row.Ordinal] {
			return nil, ErrUnsafe
		}
		ordinals[row.DocumentID][row.Ordinal] = true
		vector, err := parseVector(row.Embedding)
		if err != nil || len(vector) != service.provider.Dimension() {
			return nil, ErrUnsafe
		}
		stored[i] = vector
	}
	for doc, pieces := range expected {
		if len(ordinals[doc]) != len(pieces) {
			return nil, ErrUnsafe
		}
	}
	if len(rows) == 0 {
		return []Hit{}, nil
	}
	vectors, err := service.provider.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) != service.provider.Dimension() || !usableVector(vectors[0]) {
		return nil, ErrUnsafe
	}
	hits := make([]Hit, 0, len(rows))
	for i, row := range rows {
		score := cosine(vectors[0], stored[i])
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
	// Embedding can take seconds. Recheck after that external boundary so the
	// anonymous search route cannot return rules disabled during generation.
	after, err := service.Snapshot(ctx, shopID)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, ErrUnsafe
	}
	if err = service.Validate(ctx, hits, shopID); err != nil {
		return nil, err
	}
	return hits, nil
}

// parseVector accepts the JSON array stored in tb_ai_chunk.embedding.
func parseVector(raw string) ([]float32, error) {
	// Preserve JSON null as nil: decoding directly into float32 accepts null
	// as zero, which would silently change malformed persisted vectors.
	var components []*float32
	if err := json.Unmarshal([]byte(raw), &components); err != nil {
		return nil, err
	}
	vector := make([]float32, len(components))
	for index, component := range components {
		if component == nil {
			return nil, ErrUnsafe
		}
		vector[index] = *component
	}
	if !usableVector(vector) {
		return nil, ErrUnsafe
	}
	return vector, nil
}

func usableVector(vector []float32) bool {
	var norm float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
		norm += float64(value) * float64(value)
	}
	return norm > 0
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
