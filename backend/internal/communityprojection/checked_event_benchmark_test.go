package communityprojection

import "testing"

var checkedLikeBenchmarkEvent Event

func checkedLikeBenchmarkMessage() Message {
	return Message{Topic: "community.events", Partition: 0, Offset: 7,
		Key: []byte("42"), Body: likeBody("42", "43", 1, true),
		Headers: []Header{{Key: "eventId", Value: []byte("LIKE:42:43:1")},
			{Key: "eventType", Value: []byte("LIKE_CHANGED")}, {Key: "version", Value: []byte("1")}}}
}

// Parsing/canonical hashing only. It excludes storage, Kafka, HTTP, concurrency
// and business throughput; the shared input is never generated inside the timer.
func BenchmarkIgnoredLikeValidationDoubleDecode(b *testing.B) {
	message := checkedLikeBenchmarkMessage()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := checkedEvent(message); err != nil {
			b.Fatal(err)
		}
		event, err := Decode(message.Body)
		if err != nil {
			b.Fatal(err)
		}
		checkedLikeBenchmarkEvent = event
	}
}

func BenchmarkIgnoredLikeValidationReuseCheckedEvent(b *testing.B) {
	message := checkedLikeBenchmarkMessage()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		event, err := decodeCheckedEvent(message)
		if err != nil {
			b.Fatal(err)
		}
		checkedLikeBenchmarkEvent = event
	}
}
