package communityprojection

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func followBody(user, target string, version int, active any) []byte {
	return eventBody("FOLLOW:"+user+":"+target+":"+strconvVersion(version), "FOLLOW_CHANGED", "FOLLOW", target, version, map[string]any{"userId": user, "followUserId": target, "active": active, "version": version})
}

func noteBody(note, author string, version int) []byte {
	return eventBody("NOTE:"+note+":"+strconvVersion(version)+":PUBLISHED", "NOTE_PUBLISHED", "BLOG", note, version, map[string]any{"noteId": note, "authorId": author, "publishVersion": version})
}
func likeBody(note, user string, version int, liked bool) []byte {
	return eventBody("LIKE:"+note+":"+user+":"+strconvVersion(version), "LIKE_CHANGED", "BLOG", note, version, map[string]any{"blogId": note, "userId": user, "liked": liked, "version": version})
}

func eventBody(id, kind, aggregate, aggregateID string, version int, payload any) []byte {
	body, _ := json.Marshal(map[string]any{"eventId": id, "eventType": kind, "aggregateType": aggregate, "aggregateId": aggregateID, "version": version, "payload": payload})
	return body
}
func strconvVersion(version int) string { return strconv.Itoa(version) }

func TestDecodeMatchesCurrentProducerAndCanonicalizesEquivalentJSON(t *testing.T) {
	body := followBody("9007199254740993", "18446744073709551615", 3, "true")
	event, err := Decode(body)
	if err != nil || event.UserID != "9007199254740993" || event.TargetID != "18446744073709551615" || !event.Active || event.Version != 3 {
		t.Fatal("current follow payload or exact unsigned IDs were rejected")
	}
	equivalent, err := Decode([]byte(`{
 "payload":{"version":3,"active":true,"followUserId":"18446744073709551615","userId":"9007199254740993"},
 "version":3,"aggregateId":"18446744073709551615","aggregateType":"FOLLOW",
 "eventType":"FOLLOW_CHANGED","eventId":"FOLLOW:9007199254740993:18446744073709551615:3"}`))
	if err != nil || event.Hash != equivalent.Hash {
		t.Fatal("equivalent payload encodings must share a canonical digest")
	}
	changed, err := Decode(followBody(event.UserID, event.TargetID, 3, false))
	if err != nil || changed.Hash == event.Hash {
		t.Fatal("same ID with different state must produce a conflicting digest")
	}
	for _, body := range [][]byte{noteBody("42", "43", 1), likeBody("42", "43", 1, true)} {
		if _, err := Decode(body); err != nil {
			t.Fatal("valid known event kind was rejected")
		}
	}
}

func TestDecodeRejectsAmbiguousOrInconsistentContracts(t *testing.T) {
	base := string(followBody("1", "2", 1, true))
	for name, body := range map[string]string{
		"duplicate_envelope": strings.TrimSuffix(base, "}") + `,"version":1}`,
		"duplicate_payload":  strings.Replace(base, `"active":true`, `"active":true,"active":false`, 1),
		"unknown_envelope":   strings.TrimSuffix(base, "}") + `,"schemaVersion":1}`,
		"unknown_payload":    strings.Replace(base, `"active":true`, `"active":true,"userRole":"OWNER"`, 1),
		"trailing_value":     base + `{}`,
		"null":               `null`,
		"version_string":     strings.ReplaceAll(base, `"version":1`, `"version":"1"`),
		"version_fraction":   strings.ReplaceAll(base, `"version":1`, `"version":1.0`),
		"version_exponent":   strings.ReplaceAll(base, `"version":1`, `"version":1e0`),
		"version_overflow":   strings.ReplaceAll(base, `"version":1`, `"version":2147483648`),
		"active_number":      strings.Replace(base, `"active":true`, `"active":1`, 1),
		"active_uppercase":   strings.Replace(base, `"active":true`, `"active":"TRUE"`, 1),
		"aggregate_binding":  strings.Replace(base, `"aggregateId":"2"`, `"aggregateId":"3"`, 1),
		"event_binding":      strings.Replace(base, `FOLLOW:1:2:1`, `FOLLOW:9:2:1`, 1),
		"unknown_kind":       strings.Replace(base, `FOLLOW_CHANGED`, `FOLLOW_CREATED`, 1),
		"foreign_aggregate":  strings.Replace(base, `"aggregateType":"FOLLOW"`, `"aggregateType":"BLOG"`, 1),
		"leading_zero":       strings.Replace(base, `"userId":"1"`, `"userId":"01"`, 1),
		"numeric_id":         strings.Replace(base, `"userId":"1"`, `"userId":1`, 1),
		"invalid_utf8":       string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(body)); !errors.Is(err, ErrInvalid) {
				t.Fatal("ambiguous/inconsistent event must be rejected before persistence")
			}
		})
	}
	if _, err := Decode(followBody("1", "1", 1, true)); !errors.Is(err, ErrInvalid) {
		t.Fatal("self follow event must be rejected")
	}
	if _, err := Decode([]byte(strings.Repeat(" ", maxBody+1))); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized envelope must not allocate unbounded parse state")
	}
}
