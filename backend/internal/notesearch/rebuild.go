package notesearch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// Rebuild writes a new physical generation and atomically swaps the alias only
// after all snapshot writes and refresh succeed. It keeps old generations,
// receipts, parked failures and Kafka offsets. Operators run the command with
// API/consumers stopped: no zero-downtime guarantee is claimed.
func (sync *Sync) Rebuild(ctx context.Context) error {
	if sync.db == nil || sync.client == nil {
		return ErrNeedsRebuild
	}
	var entropy [8]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	generation := sync.client.Index() + "-build-" + hex.EncodeToString(entropy[:])
	return sync.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockSearchControl(tx); err != nil {
			return err
		}
		if err := sync.client.CreateGeneration(ctx, generation); err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM tb_note_search_state`).Error; err != nil {
			return err
		}
		// The first nonlocking SELECT establishes a repeatable-read snapshot
		// after the current-read control lock. Cursor pages share that snapshot
		// without retaining all documents in memory. Later fact changes retain
		// their outbox events and apply after this barrier releases.
		var after uint64
		for {
			var rows []struct {
				ID, ShopID, UserID uint64
				Title, Content     string
				Version            int
				CreateTime         time.Time
			}
			if err := tx.Raw(`SELECT id,shop_id,user_id,title,content,publish_version AS version,create_time FROM tb_blog WHERE status='PUBLISHED' AND id>? ORDER BY id LIMIT 250`, after).Scan(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				doc := Document{ID: strconv.FormatUint(row.ID, 10), ShopID: strconv.FormatUint(row.ShopID, 10), UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content, Version: row.Version, PublishedAt: row.CreateTime.UTC().Format(time.RFC3339Nano)}
				hash, err := searchDocumentHash(doc)
				if err != nil {
					return err
				}
				if err = sync.client.Write(ctx, generation, doc); err != nil {
					return err
				}
				if err = tx.Exec(`INSERT INTO tb_note_search_state(note_id,version,document_hash) VALUES(?,?,?)`, doc.ID, doc.Version, hash[:]).Error; err != nil {
					return err
				}
				after = row.ID
			}
			if len(rows) < 250 {
				break
			}
		}
		if err := sync.client.Refresh(ctx, generation); err != nil {
			return err
		}
		// Metadata is not committed unless activation also succeeds. A network
		// error at alias activation/SQL commit has an uncertain distributed
		// outcome; keep the old generations and rerun controlled rebuild.
		if err := tx.Exec(`UPDATE tb_note_search_control SET index_alias=?,active_generation=?,rebuilt_at=NOW(3) WHERE name=?`, sync.client.Index(), generation, Projection).Error; err != nil {
			return err
		}
		return sync.client.Activate(ctx, generation)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
}
