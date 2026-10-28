// Command note-search inspects, rebuilds or replays the public-note ES read
// model. It never changes note facts or resets Kafka offsets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cityloop/fullstack-go/internal/notesearch"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "note search operation failed; inspect configuration and durable recovery state")
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: note-search stats|rebuild or replay <topic> <partition> <offset>")
	}
	command := args[0]
	if (command != "stats" && command != "rebuild" && command != "replay") || command == "replay" && len(args) != 4 || command != "replay" && len(args) != 1 {
		return errors.New("invalid note search arguments")
	}
	var partition int
	var offset int64
	if command == "replay" {
		parsed, err := strconv.ParseInt(args[2], 10, 32)
		if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != args[2] {
			return errors.New("invalid partition")
		}
		partition = int(parsed)
		value, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != args[3] {
			return errors.New("invalid offset")
		}
		offset = value
	}
	if command == "rebuild" && os.Getenv("GO_SEARCH_REBUILD_OFFLINE") != "1" {
		return errors.New("stop API and consumers and explicitly set GO_SEARCH_REBUILD_OFFLINE=1 before rebuilding")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.Search.Ready {
		return errors.New("search is not configured")
	}
	client, err := notesearch.NewClient(notesearch.Config{URL: cfg.Search.URL, Index: cfg.Search.Index, Timeout: cfg.Search.Timeout})
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: database.MySQLDSN(cfg), SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return errors.New("search recovery database unavailable")
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	deadline := 30 * time.Second
	if command == "rebuild" {
		deadline = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	if err = pool.PingContext(ctx); err != nil {
		return errors.New("search recovery database unavailable")
	}
	sync := notesearch.NewSync(db, client)
	switch command {
	case "stats":
		result, err := sync.Stats(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "rebuild":
		err = sync.Rebuild(ctx)
	case "replay":
		err = sync.Replay(ctx, args[1], partition, offset)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"operation": command, "projection": notesearch.Projection, "result": "committed"})
}
