// Package activitypub implements the durable activity publication protocol.
package activitypub

import (
	"context"
	_ "embed"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrStale  = errors.New("activity gate version is stale")
	ErrUnsafe = errors.New("activity gate requires explicit inspection")
)

//go:embed lua/prepare.lua
var prepareSource string

//go:embed lua/open.lua
var openSource string

//go:embed lua/close.lua
var closeSource string

//go:embed lua/ready.lua
var readySource string

//go:embed lua/reconcile.lua
var reconcileSource string

var prepareScript = redis.NewScript(prepareSource)
var openScript = redis.NewScript(openSource)
var closeScript = redis.NewScript(closeSource)
var readyScript = redis.NewScript(readySource)
var reconcileScript = redis.NewScript(reconcileSource)

type Gate struct{ redis *redis.Client }

func NewGate(client *redis.Client) *Gate { return &Gate{redis: client} }

func gateKeys(id string) []string {
	return []string{"seckill:gate:" + id, "seckill:stock:" + id, "seckill:begin:" + id, "seckill:end:" + id}
}

func validID(id string) bool {
	value, err := strconv.ParseUint(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatUint(value, 10) == id
}

func (gate *Gate) Prepare(ctx context.Context, id string, version, stock int, begin, end time.Time) error {
	if !validID(id) || version < 1 || version > 2147483647 || stock < 0 || stock > 2147483647 ||
		!begin.Before(end) || begin.UnixMilli() < 1 || end.UnixMilli() > 9007199254740991 {
		return ErrUnsafe
	}
	return gate.execute(ctx, prepareScript, gateKeys(id), version, stock, begin.UnixMilli(), end.UnixMilli())
}

func (gate *Gate) Open(ctx context.Context, id string, version int) error {
	if !validID(id) || version < 1 || version > 2147483647 {
		return ErrUnsafe
	}
	return gate.execute(ctx, openScript, gateKeys(id), version)
}

func (gate *Gate) Close(ctx context.Context, id string, version int) error {
	if !validID(id) || version < 1 || version > 2147483647 {
		return ErrUnsafe
	}
	return gate.execute(ctx, closeScript, gateKeys(id)[:1], version)
}

// Reconcile overwrites the stock and time bounds of an activity that was proven
// quiescent. Unlike Prepare it accepts existing string keys, but it never accepts
// a version that is not strictly greater than the current gate version.
func (gate *Gate) Reconcile(ctx context.Context, id string, version, stock int, begin, end time.Time) error {
	if !validID(id) || version < 2 || version > 2147483647 || stock < 0 || stock > 2147483647 ||
		!begin.Before(end) || begin.UnixMilli() < 1 || end.UnixMilli() > 9007199254740991 {
		return ErrUnsafe
	}
	return gate.execute(ctx, reconcileScript, gateKeys(id), version, stock, begin.UnixMilli(), end.UnixMilli())
}

func (gate *Gate) Ready(ctx context.Context, id string, version int) (bool, error) {
	if !validID(id) || version < 1 || version > 2147483647 {
		return false, ErrUnsafe
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := readyScript.Run(bounded, gate.redis, gateKeys(id), version).Int64()
	if err != nil {
		return false, err
	}
	if value < 0 {
		return false, ErrUnsafe
	}
	return value == 1, nil
}

func (gate *Gate) execute(ctx context.Context, script *redis.Script, keys []string, arguments ...any) error {
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	value, err := script.Run(bounded, gate.redis, keys, arguments...).Int64()
	if err != nil {
		return err
	}
	switch value {
	case 0, 1:
		return nil
	case -1:
		return ErrStale
	default:
		return ErrUnsafe
	}
}
