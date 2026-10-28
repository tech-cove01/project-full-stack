package aiknowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/ai"
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
	if err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, _, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Disabled || row.PublishedVersion == nil {
			return ErrConflict
		}
		return tx.Raw(`SELECT version,body,epoch FROM tb_ai_document_version
		WHERE document_id=? AND version=? AND status='READY' AND title IS NOT NULL`, id, *row.PublishedVersion).Scan(&published).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted}); err != nil {
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
		document, membership, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if document.Disabled || document.PublishedVersion == nil || *document.PublishedVersion != published.Version || document.Epoch != published.Epoch {
			// A newer version exists; indexing the old body would be misleading.
			return ErrUnsafe
		}
		if err := tx.WithContext(ctx).Exec(`DELETE FROM tb_ai_chunk
			WHERE document_id=? AND version=?`, id, published.Version).Error; err != nil {
			return err
		}
		rows := make([]chunkInsert, 0, len(pieces))
		for ordinal, piece := range pieces {
			if len(vectors[ordinal]) != dimension {
				return ErrUnsafe
			}
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
		if err := tx.WithContext(ctx).Exec(`UPDATE tb_ai_document_version
			SET embedding_model=?, embedding_dimension=?, ready_time=NOW(3), error=NULL
			WHERE document_id=? AND version=?`, model, dimension, id, published.Version).Error; err != nil {
			return err
		}
		return audit(ctx, tx, user, id, decimal(*document.ShopID), membership.MerchantID, "KNOWLEDGE_REBUILD", published.Version)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

// manageShop resolves the management role inside the caller's transaction, so a
// revocation committed meanwhile is visible.
func (service *Service) manageShop(ctx context.Context, tx *gorm.DB, userID, shopID string) (string, error) {
	user, err := positive(userID)
	if err != nil {
		return "", err
	}
	shop, err := positive(shopID)
	if err != nil {
		return "", err
	}
	var store struct{ MerchantID uint64 }
	if err = tx.WithContext(ctx).Raw("SELECT merchant_id FROM tb_shop WHERE id=? FOR SHARE", shop).Scan(&store).Error; err != nil {
		return "", err
	}
	if store.MerchantID == 0 {
		return "", ErrForbidden
	}
	var merchant struct{ Status string }
	if err = tx.WithContext(ctx).Raw("SELECT status FROM tb_merchant WHERE id=? FOR SHARE", store.MerchantID).Scan(&merchant).Error; err != nil {
		return "", err
	}
	if merchant.Status != "ACTIVE" {
		return "", ErrForbidden
	}
	var member struct{ Status, RoleCode string }
	if err = tx.WithContext(ctx).Raw("SELECT status,role_code FROM tb_merchant_member WHERE merchant_id=? AND user_id=? FOR SHARE", store.MerchantID, user).Scan(&member).Error; err != nil {
		return "", err
	}
	if member.Status != "ACTIVE" || (member.RoleCode != "OWNER" && member.RoleCode != "MANAGER") {
		return "", ErrForbidden
	}
	return decimal(store.MerchantID), nil
}
