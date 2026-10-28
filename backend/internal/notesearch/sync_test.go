package notesearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
)

func syncNoteMessage(note, author string, version int) communityprojection.Message {
	return communityprojection.Message{Topic: "go076.sync-unit", Partition: 0, Offset: int64(version), Key: []byte(note), Body: []byte(fmt.Sprintf(`{"eventId":"NOTE:%s:%d:PUBLISHED","eventType":"NOTE_PUBLISHED","aggregateType":"BLOG","aggregateId":"%s","version":%d,"payload":{"noteId":"%s","authorId":"%s","publishVersion":%d}}`, note, version, note, version, note, author, version))}
}

func TestSearchSyncValidatesSourceAndExistingEnvelopeBeforeStorage(t *testing.T) {
	valid := syncNoteMessage("17", "18", 3)
	event, err := decodeSearchMessage(Projection, valid)
	if err != nil || event.NoteID != "17" || event.Version != 3 || event.AuthorID != "18" {
		t.Fatal("existing note envelope was not accepted")
	}
	for name, mutate := range map[string]func(*communityprojection.Message){
		"wrong-key":          func(m *communityprojection.Message) { m.Key = []byte("19") },
		"negative-offset":    func(m *communityprojection.Message) { m.Offset = -1 },
		"negative-partition": func(m *communityprojection.Message) { m.Partition = -1 },
		"invalid-topic":      func(m *communityprojection.Message) { m.Topic = ".." },
		"unknown-field": func(m *communityprojection.Message) {
			m.Body = []byte(strings.Replace(string(m.Body), `"publishVersion":3`, `"publishVersion":3,"content":"not-a-fact"`, 1))
		},
		"contradictory-header": func(m *communityprojection.Message) {
			m.Headers = []communityprojection.Header{{Key: "version", Value: []byte("2")}}
		},
		"duplicate-header": func(m *communityprojection.Message) {
			m.Headers = []communityprojection.Header{{Key: "eventType", Value: []byte("NOTE_PUBLISHED")}, {Key: "eventType", Value: []byte("NOTE_PUBLISHED")}}
		},
		"too-many-headers": func(m *communityprojection.Message) { m.Headers = make([]communityprojection.Header, 33) },
		"too-long-header": func(m *communityprojection.Message) {
			m.Headers = []communityprojection.Header{{Key: "other", Value: make([]byte, 1025)}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			message := valid
			mutate(&message)
			if err := NewSync(nil, nil).Apply(context.Background(), Projection, message); !errors.Is(err, ErrSyncInvalid) {
				t.Fatal("invalid source reached storage")
			}
		})
	}
	for _, projection := range []string{communityprojection.Follow, communityprojection.Feed, "SEARCH-V1", ""} {
		if _, err := decodeSearchMessage(projection, valid); !errors.Is(err, ErrSyncInvalid) {
			t.Fatal("search must not widen the old projection runtime")
		}
	}
}

func TestSearchFailureDigestBindsExactOriginalMessageAndNormalizesNilBlobs(t *testing.T) {
	base := communityprojection.Message{Key: nil, Body: nil}
	first, _, err := searchMessageDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Key = []byte{}
	base.Body = []byte{}
	second, _, err := searchMessageDigest(base)
	if err != nil || first != second {
		t.Fatal("SQL empty blob roundtrip changed source digest")
	}
	for _, message := range []communityprojection.Message{
		{Key: []byte("x"), Body: []byte("yz")},
		{Key: []byte("xy"), Body: []byte("z")},
		{Key: []byte("x"), Body: []byte("yz"), Headers: []communityprojection.Header{{Key: "version", Value: []byte("1")}}},
	} {
		digest, _, err := searchMessageDigest(message)
		if err != nil || digest == first {
			t.Fatal("source mutation not bound by immutable digest")
		}
		first = digest
	}
}
