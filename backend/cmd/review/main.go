// Command review is the controlled recovery entry for accepted orders whose
// database outcome was never confirmed. It reads tb_order_review, claims a row
// before publishing, and re-appends the original message to stream.orders; the
// consumer then decides whether the order already exists or must be created.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/orderreview"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fail("invalid configuration: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		fail("dependencies unavailable: " + err.Error())
	}
	defer func() {
		if resources.Close() != nil {
			fail("dependency cleanup failed")
		}
	}()
	repository := orderreview.NewRepository(resources.ORM)
	switch os.Args[1] {
	case "list":
		rows, err := repository.List(ctx, 50)
		if err != nil {
			fail("cannot list review rows: " + err.Error())
		}
		if len(rows) == 0 {
			fmt.Println("no review records")
			return
		}
		fmt.Printf("%-20s %-8s %-8s %-9s %-9s %s\n",
			"order_id", "voucher", "user", "attempts", "status", "reason")
		for _, row := range rows {
			fmt.Printf("%-20s %-8s %-8s %-9d %-9s %s\n",
				row.OrderID, row.VoucherID, row.UserID, row.Attempts, row.Status, row.Reason)
		}
	case "replay":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		replay(ctx, repository, resources.Redis, os.Args[2])
	default:
		usage()
		os.Exit(2)
	}
}

func replay(ctx context.Context, repository *orderreview.Repository, client *redis.Client, orderID string) {
	if _, err := strconv.ParseInt(orderID, 10, 64); err != nil {
		fail("order id must be a decimal integer")
	}
	row, found, err := repository.Load(ctx, orderID)
	if err != nil {
		fail("cannot read review row: " + err.Error())
	}
	if !found {
		fail("no review record for order " + orderID)
	}
	if row.Status != "OPEN" && row.Status != "REQUEUED" {
		fail("review row is " + row.Status + ", only OPEN or REQUEUED can be replayed")
	}
	// Claim first: the consumer can only react after XADD, so a failure it records
	// afterwards (REQUEUED -> OPEN) is never overwritten by a late claim.
	claimed, err := repository.Claim(ctx, orderID)
	if err != nil {
		fail("cannot claim review row: " + err.Error())
	}
	if !claimed {
		fail("review row changed meanwhile; re-read with list")
	}
	entry, err := client.XAdd(ctx, &redis.XAddArgs{
		Stream: "stream.orders",
		Values: map[string]any{"id": row.OrderID, "userId": row.UserID, "voucherId": row.VoucherID},
	}).Result()
	if err != nil {
		if releaseErr := repository.Release(ctx, orderID); releaseErr != nil {
			fail("publish failed and the claim could not be released: " + releaseErr.Error())
		}
		fail("publish failed, review row returned to OPEN: " + err.Error())
	}
	fmt.Printf("requeued %s as stream entry %s\n", row.OrderID, entry)
	fmt.Println("the consumer now decides: existing row -> reports it, unknown -> retries creation")
}

func usage() {
	fmt.Println("usage: review list | replay <orderId>")
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "error: "+message)
	os.Exit(1)
}
