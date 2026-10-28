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
	bitmap := communitycache.NewLikes(resources.Redis)
	written, err := bitmap.RebuildFromDatabase(ctx, resources.ORM, noteID)
	if err != nil {
		fail("bitmap rebuild failed: " + err.Error())
	}
	// The returned count was computed during the same locked snapshot. A second
	// read after releasing the lock could legitimately include a new like.
	fmt.Printf("note %d: rebuilt active likes=%d\n", noteID, written)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "error: "+message)
	os.Exit(1)
}
