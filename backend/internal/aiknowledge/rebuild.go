package aiknowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/merchant"
)

// Rebuild re-indexes the currently published version of a document. The body and
// the version stay untouched, so the public citation keeps working while the
// chunks are replaced. If a new version was published meanwhile, the rebuild is
// refused instead of indexing text that nobody reads any more.
func (service *Service) Rebuild(ctx context.Context, userID, documentID string) (View, error) {
	user, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	id, err := positive(documentID)
	if err != nil {
		return View{}, err
	}
	var published struct {
		Version int
		Body    string
		Epoch   int64
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT version,body,epoch FROM tb_ai_document_version
		WHERE document_id=? AND status='READY' ORDER BY version DESC LIMIT 1`, id).
		Scan(&published).Error; err != nil {
		return View{}, err
	}
	if published.Version == 0 {
		return View{}, ErrNotFound
	}
	pieces := chunks(published.Body)
	if len(pieces) == 0 {
		return View{}, ErrInput
	}
	vectors, err := service.provider.Embed(ctx, pieces)
	if err != nil {
		if errors.Is(err, ai.ErrBadInput) {
			return View{}, ErrInput
		}
		return View{}, err
	}
	if len(vectors) != len(pieces) {
		return View{}, ErrUnsafe
	}
	model, dimension := service.provider.Model(), service.provider.Dimension()
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var document struct {
			ID               uint64
			ShopID           *uint64
			Scope            string
			PublishedVersion *int
		}
		if err := tx.WithContext(ctx).Raw(`SELECT id,shop_id,scope,published_version
			FROM tb_ai_document WHERE id=? FOR UPDATE`, id).Scan(&document).Error; err != nil {
			return err
		}
		if document.ID == 0 {
			return ErrNotFound
		}
		if document.Scope != scopeShop || document.ShopID == nil {
			return ErrForbidden
		}
		if _, err := service.manageShop(ctx, tx, decimal(user), decimal(*document.ShopID)); err != nil {
			return err
		}
		if document.PublishedVersion == nil || *document.PublishedVersion != published.Version {
			// A newer version exists; indexing the old body would be misleading.
			return ErrUnsafe
		}
		if err := tx.WithContext(ctx).Exec(`DELETE FROM tb_ai_chunk
			WHERE document_id=? AND version=?`, id, published.Version).Error; err != nil {
			return err
		}
		rows := make([]chunkInsert, 0, len(pieces))
		for ordinal, piece := range pieces {
			encoded, err := json.Marshal(vectors[ordinal])
			if err != nil {
				return err
			}
			rows = append(rows, chunkInsert{DocumentID: id, Version: published.Version,
				Epoch: published.Epoch, Ordinal: ordinal, Content: piece,
				Embedding: string(encoded), EmbeddingModel: model, EmbeddingDimension: dimension})
		}
		if err := tx.WithContext(ctx).CreateInBatches(rows, 50).Error; err != nil {
			return err
		}
		return tx.WithContext(ctx).Exec(`UPDATE tb_ai_document_version
			SET embedding_model=?, embedding_dimension=?, ready_time=NOW(3), error=NULL
			WHERE document_id=? AND version=?`, model, dimension, id, published.Version).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

// manageShop resolves the management role inside the caller's transaction, so a
// revocation committed meanwhile is visible.
func (service *Service) manageShop(ctx context.Context, tx *gorm.DB, userID, shopID string) (string, error) {
	membership, err := merchant.NewAccess(tx).RequireManager(ctx, userID, shopID)
	if err != nil {
		if errors.Is(err, merchant.ErrForbidden) {
			return "", ErrForbidden
		}
		return "", err
	}
	return membership.MerchantID, nil
}
