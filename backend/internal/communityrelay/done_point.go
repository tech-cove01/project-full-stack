package communityrelay

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// Completion uses exact primary-key locks, the original per-row fence and a
// fresh server-time guard before its single commit.
type donePointLease struct {
	ID            uint64
	OriginalLease sql.NullString
}

var donePointServerFormat = regexp.MustCompile(`^[1-9][0-9]{3}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}000$`)

// The finite local comparison did not establish a reliable gain below eight.
// This is a conservative routing threshold, not a universal optimum.
const donePointMinBatchSize = 8

func donePointLockSQL(events []Event) (string, []any, error) {
	ordered, err := orderedBatch(events)
	if err != nil {
		return "", nil, err
	}
	if len(ordered) < 2 {
		return "", nil, ErrInvalidBatch
	}
	keys := make([]string, len(ordered))
	args := make([]any, 0, len(ordered))
	for index, event := range ordered {
		keys[index] = "SELECT CAST(? AS UNSIGNED) AS id"
		args = append(args, event.ID)
	}
	return `SELECT o.id,DATE_FORMAT(o.lease_until,'%Y-%m-%d %H:%i:%s.%f') AS original_lease
 FROM (` + strings.Join(keys, " UNION ALL ") + `) AS selected
 STRAIGHT_JOIN tb_community_outbox AS o FORCE INDEX (PRIMARY) ON o.id=selected.id
 ORDER BY o.id FOR UPDATE`, args, nil
}
func donePointUpdateSQL(events []Event) (string, []any, error) {
	ordered, err := orderedBatch(events)
	if err != nil {
		return "", nil, err
	}
	if len(ordered) < 2 {
		return "", nil, ErrInvalidBatch
	}
	keys := make([]string, len(ordered))
	args := make([]any, 0, len(ordered)*2)
	for index, event := range ordered {
		keys[index] = "SELECT CAST(? AS UNSIGNED) AS id,CAST(? AS SIGNED) AS attempts"
		args = append(args, event.ID, event.Attempts)
	}
	return `UPDATE (` + strings.Join(keys, " UNION ALL ") + `) AS selected
 STRAIGHT_JOIN tb_community_outbox AS o FORCE INDEX (PRIMARY) ON o.id=selected.id
 SET o.delivery_status='DONE',o.completed_at=NOW(3),o.lease_until=NULL,o.last_error=NULL
 WHERE o.delivery_status='PROCESSING' AND o.attempts=selected.attempts AND o.lease_until>NOW(3)`, args, nil
}

const donePointFreshGuard = `SELECT CAST(? AS DATETIME(6))>NOW(3)`

func donePointEarliest(events []Event, rows []donePointLease) (string, error) {
	if len(rows) != len(events) {
		return "", ErrLeaseLost
	}
	wanted := make(map[uint64]bool, len(events))
	for _, event := range events {
		wanted[event.ID] = true
	}
	seen := map[uint64]bool{}
	earliest := ""
	for _, row := range rows {
		if !wanted[row.ID] || seen[row.ID] || !row.OriginalLease.Valid {
			return "", ErrLeaseLost
		}
		seen[row.ID] = true
		if !donePointServerFormat.MatchString(row.OriginalLease.String) {
			return "", errors.New("invalid original server lease encoding")
		}
		if earliest == "" || row.OriginalLease.String < earliest {
			earliest = row.OriginalLease.String
		}
	}
	return earliest, nil
}

func (repository *Repository) donePointBatch(ctx context.Context, events []Event) error {
	ordered, err := orderedBatch(events)
	if err != nil {
		return err
	}
	if len(ordered) == 1 {
		return repository.DoneBatch(ctx, ordered)
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query, args, err := donePointLockSQL(ordered)
		if err != nil {
			return err
		}
		var rows []donePointLease
		if err = tx.Raw(query, args...).Scan(&rows).Error; err != nil {
			return err
		}
		earliest, err := donePointEarliest(ordered, rows)
		if err != nil {
			return err
		}
		query, args, err = donePointUpdateSQL(ordered)
		if err != nil {
			return err
		}
		result := tx.Exec(query, args...)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(ordered)) {
			return ErrLeaseLost
		}
		var valid sql.NullBool
		if err = tx.Raw(donePointFreshGuard, earliest).Row().Scan(&valid); err != nil {
			return err
		}
		if !valid.Valid || !valid.Bool {
			return ErrLeaseLost
		}
		return nil
	})
}
