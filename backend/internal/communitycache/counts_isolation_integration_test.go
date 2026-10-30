package communitycache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
	"github.com/redis/go-redis/v9"
)

type countCommandBarrier struct {
	command, ownedKey string
	reached, release  chan struct{}
	used              atomic.Bool
	once              sync.Once
}

func (barrier *countCommandBarrier) releaseOnce()                                { barrier.once.Do(func() { close(barrier.release) }) }
func (barrier *countCommandBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }
func (barrier *countCommandBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (barrier *countCommandBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		args := command.Args()
		if command.Name() == barrier.command && len(args) > 1 && args[1] == barrier.ownedKey && barrier.used.CompareAndSwap(false, true) {
			close(barrier.reached)
			select {
			case <-barrier.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return next(ctx, command)
	}
}

func waitForPublicationWaiters(t *testing.T, ctx context.Context, recorder *taildiag.Recorder, count int64) {
	t.Helper()
	bound := time.NewTimer(time.Second)
	defer bound.Stop()
	for {
		for _, stage := range recorder.Snapshot().Stages {
			if stage.Name == "counts.publication_wait" && stage.Active >= count {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("controlled publication wait did not become observable before context end")
		case <-bound.C:
			t.Fatal("controlled publication wait was not reached")
		default:
			runtime.Gosched()
		}
	}
}

type boundedProfile struct {
	bytes.Buffer
	truncated bool
}

func (buffer *boundedProfile) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > 1<<20 {
		buffer.truncated = true
		return 0, io.ErrShortWrite
	}
	return buffer.Buffer.Write(value)
}

func saveCountControlEvidence(t *testing.T, command string, value any, profile *boundedProfile) {
	t.Helper()
	if directory := os.Getenv("CAP06V_EVIDENCE"); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal("cannot create controlled evidence directory")
		}
		for name, raw := range map[string][]byte{command + "-inflight.pprof.txt": profile.Bytes()} {
			path := filepath.Join(directory, name)
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				t.Fatal("controlled evidence must use a new output path")
			}
			_, writeErr := file.Write(raw)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatal("cannot save controlled profile")
			}
		}
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal("cannot encode controlled evidence")
		}
		file, err := os.OpenFile(filepath.Join(directory, command+"-control.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal("controlled evidence must use a new output path")
		}
		_, writeErr := file.Write(append(raw, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot save controlled observations")
		}
	}
}

