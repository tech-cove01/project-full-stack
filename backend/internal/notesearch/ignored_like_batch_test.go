package notesearch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"gorm.io/gorm"
)

func ignoredLikeMessage(note, user string, version int, liked bool) communityprojection.Message {
	id := fmt.Sprintf("LIKE:%s:%s:%d", note, user, version)
	return communityprojection.Message{Topic: "go076.ignored-batch", Partition: 0, Offset: int64(version), Key: []byte(note),
		Body:    []byte(fmt.Sprintf(`{"eventId":"%s","eventType":"LIKE_CHANGED","aggregateType":"BLOG","aggregateId":"%s","version":%d,"payload":{"blogId":"%s","userId":"%s","liked":%t,"version":%d}}`, id, note, version, note, user, liked, version)),
		Headers: []communityprojection.Header{{Key: "eventId", Value: []byte(id)}, {Key: "eventType", Value: []byte("LIKE_CHANGED")}, {Key: "version", Value: []byte(fmt.Sprint(version))}}}
}

func TestSearchIgnoredLikeBatchValidatesAllMessagesBeforeStorage(t *testing.T) {
	valid := ignoredLikeMessage("17", "18", 1, true)
	for name, message := range map[string]communityprojection.Message{
		"note-event": syncNoteMessage("17", "18", 1),
		"bad-key":    {Topic: valid.Topic, Partition: 0, Offset: 1, Key: []byte("19"), Body: valid.Body},
		"bad-header": {Topic: valid.Topic, Partition: 0, Offset: 1, Key: valid.Key, Body: valid.Body,
			Headers: []communityprojection.Header{{Key: "version", Value: []byte("2")}}},
		"bad-coordinate": {Topic: valid.Topic, Partition: -1, Offset: 1, Key: valid.Key, Body: valid.Body},
	} {
		t.Run(name, func(t *testing.T) {
			if err := NewSync(nil, nil).ApplyIgnoredLikes(context.Background(), Projection, []communityprojection.Message{valid, message}); !errors.Is(err, ErrSyncInvalid) {
				t.Fatal("invalid mixed batch reached storage")
			}
		})
	}
	for _, messages := range [][]communityprojection.Message{nil, {}, make([]communityprojection.Message, 65)} {
		if err := NewSync(nil, nil).ApplyIgnoredLikes(context.Background(), Projection, messages); !errors.Is(err, ErrSyncInvalid) {
			t.Fatal("batch bounds were not enforced")
		}
	}
	if err := NewSync(nil, nil).ApplyIgnoredLikes(context.Background(), communityprojection.Feed, []communityprojection.Message{valid}); !errors.Is(err, ErrSyncInvalid) {
		t.Fatal("search ignored batch accepted another projection")
	}
}

func TestSearchIgnoredLikeBatchDuplicateHashAndDependenciesRemainStrict(t *testing.T) {
	valid := ignoredLikeMessage("17", "18", 1, true)
	conflict := ignoredLikeMessage("17", "18", 1, false)
	if err := NewSync(nil, nil).ApplyIgnoredLikes(context.Background(), Projection, []communityprojection.Message{valid, conflict}); !errors.Is(err, ErrSyncConflict) {
		t.Fatal("same event id with different canonical facts must conflict before storage")
	}
	for _, runtime := range []*Sync{NewSync(nil, nil), NewSync(nil, &Client{}), NewSync(&gorm.DB{}, nil)} {
		if err := runtime.ApplyIgnoredLikes(context.Background(), Projection, []communityprojection.Message{valid, valid}); !errors.Is(err, ErrNeedsRebuild) {
			t.Fatal("identical duplicate must remain idempotent and dependencies must remain mandatory")
		}
	}
	messages := make([]communityprojection.Message, 64)
	for i := range messages {
		messages[i] = ignoredLikeMessage(fmt.Sprint(100+i), "18", 1, true)
	}
	if err := NewSync(nil, nil).ApplyIgnoredLikes(context.Background(), Projection, messages); !errors.Is(err, ErrNeedsRebuild) {
		t.Fatal("maximum legal batch failed source validation")
	}
}
