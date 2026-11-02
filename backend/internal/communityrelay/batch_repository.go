package communityrelay

import (
	"context"
	"database/sql"
	"errors"
	"sort"

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
		remaining := uint64(maxBytes)
		for _, event := range rows {
			if event.Attempts >= maxAttempts {
				if err := fenced(tx.Exec(`UPDATE tb_community_outbox SET delivery_status='NEEDS_REVIEW',
					lease_until=NULL,last_error='delivery lease expired at retry limit'
					WHERE id=? AND attempts=?`, event.ID, event.Attempts)); err != nil {
					return err
				}
				continue
			}
			oversizedSingleton := false
			if maxBytes > 0 {
				size := eventEncodedUpperBound(event)
				if size > remaining {
					if len(claimed) > 0 {
						break
					}
					oversizedSingleton = true
				} else {
					remaining -= size
				}
			}
			if err := fenced(tx.Exec(`UPDATE tb_community_outbox
				SET delivery_status='PROCESSING', attempts=attempts+1,
				    lease_until=DATE_ADD(NOW(3), INTERVAL ? SECOND), last_error=NULL
				WHERE id=? AND attempts=?`, leaseSeconds, event.ID, event.Attempts)); err != nil {
				return err
			}
			event.Attempts++
			claimed = append(claimed, event)
			if oversizedSingleton {
				break
			}
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