// This test intentionally uses APIs that exist in the old implementation, so
// the exact same controlled cases run with a counts.go-only Go overlay.
func TestIntegrationCountPublicationIsolation(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("isolated MySQL and Redis required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated configuration")
	}
	for _, command := range []string{"set", "del"} {
		t.Run(command, func(t *testing.T) {
			ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			r, err := database.Open(ctx, cfg)
			if err != nil {
				t.Fatal("isolated dependencies unavailable")
			}
			t.Cleanup(func() { _ = r.Close() })
			a, b := insertNoteFixture(t, r), insertNoteFixture(t, r)
			// The fix deliberately offers stripe isolation, not collision-free
			// key isolation. Consecutive fixture IDs are distinct modulo 4096.
			if a%4096 == b%4096 {
				t.Fatal("controlled distinct-stripe fixture unavailable")
			}
			cache := New(r.Redis, r.ORM)
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = r.Redis.Del(cleanup, key(a), key(b)).Err()
				_ = r.ORM.WithContext(cleanup).Exec("DELETE FROM tb_blog WHERE id IN (?,?)", a, b).Error
			})
			if _, err = cache.GetMany(ctx, []uint64{b}); err != nil {
				t.Fatal("cannot prewarm independent B")
			}
			if command == "del" {
				if _, err = cache.GetMany(ctx, []uint64{a}); err != nil {
					t.Fatal("cannot prewarm A for invalidation")
				}
			}
			barrier := &countCommandBarrier{command: command, ownedKey: key(a), reached: make(chan struct{}), release: make(chan struct{})}
			r.Redis.AddHook(barrier)
			t.Cleanup(barrier.releaseOnce)
			recorder := taildiag.New()
			observed, finish := taildiag.Start(ctx, recorder, false)
			defer finish(false)
			leader := make(chan error, 1)
			go func() {
				if command == "set" {
					_, err := cache.GetMany(observed, []uint64{a})
					leader <- err
				} else {
					leader <- cache.Invalidate(observed, a)
				}
			}()
			select {
			case <-barrier.reached:
			case <-ctx.Done():
				t.Fatal("A never reached the controlled Redis boundary")
			}
			waitCtx, cancelWait := context.WithCancel(observed)
			defer cancelWait()
			waiter := make(chan error, 1)
			go func() { _, err := cache.GetMany(waitCtx, []uint64{a}); waiter <- err }()
			// A synthetic L1-only ID checks the documented same-stripe collision
			// boundary without creating or mutating an unrelated database row.
			collision := a + 4096
			cache.writeLocal(collision, Counts{Likes: 9})
			collisionCtx, cancelCollision := context.WithCancel(observed)
			defer cancelCollision()
			colliding := make(chan error, 1)
			go func() { _, err := cache.GetMany(collisionCtx, []uint64{collision}); colliding <- err }()
			waitForPublicationWaiters(t, ctx, recorder, 2)
			capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
			inflight := recorder.Snapshot()
			profile := &boundedProfile{}
			profileErr := pprof.Lookup("goroutine").WriteTo(profile, 1)
			if profileErr != nil && !profile.truncated {
				t.Fatal("cannot capture controlled in-flight stacks")
			}
			cancelWait()
			cancelCollision()
			for _, result := range []<-chan error{waiter, colliding} {
				select {
				case err := <-result:
					if !errors.Is(err, context.Canceled) {
						t.Fatal("waiting same-stripe request did not cancel independently")
					}
				case <-ctx.Done():
					t.Fatal("cancelled waiter remained dependent on blocked A")
				}
			}
			if _, hot := cache.readLocal(b); !hot {
				t.Fatal("independent B L1 entry expired before the controlled assertion")
			}
			bCtx, cancelB := context.WithTimeout(observed, 300*time.Millisecond)
			bStarted := time.Now()
			other, bErr := cache.GetMany(bCtx, []uint64{b})
			cancelB()
			bElapsed := time.Since(bStarted)
			bPassed := bErr == nil && len(other) == 1 && other[b] == (Counts{})
			profileSum := sha256.Sum256(profile.Bytes())
			saveCountControlEvidence(t, command, map[string]any{
				"operation": command, "barrierReached": true, "releasedAtCapture": false,
				"capturedAt": capturedAt, "snapshot": inflight,
				"sameKeyWaitCancelled": true, "sameStripeCollisionCancelled": true,
				"independentHotBReturned": bPassed, "independentBElapsedNs": bElapsed.Nanoseconds(),
				"independentBDeadlineExceeded": errors.Is(bErr, context.DeadlineExceeded),
				"profileSha256":                hex.EncodeToString(profileSum[:]), "profileTruncated": profile.truncated,
				"scope": "controlled Redis hook, not a capacity run or proof of the original incident cause; no SQL/command arguments are exported",
			}, profile)
			if err = r.ORM.WithContext(ctx).Exec("UPDATE tb_blog SET liked=7 WHERE id=?", a).Error; err != nil {
				t.Fatal("cannot update owned A fact")
			}
			var invalidated chan error
			if command == "set" {
				invalidated = make(chan error, 1)
				go func() { invalidated <- cache.Invalidate(observed, a) }()
				waitForPublicationWaiters(t, ctx, recorder, 1)
			}
			barrier.releaseOnce()
			if err = <-leader; err != nil {
				t.Fatal("controlled A did not finish after release")
			}
			if invalidated != nil {
				if err = <-invalidated; err != nil {
					t.Fatal("controlled invalidation did not finish after release")
				}
			}
			latest, err := cache.GetMany(observed, []uint64{a})
			if err != nil || latest[a].Likes != 7 {
				t.Fatalf("old load overwrote A's post-invalidation generation: %+v %v", latest, err)
			}
			if !bPassed {
				t.Errorf("blocked A %s prevented independent hot B read before release (deadline=%v)", command, errors.Is(bErr, context.DeadlineExceeded))
			}
		})
	}
}
