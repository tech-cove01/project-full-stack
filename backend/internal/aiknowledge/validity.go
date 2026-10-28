package aiknowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

const maxChunks = 2000

// Snapshot fingerprints the currently usable knowledge for one public scope.
// It protects refusals and history as well as answers with individual hits.
func (service *Service) Snapshot(ctx context.Context, shopID *uint64) (string, error) {
	var rows []struct {
		ID               uint64
		PublishedVersion int
		Epoch            int64
		Chunks           int64
		MaxChunkID       uint64
	}
	where, args := publicScope(shopID)
	err := service.db.WithContext(ctx).Raw(`SELECT d.id,d.published_version,d.epoch,
 (SELECT COUNT(*) FROM tb_ai_chunk c WHERE c.document_id=d.id AND c.version=d.published_version) AS chunks,
 (SELECT COALESCE(MAX(c.id),0) FROM tb_ai_chunk c WHERE c.document_id=d.id AND c.version=d.published_version) AS max_chunk_id
 FROM tb_ai_document d JOIN tb_ai_document_version v ON v.document_id=d.id AND v.version=d.published_version
 WHERE d.disabled=0 AND d.status='PUBLISHED' AND v.status='READY' AND v.title IS NOT NULL AND v.epoch=d.epoch AND `+where+` ORDER BY d.id`, args...).Scan(&rows).Error
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return service.provider.Model() + ":" + strconv.Itoa(service.provider.Dimension()) + ":" + hex.EncodeToString(sum[:]), nil
}

func publicScope(shop *uint64) (string, []any) {
	if shop == nil {
		return "d.scope='PUBLIC'", nil
	}
	return "(d.scope='PUBLIC' OR (d.scope='SHOP_PUBLIC' AND d.shop_id=?))", []any{*shop}
}

// Validate checks exact immutable chunks immediately before a provider boundary
// and terminal delivery; stale/rebuilt chunk IDs cannot authorize a response.
func (service *Service) Validate(ctx context.Context, hits []Hit, shop *uint64) error {
	where, args := publicScope(shop)
	for _, hit := range hits {
		var count int64
		values := []any{hit.ChunkID, hit.DocumentID, hit.Version, hit.Epoch, service.provider.Model(), service.provider.Dimension()}
		values = append(values, args...)
		err := service.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_ai_chunk c
 JOIN tb_ai_document d ON d.id=c.document_id
 JOIN tb_ai_document_version v ON v.document_id=d.id AND v.version=c.version
 WHERE c.id=? AND d.id=? AND c.version=? AND c.epoch=? AND d.published_version=c.version AND d.epoch=c.epoch
 AND c.embedding_model=? AND c.embedding_dimension=? AND d.disabled=0 AND d.status='PUBLISHED' AND v.status='READY' AND v.title IS NOT NULL AND `+where, values...).Scan(&count).Error
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrUnsafe
		}
	}
	return nil
}
