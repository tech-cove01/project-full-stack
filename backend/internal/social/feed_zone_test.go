package social

import (
	"testing"
	"time"
)

func TestFeedZonePreservesShanghaiTimeAndCursor(t *testing.T) {
	for _, test := range []struct {
		name   string
		utc    time.Time
		local  string
		offset int
	}{
		{"modern", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "2026-11-01 08:00:00", 8 * 60 * 60},
		{"historical-winter", time.Date(1991, 1, 1, 0, 0, 0, 0, time.UTC), "1991-01-01 08:00:00", 8 * 60 * 60},
		{"historical-summer", time.Date(1991, 6, 1, 0, 0, 0, 0, time.UTC), "1991-06-01 09:00:00", 9 * 60 * 60},
	} {
		t.Run(test.name, func(t *testing.T) {
			local := test.utc.In(zone())
			_, offset := local.Zone()
			formatted := local.Format(feedTimeLayout)
			if formatted != test.local || offset != test.offset {
				t.Fatalf("Shanghai conversion = %s offset %d, want %s offset %d", formatted, offset, test.local, test.offset)
			}
			cursor, err := time.ParseInLocation(feedTimeLayout, formatted, zone())
			if err != nil || !cursor.Equal(test.utc) {
				t.Fatalf("feed cursor changed the instant: %v, error %v", cursor, err)
			}
		})
	}
}
