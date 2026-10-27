// Package communityrelay delivers community events from tb_community_outbox to
// Kafka. Delivery is at-least-once: the row is claimed under a lease, marked done
// only after the broker acknowledged it, and retried with backoff otherwise.
package communityrelay

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

var (
	// ErrLeaseLost means another relay reclaimed the same event; the late result
	// must not overwrite the new owner's state.
	ErrLeaseLost = errors.New("community outbox lease lost")
)

const (
	leaseSeconds   = 30
	maxAttempts    = 8
	maxBackoffSecs = 300
)

type Event struct {
	ID               uint64
	EventID          string
	AggregateType    string
	AggregateID      string
	AggregateVersion int
	EventType        string
	Payload          []byte
	Attempts         int
}

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// Claim takes one due event under a 30 s lease and increments the attempts fence
// in the same transaction, so a reclaimed row cannot be finished twice.
func (repository *Repository) Claim(ctx context.Context) (*Event, error) {
	var claimed *Event
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Event
		if err := tx.Raw(`SELECT id,event_id,aggregate_type,aggregate_id,aggregate_version,
			event_type,payload,attempts FROM tb_community_outbox
			WHERE (delivery_status='PENDING' AND next_attempt_at<=NOW(3))
			   OR (delivery_status='PROCESSING' AND lease_until<NOW(3))
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		event := rows[0]
		if event.Attempts >= maxAttempts {
			// Parked by a previous worker; NeedsReview already moved it, so skip.
			return nil
		}
		if err := tx.Exec(`UPDATE tb_community_outbox
			SET delivery_status='PROCESSING', attempts=attempts+1,
			    lease_until=DATE_ADD(NOW(3), INTERVAL ? SECOND), last_error=NULL
			WHERE id=?`, leaseSeconds, event.ID).Error; err != nil {
			return err
		}
		event.Attempts++
		claimed = &event
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// fenced applies an update only while this worker still owns the lease and the
// attempt count it observed. A stale worker gets ErrLeaseLost instead of a silent
// overwrite.
func fenced(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (repository *Repository) Done(ctx context.Context, event Event) error {
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_community_outbox
		SET delivery_status='DONE', completed_at=NOW(3), lease_until=NULL, last_error=NULL
		WHERE id=? AND delivery_status='PROCESSING' AND attempts=? AND lease_until>NOW(3)`,
		event.ID, event.Attempts))
}

// backoffSeconds double each attempt and is capped so a long outage does not turn
// into a one-hour retry delay.
func backoffSeconds(attempts int) int {
	if attempts < 1 {
		attempts = 1
	}
	backoff := 1 << min(attempts, 8)
	if backoff > maxBackoffSecs {
		backoff = maxBackoffSecs
	}
	return backoff
}

// Retry schedules the next attempt with bounded exponential backoff.
func (repository *Repository) Retry(ctx context.Context, event Event, cause error) error {
	backoff := backoffSeconds(event.Attempts)
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_community_outbox
		SET delivery_status='PENDING', lease_until=NULL,
		    next_attempt_at=DATE_ADD(NOW(3), INTERVAL ? SECOND), last_error=?
		WHERE id=? AND delivery_status='PROCESSING' AND attempts=? AND lease_until>NOW(3)`,
		backoff, truncate(cause), event.ID, event.Attempts))
}

// NeedsReview stops retrying but keeps the row visible for an operator.
func (repository *Repository) NeedsReview(ctx context.Context, event Event, cause error) error {
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_community_outbox
		SET delivery_status='NEEDS_REVIEW', lease_until=NULL, last_error=?
		WHERE id=? AND delivery_status='PROCESSING' AND attempts=? AND lease_until>NOW(3)`,
		truncate(cause), event.ID, event.Attempts))
}

func truncate(cause error) string {
	message := "delivery failed"
	if cause != nil && cause.Error() != "" {
		message = cause.Error()
	}
	if runes := []rune(message); len(runes) > 500 {
		// Driver text can contain payload fragments; the row stays under the column limit.
		message = strings.TrimSpace(string(runes[:500]))
	}
	return message
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
