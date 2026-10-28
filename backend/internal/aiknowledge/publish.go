package aiknowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/ai"
)

type versionInsert struct {
	DocumentID         uint64
	Version            int
	Epoch              int64
	Title              string
	Body               string
	ContentHash        string
	Status             string
	EmbeddingModel     string
	EmbeddingDimension int
}

func (versionInsert) TableName() string { return "tb_ai_document_version" }

type chunkInsert struct {
	DocumentID         uint64
	Version            int
	Epoch              int64
	Ordinal            int
	Content            string
	Embedding          string
	EmbeddingModel     string
	EmbeddingDimension int
}

func (chunkInsert) TableName() string { return "tb_ai_chunk" }

// Publish freezes the current body into a new immutable version with its chunks.
// Embedding runs outside the transaction; the second transaction re-reads the row
// and refuses to write if the body or the expected version changed meanwhile.
func (service *Service) Publish(ctx context.Context, userID, documentID string) (View, error) {
	id, err := positive(documentID)
	if err != nil {
		return View{}, err
	}
	user, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	var body string
	var title string
	var version int
	var epoch int64
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, _, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Disabled {
			return ErrConflict
		}
		body = row.Body
		title = row.Title
		version = 1
		if row.PublishedVersion != nil {
			version = *row.PublishedVersion + 1
		}
		epoch = row.Epoch + 1
		return nil
	}, nil)
	if err != nil {
		return View{}, err
	}

	pieces := chunks(body)
	if len(pieces) == 0 {
		return View{}, ErrInput
	}
	var activeChunks int64
	if err = service.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_ai_chunk c JOIN tb_ai_document d ON d.id=c.document_id
 WHERE d.disabled=0 AND d.status='PUBLISHED' AND d.published_version=c.version AND d.id<>?`, id).Scan(&activeChunks).Error; err != nil {
		return View{}, err
	}
	if activeChunks+int64(len(pieces)) > maxChunks {
		return View{}, ErrUnsafe
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
	model, dimension := ai.IndexModel(service.provider), service.provider.Dimension()
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, membership, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Disabled {
			return ErrConflict
		}
		current := 1
		if row.PublishedVersion != nil {
			current = *row.PublishedVersion + 1
		}
		// The body or another publication changed while the vectors were computed.
		if row.Body != body || row.Title != title || current != version || row.Epoch+1 != epoch {
			return ErrUnsafe
		}
		if err = tx.Create(&versionInsert{DocumentID: id, Version: version, Epoch: epoch, Title: title, Body: body,
			ContentHash: contentHash(body), Status: "READY", EmbeddingModel: model,
			EmbeddingDimension: dimension}).Error; err != nil {
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
			rows = append(rows, chunkInsert{DocumentID: id, Version: version, Epoch: epoch,
				Ordinal: ordinal, Content: piece, Embedding: string(encoded),
				EmbeddingModel: model, EmbeddingDimension: dimension})
		}
		if err = tx.CreateInBatches(rows, 50).Error; err != nil {
			return err
		}
		result := tx.Exec(`UPDATE tb_ai_document
			SET published_version=?,status='PUBLISHED',epoch=?,updated_by=?
			WHERE id=? AND disabled=0`, version, epoch, user, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnsafe
		}
		return audit(ctx, tx, user, id, decimal(*row.ShopID), membership.MerchantID, "KNOWLEDGE_PUBLISH", version)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}
