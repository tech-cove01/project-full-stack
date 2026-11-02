package communityprojection

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func cloneCheckedMessage(message Message) Message {
	message.Key = bytes.Clone(message.Key)
	message.Body = bytes.Clone(message.Body)
	message.Headers = append([]Header(nil), message.Headers...)
	for i := range message.Headers {
		message.Headers[i].Value = bytes.Clone(message.Headers[i].Value)
	}
	return message
}

func TestDecodedCheckedEventRetainsCanonicalHashAndOriginalHeaderPolicy(t *testing.T) {
	base := checkedLikeBenchmarkMessage()
	want, err := Decode(base.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range [][]Header{base.Headers, nil,
		{{Key: "extra", Value: []byte("first")}, {Key: "extra", Value: []byte("second")}}} {
		message := cloneCheckedMessage(base)
		message.Headers = headers
		before := cloneCheckedMessage(message)
		got, err := decodeCheckedEvent(message)
		if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(before, message) || got.Hash == [32]byte{} {
			t.Fatal("checked result changed canonical fields/hash, input bytes or the optional extension-header policy")
		}
		// Canonical payload and digest must not alias the original raw buffer.
		for i := range message.Body {
			message.Body[i] = ' '
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("checked canonical event retained a mutable raw-message alias")
		}
	}
}

func TestDecodedCheckedEventRejectsStrictJSONKeyAndHeaderViolations(t *testing.T) {
	base := checkedLikeBenchmarkMessage()
	mutations := map[string]func(*Message){
		"wrong-key": func(m *Message) { m.Key = []byte("43") },
		"duplicate-envelope": func(m *Message) {
			m.Body = []byte(strings.TrimSuffix(string(m.Body), "}") + `,"version":1}`)
		},
		"unknown-envelope": func(m *Message) {
			m.Body = []byte(strings.TrimSuffix(string(m.Body), "}") + `,"schemaVersion":1}`)
		},
		"duplicate-payload": func(m *Message) {
			m.Body = []byte(strings.Replace(string(m.Body), `"liked":true`, `"liked":true,"liked":false`, 1))
		},
		"unknown-payload": func(m *Message) {
			m.Body = []byte(strings.Replace(string(m.Body), `"liked":true`, `"liked":true,"userRole":"OWNER"`, 1))
		},
		"mismatched-version": func(m *Message) { m.Body = bytes.ReplaceAll(m.Body, []byte(`"version":1`), []byte(`"version":2`)) },
		"invalid-utf8":       func(m *Message) { m.Body = []byte{0xff} },
		"oversize-body":      func(m *Message) { m.Body = bytes.Repeat([]byte{' '}, (1<<20)+1) },
		"duplicate-header": func(m *Message) {
			m.Headers = append(m.Headers, Header{Key: "eventId", Value: []byte("LIKE:42:43:1")})
		},
		"wrong-header-id":      func(m *Message) { m.Headers[0].Value = []byte("LIKE:42:43:2") },
		"wrong-header-type":    func(m *Message) { m.Headers[1].Value = []byte("FOLLOW_CHANGED") },
		"wrong-header-version": func(m *Message) { m.Headers[2].Value = []byte("2") },
		"oversize-header-key":  func(m *Message) { m.Headers = []Header{{Key: strings.Repeat("x", 129)}} },
		"oversize-header-value": func(m *Message) {
			m.Headers = []Header{{Key: "extra", Value: make([]byte, 1025)}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			message := cloneCheckedMessage(base)
			mutate(&message)
			event, err := decodeCheckedEvent(message)
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(event, Event{}) || !errors.Is(checkedEvent(message), ErrInvalid) {
				t.Fatal("invalid message exposed a reusable partial event or bypassed the existing error-only interface")
			}
		})
	}
}

func TestIgnoredLikeReuseStillRejectsCallerCoordinatesAndByteBounds(t *testing.T) {
	runtime := NewRuntime(New(nil))
	base := checkedLikeBenchmarkMessage()
	for name, change := range map[string]func(*Message){
		"topic":          func(m *Message) { m.Topic = ".." },
		"partition":      func(m *Message) { m.Partition = -1 },
		"offset":         func(m *Message) { m.Offset = -1 },
		"key-bytes":      func(m *Message) { m.Key = make([]byte, 1025) },
		"header-count":   func(m *Message) { m.Headers = make([]Header, 33) },
		"body-bytes":     func(m *Message) { m.Body = bytes.Repeat([]byte{' '}, (1<<20)+1) },
		"non-like-event": func(m *Message) { m.Body = noteBody("42", "43", 1); m.Headers = nil },
	} {
		t.Run(name, func(t *testing.T) {
			message := cloneCheckedMessage(base)
			change(&message)
			if err := runtime.ApplyIgnoredLikes(context.Background(), Follow, []Message{message}); !errors.Is(err, ErrInvalid) {
				t.Fatal("returned-event reuse bypassed the original batch boundary before unavailable storage")
			}
		})
	}
}
