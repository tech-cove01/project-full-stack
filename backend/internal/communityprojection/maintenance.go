package communityprojection

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

type Stats struct {
	Projection  string     `json:"projection"`
	Receipts    int64      `json:"receipts"`
	Parked      int64      `json:"parked"`
	Resolved    int64      `json:"resolved"`
	LastApplied *time.Time `json:"lastApplied,omitempty"`
}

func (runtime *Runtime) Stats(ctx context.Context, projection string) (Stats, error) {
	result := Stats{Projection: projection}
	if projection != Follow && projection != Feed {
		return result, ErrInvalid
	}
	err := runtime.db.WithContext(ctx).Raw(`SELECT
 (SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=?) AS receipts,
 (SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND state='PARKED') AS parked,
 (SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND state='RESOLVED') AS resolved,
 (SELECT MAX(applied_at) FROM tb_community_projection_event WHERE projection=?) AS last_applied`,
		projection, projection, projection, projection).Scan(&result).Error
	return result, err
}

// Rebuild replaces only one derived model. The shared/exclusive control-row
// barrier serializes every incremental application against the snapshot.
// Receipts and parked records remain durable; no Kafka offsets are reset.
func (runtime *Runtime) Rebuild(ctx context.Context, projection string) error {
	if projection != Follow && projection != Feed {
		return ErrInvalid
	}
	return runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var control string
		if err := tx.Raw(`SELECT projection FROM tb_community_projection_control WHERE projection=? FOR UPDATE`, projection).Scan(&control).Error; err != nil {
			return err
		}
		if control != projection {
			return ErrFactUnavailable
		}
		var zero [32]byte
		if projection == Follow {
			if err := tx.Exec(`DELETE FROM tb_community_follow_projection`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO tb_community_follow_projection(user_id,target_id,active,version,event_hash,last_event_id)
 SELECT user_id,follow_user_id,active,version,?,'' FROM tb_follow`, zero[:]).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Exec(`DELETE FROM tb_community_note_projection`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO tb_community_note_projection(note_id,author_id,version,event_hash,last_event_id,published_at)
 SELECT id,user_id,publish_version,?,'',create_time FROM tb_blog WHERE status='PUBLISHED'`, zero[:]).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`UPDATE tb_community_projection_control SET rebuilt_at=NOW(3) WHERE projection=?`, projection).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
