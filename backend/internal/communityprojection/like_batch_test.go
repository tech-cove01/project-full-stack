package communityprojection

import (
	"context"
	"errors"
	"testing"
)

func TestIgnoredLikeBatchRejectsEveryInvalidSourceBeforeStorage(t *testing.T) {
	valid := Message{Topic: "private.likes", Partition: 0, Offset: 7, Key: []byte("42"), Body: likeBody("42", "43", 1, true)}
	mutations := map[string]func(*Message){
		"wrong-key":            func(m *Message) { m.Key = []byte("44") },
		"invalid-topic":        func(m *Message) { m.Topic = ".." },
		"negative-partition":   func(m *Message) { m.Partition = -1 },
		"negative-offset":      func(m *Message) { m.Offset = -1 },
		"too-many-headers":     func(m *Message) { m.Headers = make([]Header, 33) },
		"contradictory-header": func(m *Message) { m.Headers = []Header{{Key: "version", Value: []byte("2")}} },
		"duplicate-header": func(m *Message) {
			m.Headers = []Header{{Key: "eventId", Value: []byte("LIKE:42:43:1")}, {Key: "eventId", Value: []byte("LIKE:42:43:1")}}
		},
		"oversize-header": func(m *Message) { m.Headers = []Header{{Key: "extra", Value: make([]byte, 1025)}} },
		"non-like":        func(m *Message) { m.Body = noteBody("42", "43", 1) },
		"invalid-body":    func(m *Message) { m.Body = []byte(`{}`) },
	}
	runtime := NewRuntime(New(nil))
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := valid
			mutate(&bad)
			if err := runtime.ApplyIgnoredLikes(context.Background(), Follow, []Message{valid, bad}); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid batch source reached absent storage")
			}
		})
	}
	for _, projection := range []string{"search-v1", "", "FOLLOW-V1"} {
		if !errors.Is(runtime.ApplyIgnoredLikes(context.Background(), projection, []Message{valid}), ErrInvalid) {
			t.Fatal("batch widened projection boundary")
		}
	}
	for _, messages := range [][]Message{nil, make([]Message, 65)} {
		if !errors.Is(runtime.ApplyIgnoredLikes(context.Background(), Follow, messages), ErrInvalid) {
			t.Fatal("batch accepted an unbounded or empty request")
		}
	}
	conflict := valid
	conflict.Body = likeBody("42", "43", 1, false)
	if !errors.Is(runtime.ApplyIgnoredLikes(context.Background(), Follow, []Message{valid, conflict}), ErrConflict) {
		t.Fatal("same-event different-hash batch reached storage")
	}
	if !errors.Is(runtime.ApplyIgnoredLikes(context.Background(), Feed, []Message{valid, valid}), ErrFactUnavailable) {
		t.Fatal("identical duplicate should pass strict validation before unavailable DB")
	}
}
