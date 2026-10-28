// Command community-projector inspects, replays one parked source record or
// rebuilds one derived model. It never edits business facts or Kafka offsets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "community projection operation failed; inspect configuration and durable failure state")
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 || (args[1] != communityprojection.Follow && args[1] != communityprojection.Feed) {
		return errors.New("usage: community-projector stats|rebuild <follow-v1|feed-v1> or replay <projection> <topic> <partition> <offset>")
	}
	command, projection := args[0], args[1]
	if (command != "stats" && command != "rebuild" && command != "replay") || (command == "replay" && len(args) != 5) || (command != "replay" && len(args) != 2) {
		return errors.New("invalid arguments")
	}
	var partition int
	var offset int64
	if command == "replay" {
		p, e := strconv.ParseInt(args[3], 10, 32)
		if e != nil || p < 0 || strconv.FormatInt(p, 10) != args[3] {
			return errors.New("invalid partition")
		}
		partition = int(p)
		o, e := strconv.ParseInt(args[4], 10, 64)
		if e != nil || o < 0 || strconv.FormatInt(o, 10) != args[4] {
			return errors.New("invalid offset")
		}
		offset = o
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := gorm.Open(mysql.Open(database.MySQLDSN(cfg)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return err
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	runtime := communityprojection.NewRuntime(communityprojection.New(db))
	switch command {
	case "stats":
		result, err := runtime.Stats(ctx, projection)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "rebuild":
		err = runtime.Rebuild(ctx, projection)
	case "replay":
		err = runtime.Replay(ctx, projection, args[2], partition, offset)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"operation": command, "projection": projection, "result": "committed"})
}
