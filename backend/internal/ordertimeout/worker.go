// Package ordertimeout scans the database for expired unpaid orders. Redis
// reminders are optional projections and are not a prerequisite for closing.
package ordertimeout

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// Lifecycle owns the authoritative transaction, time/status predicate,
// inventory, audit and outbox. False means another action already won or the
// order is not due; the scanner never changes business rows itself.
type Lifecycle interface {
	CancelExpired(context.Context, string) (bool, error)
}

type cursor struct {
	PayDeadlineAt time.Time
	ID            int64
}

type scanFunc func(context.Context, cursor, int) ([]cursor, error)

type Worker struct {
	db                                              *gorm.DB
	lifecycle                                       Lifecycle
	position                                        cursor
	batchSize                                       int
	interval, scanTimeout, itemTimeout, pollTimeout time.Duration
	scan                                            scanFunc
}

func NewWorker(db *gorm.DB, lifecycle Lifecycle) *Worker {
	worker := &Worker{db: db, lifecycle: lifecycle, batchSize: 100, interval: 5 * time.Second,
		scanTimeout: 2 * time.Second, itemTimeout: 5 * time.Second, pollTimeout: 10 * time.Second}
	worker.scan = worker.scanDue
	return worker
}

// Run scans immediately, then waits five seconds after each batch. Poll is
// single-owner: one worker must not run Run and Poll concurrently.
func (worker *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		attempted, err := worker.Poll(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Never log SQL/driver details or arbitrary Lifecycle error text.
			slog.Warn("expired order scan will retry", "attempted", attempted)
		}
		timer := time.NewTimer(worker.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Poll returns how many orders were actually passed to CancelExpired, including
// false/error results. This is not the count of successful cancellations. A
// failure does not stop later rows; a cancelled budget stops without skipping
// the unattempted tail. Failed orders remain discoverable in the DB.
func (worker *Worker) Poll(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	pollCtx, cancel := context.WithTimeout(ctx, worker.pollTimeout)
	defer cancel()
	scanCtx, stopScan := context.WithTimeout(pollCtx, worker.scanTimeout)
	rows, err := worker.scan(scanCtx, worker.position, worker.batchSize)
	stopScan()
	if err != nil {
		if pollCtx.Err() != nil {
			return 0, pollCtx.Err()
		}
		return 0, errors.New("expired order scan unavailable")
	}
	if len(rows) == 0 {
		worker.position = cursor{}
		return 0, nil
	}
	attempted := 0
	failed := false
	for _, row := range rows {
		if err := pollCtx.Err(); err != nil {
			return attempted, err
		}
		// Advance only for the row we are now attempting, never the fetched
		// page tail. A timeout midway through the page preserves fair progress.
		worker.position = row
		attempted++
		itemCtx, stopItem := context.WithTimeout(pollCtx, worker.itemTimeout)
		_, err := worker.lifecycle.CancelExpired(itemCtx, strconv.FormatInt(row.ID, 10))
		stopItem()
		if err != nil {
			failed = true
		}
	}
	if pollCtx.Err() != nil {
		return attempted, pollCtx.Err()
	}
	if len(rows) < worker.batchSize {
		worker.position = cursor{}
	}
	if failed {
		return attempted, errors.New("some expired orders remain for retry")
	}
	return attempted, nil
}

func (worker *Worker) scanDue(ctx context.Context, after cursor, limit int) ([]cursor, error) {
	query := `SELECT id,pay_deadline_at FROM tb_voucher_order
		WHERE status = 1 AND pay_deadline_at <= NOW()`
	arguments := []any{}
	if after.ID != 0 || !after.PayDeadlineAt.IsZero() {
		query += ` AND (pay_deadline_at > ? OR (pay_deadline_at = ? AND id > ?))`
		arguments = append(arguments, after.PayDeadlineAt, after.PayDeadlineAt, after.ID)
	}
	query += " ORDER BY pay_deadline_at ASC,id ASC LIMIT ?"
	arguments = append(arguments, limit)
	var rows []cursor
	err := worker.db.WithContext(ctx).Raw(query, arguments...).Scan(&rows).Error
	return rows, err
}
