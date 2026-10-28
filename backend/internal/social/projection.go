package social

import (
	"context"
	"database/sql"
	"strings"

	"gorm.io/gorm"
)

const (
	sourceDatabase   = "database"
	sourceProjection = "projection"
)

// WithProjections enables the optional Kafka read models. Facts remain the
// authorization boundary, and an incomplete or unavailable projection falls
// back to the existing database query.
func (service *Service) WithProjections() *Service {
	service.projections = true
	return service
}

// readSnapshot makes the completeness check and its dependent read observe the
// same committed facts. It takes no row locks: a later request sees a follow or
// publication change committed after this snapshot was established.
func (service *Service) readSnapshot(ctx context.Context, query func(*Service) error) error {
	return service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reader := *service
		reader.db = tx
		return query(&reader)
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// projectedFollowerCount is usable only when every fact for this author has the
// same pair, target state and version in the read model, with no extra active
// projected follower. Missing inactive tombstones also force the safe fallback.
func (service *Service) projectedFollowerCount(ctx context.Context, author uint64) (int64, bool) {
	var incomplete int
	err := service.db.WithContext(ctx).Raw(`SELECT
		EXISTS(SELECT 1 FROM tb_follow f
			LEFT JOIN tb_community_follow_projection p
			ON p.user_id=f.user_id AND p.target_id=f.follow_user_id
			WHERE f.follow_user_id=? AND
			(p.user_id IS NULL OR p.active<>f.active OR p.version<>f.version))
		OR EXISTS(SELECT 1 FROM tb_community_follow_projection p
			LEFT JOIN tb_follow f ON f.user_id=p.user_id AND f.follow_user_id=p.target_id
			WHERE p.target_id=? AND p.active=1 AND f.id IS NULL)`, author, author).Scan(&incomplete).Error
	if err != nil || incomplete != 0 {
		return 0, false
	}
	var count int64
	if err = service.db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_community_follow_projection
		WHERE target_id=? AND active=1`, author).Scan(&count).Error; err != nil {
		return 0, false
	}
	return count, true
}

// feedCatalogComplete checks the whole currently visible author set, not just
// an already-limited page. A single historical or delayed note must not vanish
// merely because other notes have reached Kafka. Extra/stale catalog rows are
// harmless candidates: the final query still joins current published facts.
func (service *Service) feedCatalogComplete(ctx context.Context, authors []uint64) bool {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(authors)), ",")
	args := make([]any, 0, len(authors))
	for _, author := range authors {
		args = append(args, author)
	}
	var incomplete int
	err := service.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tb_blog b
		LEFT JOIN tb_community_note_projection p ON p.note_id=b.id
		WHERE b.status='PUBLISHED' AND b.user_id IN (`+placeholders+`) AND
		(p.note_id IS NULL OR p.author_id<>b.user_id OR p.version<>b.publish_version
		OR NOT(p.published_at<=>b.create_time)))`, args...).Scan(&incomplete).Error
	return err == nil && incomplete == 0
}
