package communityrelay

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
)

// MaxBatchSize bounds repository work independently of future worker settings.
const MaxBatchSize = 64

// ErrInvalidBatch rejects unsafe batch input before a database is accessed.
var ErrInvalidBatch = errors.New("invalid community outbox batch")

// ClaimBatch leases at most limit currently due rows in one RC transaction.
// Exhausted rows are parked in that same transaction and omitted from the
// returned events. An empty successful result can therefore include parking
// progress, just as Claim can return nil after parking an exhausted head.
// Events are returned only after a successful commit; a commit error does not
// prove that the server rolled back, so the caller must not publish that slice.
func (repository *Repository) ClaimBatch(ctx context.Context, limit int) ([]Event, error) {
	return repository.claimBatch(ctx, limit, 0)
}

const pointOriginalPark = `UPDATE tb_community_outbox SET delivery_status='NEEDS_REVIEW',
					lease_until=NULL,last_error='delivery lease expired at retry limit'
					WHERE id=? AND attempts=?`
const pointOriginalLease = `UPDATE tb_community_outbox
				SET delivery_status='PROCESSING', attempts=attempts+1,
				    lease_until=DATE_ADD(NOW(3), INTERVAL ? SECOND), last_error=NULL
				WHERE id=? AND attempts=?`

func claimPointSQL(events []Event, park bool) (string, []any, error) {
	if len(events) < 1 || len(events) > MaxBatchSize {
		return "", nil, ErrInvalidBatch
	}
	seen := make(map[uint64]bool, len(events))
	for _, event := range events {
		if event.ID == 0 || seen[event.ID] || event.Attempts < 0 {
			return "", nil, ErrInvalidBatch
		}
		seen[event.ID] = true
	}
	if len(events) == 1 {
		if park {
			return pointOriginalPark, []any{events[0].ID, events[0].Attempts}, nil
		}
		return pointOriginalLease, []any{leaseSeconds, events[0].ID, events[0].Attempts}, nil
	}
	var text strings.Builder
	text.WriteString("UPDATE (SELECT CAST(? AS UNSIGNED) AS id,CAST(? AS SIGNED) AS attempts")
	args := make([]any, 0, len(events)*2+1)
	for i, event := range events {
		if i > 0 {
			text.WriteString(" UNION ALL SELECT CAST(? AS UNSIGNED),CAST(? AS SIGNED)")
		}
		args = append(args, event.ID, event.Attempts)
	}
	text.WriteString(") AS selected STRAIGHT_JOIN tb_community_outbox AS o FORCE INDEX (PRIMARY) ON o.id=selected.id SET ")
	if park {
		text.WriteString("o.delivery_status='NEEDS_REVIEW',o.lease_until=NULL,o.last_error='delivery lease expired at retry limit'")
	} else {
		text.WriteString("o.delivery_status='PROCESSING',o.attempts=o.attempts+1,o.lease_until=DATE_ADD(NOW(3), INTERVAL ? SECOND),o.last_error=NULL")
		args = append(args, leaseSeconds)
	}
	text.WriteString(" WHERE o.attempts=selected.attempts")
	return text.String(), args, nil
}

func applyClaimPoint(tx *gorm.DB, events []Event, park bool) error {
	if len(events) == 0 {
		return nil
	}
	query, args, err := claimPointSQL(events, park)
	if err != nil {
		return err
	}
	result := tx.Exec(query, args...)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(events)) {
		return ErrLeaseLost
	}
	return nil
}

func (repository *Repository) claimBatch(ctx context.Context, limit, maxBytes int) ([]Event, error) {
	if limit < 1 || limit > MaxBatchSize {
		return nil, ErrInvalidBatch
	}
	var claimed []Event
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Event
		if err := tx.Raw(`SELECT id,event_id,aggregate_type,aggregate_id,aggregate_version,
			event_type,payload,attempts FROM tb_community_outbox FORCE INDEX (idx_community_active_claim)
			WHERE claim_active=1 AND ((delivery_status='PENDING' AND next_attempt_at<=NOW(3))
			   OR (delivery_status='PROCESSING' AND lease_until<NOW(3)))
			ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED`, limit).Scan(&rows).Error; err != nil {
			return err
		}
		var parked, leases []Event
		remaining := uint64(maxBytes)
		for _, event := range rows {
			if event.Attempts >= maxAttempts {
				parked = append(parked, event)
				continue
			}
			oversizedSingleton := false
			if maxBytes > 0 {
				size := eventEncodedUpperBound(event)
				if size > remaining {
					if len(leases) > 0 {
						break
					}
					oversizedSingleton = true
				} else {
					remaining -= size
				}
			}
			leases = append(leases, event)
			if oversizedSingleton {
				break
			}
		}
		if err := applyClaimPoint(tx, parked, true); err != nil {
			return err
		}
		if err := applyClaimPoint(tx, leases, false); err != nil {
			return err
		}
		for _, event := range leases {
			event.Attempts++
			claimed = append(claimed, event)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func orderedBatch(events []Event) ([]Event, error) {
	if len(events) < 1 || len(events) > MaxBatchSize {
		return nil, ErrInvalidBatch
	}
	seen := make(map[uint64]struct{}, len(events))
	for _, event := range events {
		if event.ID == 0 || event.Attempts < 1 || event.Attempts > maxAttempts {
			return nil, ErrInvalidBatch
		}
		if _, duplicate := seen[event.ID]; duplicate {
			return nil, ErrInvalidBatch
		}
		seen[event.ID] = struct{}{}
	}
	ordered := append([]Event(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	return ordered, nil
}

// DoneBatch applies the original Done fence in ascending ID order inside one
// transaction. A lost lease returns ErrLeaseLost and aborts the whole batch;
// callers may subsequently retry individual Done operations with the same fences.
// A database/commit error is returned unchanged, without claiming that a server
// commit could not have happened. Broker acknowledgement remains the caller's
// responsibility, exactly as it is for Done.
func (repository *Repository) DoneBatch(ctx context.Context, events []Event) error {
	ordered, err := orderedBatch(events)
	if err != nil {
		return err
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owned := NewRepository(tx)
		for _, event := range ordered {
			if err := owned.Done(ctx, event); err != nil {
				return err
			}
		}
		return nil
	})
}
