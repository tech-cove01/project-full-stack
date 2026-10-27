package ordertimeout

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type lifecycleFunc func(context.Context, string) (bool, error)

func (fn lifecycleFunc) CancelExpired(ctx context.Context, id string) (bool, error) {
	return fn(ctx, id)
}

func listScan(rows []cursor) scanFunc {
	return func(ctx context.Context, after cursor, limit int) ([]cursor, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var page []cursor
		for _, row := range rows {
			if after.PayDeadlineAt.IsZero() || row.PayDeadlineAt.After(after.PayDeadlineAt) || (row.PayDeadlineAt.Equal(after.PayDeadlineAt) && row.ID > after.ID) {
				page = append(page, row)
				if len(page) == limit {
					break
				}
			}
		}
		return page, nil
	}
}

func testRows(count int) []cursor {
	deadline := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	rows := make([]cursor, count)
	for index := range rows {
		rows[index] = cursor{PayDeadlineAt: deadline, ID: int64(index + 1)}
	}
	return rows
}

func TestPollContinuesAfterErrorsAndCountsAttemptsWithoutLeakingDetails(t *testing.T) {
	var called []string
	worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
		called = append(called, id)
		if id == "1" {
			return false, errors.New("driver password=must-not-escape")
		}
		return id == "3", nil
	}))
	worker.scan = listScan(testRows(3))
	attempted, err := worker.Poll(context.Background())
	if attempted != 3 || err == nil || strings.Contains(err.Error(), "password") || !reflect.DeepEqual(called, []string{"1", "2", "3"}) {
		t.Fatal("a failed row stopped later work, exposed a driver error, or changed attempt counting")
	}
	if worker.position != (cursor{}) {
		t.Fatal("a completed short page must wrap so the failure can be retried")
	}
}

func TestCursorMovesPastAFullFailedPageAndReturnsForRetry(t *testing.T) {
	var called []string
	worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
		called = append(called, id)
		return false, errors.New("retry")
	}))
	worker.batchSize = 2
	worker.scan = listScan(testRows(5))
	for index := 0; index < 4; index++ {
		_, _ = worker.Poll(context.Background())
	}
	if !reflect.DeepEqual(called, []string{"1", "2", "3", "4", "5", "1", "2"}) {
		t.Fatal("failed first page starved later orders or was never retried")
	}
}

func TestBudgetExhaustionPreservesTheUnattemptedPageTail(t *testing.T) {
	worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) { <-ctx.Done(); return false, ctx.Err() }))
	worker.pollTimeout = 40 * time.Millisecond
	worker.itemTimeout = time.Second
	worker.scan = listScan(testRows(3))
	attempted, err := worker.Poll(context.Background())
	if attempted != 1 || !errors.Is(err, context.DeadlineExceeded) || worker.position.ID != 1 {
		t.Fatal("exhausted budget advanced beyond the row actually attempted")
	}
	var called []string
	worker.lifecycle = lifecycleFunc(func(ctx context.Context, id string) (bool, error) { called = append(called, id); return true, nil })
	worker.pollTimeout = time.Second
	if attempted, err = worker.Poll(context.Background()); attempted != 2 || err != nil || !reflect.DeepEqual(called, []string{"2", "3"}) {
		t.Fatal("page tail was skipped after the previous budget expired")
	}
}

func TestItemTimeoutDoesNotStopTheNextOrder(t *testing.T) {
	var called []string
	worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
		called = append(called, id)
		if id == "1" {
			<-ctx.Done()
			return false, ctx.Err()
		}
		return true, nil
	}))
	worker.itemTimeout = 20 * time.Millisecond
	worker.scan = listScan(testRows(2))
	if count, err := worker.Poll(context.Background()); count != 2 || err == nil || !reflect.DeepEqual(called, []string{"1", "2"}) {
		t.Fatal("one item deadline starved the next due order")
	}
}

func TestScanErrorKeepsCursorAndCancelledPollDoesNotRead(t *testing.T) {
	worker := NewWorker(nil, nil)
	worker.position = testRows(1)[0]
	worker.scan = func(context.Context, cursor, int) ([]cursor, error) { return nil, errors.New("sensitive driver error") }
	if count, err := worker.Poll(context.Background()); count != 0 || err == nil || strings.Contains(err.Error(), "sensitive") || worker.position.ID != 1 {
		t.Fatal("scan failure changed cursor or exposed diagnostics")
	}
	worker.scan = func(context.Context, cursor, int) ([]cursor, error) {
		t.Fatal("cancelled scan touched dependencies")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if count, err := worker.Poll(ctx); count != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled Poll must return immediately")
	}
}

func TestRunScansImmediatelyAndStopsDuringWorkOrWait(t *testing.T) {
	t.Run("already_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		NewWorker(nil, nil).Run(ctx)
	})
	t.Run("during_work", func(t *testing.T) {
		entered := make(chan struct{})
		exited := make(chan struct{})
		worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
			close(entered)
			<-ctx.Done()
			close(exited)
			return false, ctx.Err()
		}))
		worker.scan = listScan(testRows(1))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() { worker.Run(ctx); close(done) }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("Run did not scan immediately")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Run did not stop after cancelling its in-flight lifecycle call")
		}
		select {
		case <-exited:
		default:
			t.Fatal("Run leaked lifecycle work")
		}
	})
	t.Run("during_wait", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		worker := NewWorker(nil, nil)
		worker.scan = func(context.Context, cursor, int) ([]cursor, error) { entered <- struct{}{}; return nil, nil }
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() { worker.Run(ctx); close(done) }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("Run did not scan immediately")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Run ignored cancellation during the interval")
		}
	})
}

func TestProductionDefaultsAndExactIDFormatting(t *testing.T) {
	worker := NewWorker(nil, lifecycleFunc(func(ctx context.Context, id string) (bool, error) {
		if id != "9223372036854775807" {
			t.Fatal("large ID lost precision")
		}
		return true, nil
	}))
	if worker.batchSize != 100 || worker.interval != 5*time.Second {
		t.Fatal("production batch and interval changed")
	}
	worker.scan = listScan([]cursor{{ID: 9223372036854775807, PayDeadlineAt: time.Now()}})
	if count, err := worker.Poll(context.Background()); count != 1 || err != nil {
		t.Fatal("large order ID could not be passed exactly")
	}
	if strconv.FormatInt(worker.position.ID, 10) != "0" {
		t.Fatal("short page did not wrap")
	}
}
