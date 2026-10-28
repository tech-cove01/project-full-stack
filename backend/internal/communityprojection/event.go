// Package communityprojection applies Kafka community events to independent,
// rebuildable read models. MySQL community tables remain the business facts.
package communityprojection

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	Follow  = "follow-v1"
	Feed    = "feed-v1"
	maxBody = 1 << 20
)

var (
	ErrInvalid         = errors.New("invalid community projection event")
	ErrConflict        = errors.New("community projection event conflicts with persistent state")
	ErrFactUnavailable = errors.New("community projection fact is not available")
)

// Event is fully validated and normalized. Hash includes all six envelope
// fields and the normalized typed payload, independent of JSON key ordering or
// whitespace. Kafka callers must additionally compare Key with AggregateID.
type Event struct {
	EventID       string          `json:"eventId"`
	EventType     string          `json:"eventType"`
	AggregateType string          `json:"aggregateType"`
	AggregateID   string          `json:"aggregateId"`
	Version       int             `json:"version"`
	Payload       json.RawMessage `json:"payload"`
	Hash          [32]byte        `json:"-"`
	UserID        string          `json:"-"`
	TargetID      string          `json:"-"`
	NoteID        string          `json:"-"`
	AuthorID      string          `json:"-"`
	Active        bool            `json:"-"`
}

// Decode supports the existing relay's six-field envelope without inventing a
// schemaVersion or publish timestamp that the producer does not currently send.
// Known but irrelevant event kinds are still checked before they can be ignored.
func Decode(body []byte) (Event, error) {
	var event Event
	if len(body) == 0 || len(body) > maxBody || !utf8.Valid(body) {
		return event, ErrInvalid
	}
	fields, err := object(body, "eventId", "eventType", "aggregateType", "aggregateId", "version", "payload")
	if err != nil || len(fields) != 6 {
		return event, ErrInvalid
	}
	if event.EventID, err = stringValue(fields["eventId"]); err != nil || len(event.EventID) > 96 {
		return Event{}, ErrInvalid
	}
	if event.EventType, err = stringValue(fields["eventType"]); err != nil {
		return Event{}, ErrInvalid
	}
	if event.AggregateType, err = stringValue(fields["aggregateType"]); err != nil {
		return Event{}, ErrInvalid
	}
	if event.AggregateID, err = idValue(fields["aggregateId"]); err != nil {
		return Event{}, ErrInvalid
	}
	if event.Version, err = versionValue(fields["version"]); err != nil {
		return Event{}, ErrInvalid
	}
	var canonical any
	switch event.EventType {
	case "FOLLOW_CHANGED":
		payload, err := object(fields["payload"], "userId", "followUserId", "active", "version")
		if err != nil || len(payload) != 4 || event.AggregateType != "FOLLOW" {
			return Event{}, ErrInvalid
		}
		if event.UserID, err = idValue(payload["userId"]); err != nil {
			return Event{}, ErrInvalid
		}
		if event.TargetID, err = idValue(payload["followUserId"]); err != nil || event.TargetID != event.AggregateID || event.UserID == event.TargetID {
			return Event{}, ErrInvalid
		}
		if version, err := versionValue(payload["version"]); err != nil || version != event.Version {
			return Event{}, ErrInvalid
		}
		if event.Active, err = boolValue(payload["active"], true); err != nil {
			return Event{}, ErrInvalid
		}
		if event.EventID != "FOLLOW:"+event.UserID+":"+event.TargetID+":"+strconv.Itoa(event.Version) {
			return Event{}, ErrInvalid
		}
		canonical = struct {
			UserID   string `json:"userId"`
			TargetID string `json:"followUserId"`
			Active   bool   `json:"active"`
			Version  int    `json:"version"`
		}{event.UserID, event.TargetID, event.Active, event.Version}
	case "NOTE_PUBLISHED":
		payload, err := object(fields["payload"], "noteId", "authorId", "publishVersion")
		if err != nil || len(payload) != 3 || event.AggregateType != "BLOG" {
			return Event{}, ErrInvalid
		}
		if event.NoteID, err = idValue(payload["noteId"]); err != nil || event.NoteID != event.AggregateID {
			return Event{}, ErrInvalid
		}
		if event.AuthorID, err = idValue(payload["authorId"]); err != nil {
			return Event{}, ErrInvalid
		}
		if version, err := versionValue(payload["publishVersion"]); err != nil || version != event.Version {
			return Event{}, ErrInvalid
		}
		if event.EventID != "NOTE:"+event.NoteID+":"+strconv.Itoa(event.Version)+":PUBLISHED" {
			return Event{}, ErrInvalid
		}
		canonical = struct {
			NoteID   string `json:"noteId"`
			AuthorID string `json:"authorId"`
			Version  int    `json:"publishVersion"`
		}{event.NoteID, event.AuthorID, event.Version}
	case "LIKE_CHANGED":
		payload, err := object(fields["payload"], "blogId", "userId", "liked", "version")
		if err != nil || len(payload) != 4 || event.AggregateType != "BLOG" {
			return Event{}, ErrInvalid
		}
		if event.NoteID, err = idValue(payload["blogId"]); err != nil || event.NoteID != event.AggregateID {
			return Event{}, ErrInvalid
		}
		if event.UserID, err = idValue(payload["userId"]); err != nil {
			return Event{}, ErrInvalid
		}
		if version, err := versionValue(payload["version"]); err != nil || version != event.Version {
			return Event{}, ErrInvalid
		}
		if event.Active, err = boolValue(payload["liked"], false); err != nil {
			return Event{}, ErrInvalid
		}
		if event.EventID != "LIKE:"+event.NoteID+":"+event.UserID+":"+strconv.Itoa(event.Version) {
			return Event{}, ErrInvalid
		}
		canonical = struct {
			NoteID  string `json:"blogId"`
			UserID  string `json:"userId"`
			Liked   bool   `json:"liked"`
			Version int    `json:"version"`
		}{event.NoteID, event.UserID, event.Active, event.Version}
	default:
		return Event{}, ErrInvalid
	}
	event.Payload, err = json.Marshal(canonical)
	if err != nil {
		return Event{}, ErrInvalid
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return Event{}, ErrInvalid
	}
	event.Hash = sha256.Sum256(encoded)
	return event, nil
}

func object(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalid
	}
	permitted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		permitted[key] = true
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		key, valid := token.(string)
		if !valid || !permitted[key] {
			return nil, ErrInvalid
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, ErrInvalid
		}
		fields[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	return fields, nil
}

func stringValue(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &value) != nil || value == "" {
		return "", ErrInvalid
	}
	return value, nil
}
func idValue(raw json.RawMessage) (string, error) {
	value, err := stringValue(raw)
	if err != nil {
		return "", err
	}
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
		return "", ErrInvalid
	}
	return value, nil
}
func versionValue(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, ErrInvalid
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, ErrInvalid
		}
	}
	parsed, err := strconv.ParseInt(string(raw), 10, 32)
	if err != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != string(raw) {
		return 0, ErrInvalid
	}
	return int(parsed), nil
}
func boolValue(raw json.RawMessage, stringAllowed bool) (bool, error) {
	if bytes.Equal(raw, []byte("true")) {
		return true, nil
	}
	if bytes.Equal(raw, []byte("false")) {
		return false, nil
	}
	if stringAllowed {
		if bytes.Equal(raw, []byte(`"true"`)) {
			return true, nil
		}
		if bytes.Equal(raw, []byte(`"false"`)) {
			return false, nil
		}
	}
	return false, ErrInvalid
}
