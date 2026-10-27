// Package outbox persists transaction events and fences each delivery lease.
package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

var (
	ErrConflict  = errors.New("outbox event conflicts with an existing event")
	ErrLeaseLost = errors.New("outbox delivery lease is no longer owned")
)

type Event struct {
	ID               uint64
	EventID          string
	AggregateType    string
	AggregateID      string
	AggregateVersion int
	EventType        string
	Payload          json.RawMessage
	// Attempts is incremented while claiming and is the monotonically increasing
	// fence for this lease. A previous worker cannot finish a reclaimed event.
	Attempts int
}

// Enqueue must receive the caller's transaction. Only a duplicate containing
// exactly the same event semantics is idempotent; other constraint errors fail.
func Enqueue(ctx context.Context, tx *gorm.DB, event Event) error {
	if tx == nil || !validEvent(event) {
		return errors.New("invalid outbox event")
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Payload) {
		return errors.New("invalid outbox JSON payload")
	}
	err := tx.WithContext(ctx).Exec(`INSERT INTO tb_outbox
		(event_id, aggregate_type, aggregate_id, aggregate_version, event_type, payload)
		VALUES (?, ?, ?, ?, ?, ?)`, event.EventID, event.AggregateType,
		event.AggregateID, event.AggregateVersion, event.EventType, string(event.Payload)).Error
	if err == nil {
		return nil
	}
	var duplicate *mysqldriver.MySQLError
	if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
		return err
	}
	var existing []Event
	if err := tx.WithContext(ctx).Raw(`SELECT id, event_id, aggregate_type, aggregate_id,
		aggregate_version, event_type, payload, attempts FROM tb_outbox
		WHERE event_id = ? OR (aggregate_type = ? AND aggregate_id = ?
		AND aggregate_version = ? AND event_type = ?) FOR UPDATE`, event.EventID,
		event.AggregateType, event.AggregateID, event.AggregateVersion, event.EventType).Scan(&existing).Error; err != nil {
		return err
	}
	if len(existing) != 1 || !sameEvent(existing[0], event) {
		return ErrConflict
	}
	return nil
}

func validEvent(event Event) bool {
	return strings.TrimSpace(event.EventID) != "" && len(event.EventID) <= 96 &&
		strings.TrimSpace(event.AggregateType) != "" && len(event.AggregateType) <= 32 &&
		strings.TrimSpace(event.AggregateID) != "" && len(event.AggregateID) <= 64 &&
		event.AggregateVersion >= 0 && event.AggregateVersion <= 2147483647 &&
		strings.TrimSpace(event.EventType) != "" && len(event.EventType) <= 40
}

func sameEvent(left, right Event) bool {
	if left.EventID != right.EventID || left.AggregateType != right.AggregateType ||
		left.AggregateID != right.AggregateID || left.AggregateVersion != right.AggregateVersion ||
		left.EventType != right.EventType {
		return false
	}
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	l, le := decode(left.Payload)
	r, re := decode(right.Payload)
	return le == nil && re == nil && reflect.DeepEqual(l, r)
}

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// ClaimActivity claims one supported activity event. Orders, community events,
// and unsupported activity events are deliberately outside this worker's scope.
func (repository *Repository) ClaimActivity(ctx context.Context) (*Event, error) {
	return repository.claim(ctx, "ACTIVITY",
		[]string{"ACTIVITY_PREPARE", "ACTIVITY_OPEN", "ACTIVITY_CLOSE"})
}

// ClaimOrder claims one order transition for the compensation worker. The two
// event families use separate claims so neither worker can take the other's rows.
func (repository *Repository) ClaimOrder(ctx context.Context) (*Event, error) {
	return repository.claim(ctx, "ORDER", []string{"ORDER_CHANGED"})
}

// claim takes a 30 s lease and increments the attempts fence in one statement, so
// a previous worker cannot finish an event that has been reclaimed.
func (repository *Repository) claim(ctx context.Context, aggregate string, eventTypes []string) (*Event, error) {
	if aggregate == "" || len(eventTypes) == 0 {
		return nil, errors.New("invalid claim scope")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(eventTypes)), ",")
	args := make([]any, 0, len(eventTypes)+1)
	args = append(args, aggregate)
	for _, eventType := range eventTypes {
		args = append(args, eventType)
	}
	var claimed *Event
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Event
		if err := tx.Raw(`SELECT id, event_id, aggregate_type, aggregate_id,
			aggregate_version, event_type, payload, attempts FROM tb_outbox
			WHERE BINARY aggregate_type = ?
			  AND BINARY event_type IN (`+placeholders+`)
			  AND ((delivery_status = 'PENDING' AND next_attempt_at <= NOW(3))
			    OR (delivery_status = 'PROCESSING' AND lease_until < NOW(3)))
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`, args...).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		event := rows[0]
		if event.Attempts >= 2147483647 {
			return errors.New("outbox lease generation exhausted")
		}
		result := tx.Exec(`UPDATE tb_outbox SET delivery_status = 'PROCESSING',
			lease_until = TIMESTAMPADD(SECOND, 30, NOW(3)), attempts = attempts + 1
			WHERE id = ? AND attempts = ?`, event.ID, event.Attempts)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseLost
		}
		event.Attempts++
		claimed = &event
		return nil
	})
	return claimed, err
}

func (repository *Repository) Done(ctx context.Context, event Event) error {
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_outbox
		SET delivery_status = 'DONE', completed_at = NOW(3), lease_until = NULL, last_error = NULL
		WHERE id = ? AND delivery_status = 'PROCESSING' AND attempts = ? AND lease_until > NOW(3)`, event.ID, event.Attempts))
}

// Retry never discards an event after an arbitrary number of transient Redis
// failures. The durable row remains PENDING with bounded exponential backoff.
func (repository *Repository) Retry(ctx context.Context, event Event, cause error) error {
	message := "delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_outbox
		SET delivery_status = 'PENDING', lease_until = NULL,
		    next_attempt_at = TIMESTAMPADD(SECOND, ?, NOW(3)), last_error = ?
		WHERE id = ? AND delivery_status = 'PROCESSING' AND attempts = ? AND lease_until > NOW(3)`,
		retryDelay(event.Attempts), message, event.ID, event.Attempts))
}

// NeedsReview parks an event whose Redis state cannot be applied safely (missing
// stock key, wrong type, counter out of range). The row stays visible for
// reconciliation instead of being retried forever or silently deleted.
func (repository *Repository) NeedsReview(ctx context.Context, event Event, cause error) error {
	message := "needs reconciliation"
	if cause != nil {
		message = cause.Error()
	}
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500])
	}
	return fenced(repository.db.WithContext(ctx).Exec(`UPDATE tb_outbox
		SET delivery_status = 'NEEDS_REVIEW', lease_until = NULL, last_error = ?
		WHERE id = ? AND delivery_status = 'PROCESSING' AND attempts = ? AND lease_until > NOW(3)`,
		message, event.ID, event.Attempts))
}

func retryDelay(attempts int) int {
	if attempts < 1 {
		return 2
	}
	if attempts >= 6 {
		return 60
	}
	return 1 << attempts
}

func fenced(result *gorm.DB) error {
	if result.Error != nil {
		return fmt.Errorf("update outbox delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}
