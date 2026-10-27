// Command recount rebuilds the Redis like bitmap of one note from MySQL and
// prints the comparison. The bitmap is a projection: MySQL is the fact source, so
// a mismatch is repaired by running this command, never by writing MySQL.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/cityloop/fullstack-go/internal/communitycache"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: recount <noteId>")
		os.Exit(2)
	}
	noteID, err := strconv.ParseUint(os.Args[1], 10, 64)
	if err != nil || noteID == 0 {
		fmt.Fprintln(os.Stderr, "error: noteId must be a positive decimal integer")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fail("invalid configuration: " + err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	var userIDs []uint64
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT user_id FROM tb_blog_like
		WHERE blog_id=? AND active=1 ORDER BY user_id`, noteID).Scan(&userIDs).Error; err != nil {
		fail("cannot read like rows: " + err.Error())
	}
	bitmap := communitycache.NewBitmap(resources.Redis)
	written, err := bitmap.Rebuild(ctx, noteID, userIDs)
	if err != nil {
		fail("bitmap rebuild failed: " + err.Error())
	}
	count, present, err := bitmap.Count(ctx, noteID)
	if err != nil || !present {
		fail("bitmap count unavailable after rebuild")
	}
	fmt.Printf("note %d: rows=%d written=%d bitmap=%d\n", noteID, len(userIDs), written, count)
	if count != int64(len(userIDs)) {
		// A mismatch means the rebuild itself is broken (for example an id beyond
		// the 32-bit offset limit); it must fail loudly so the operator notices.
		fail("bitmap does not match the authoritative rows")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "error: "+message)
	os.Exit(1)
}
