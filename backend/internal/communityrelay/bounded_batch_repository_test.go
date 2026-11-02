package communityrelay

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
)

func boundedEvent(id uint64, text string) Event {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return Event{ID: id, EventID: "event:" + strconv.FormatUint(id, 10), AggregateType: "NOTE",
		AggregateID: "42", AggregateVersion: 1, EventType: "NOTE_PUBLISHED", Payload: payload}
}

// Independent oracle for the pinned kafka-go v0.4.47 Message.totalSize used by
// Writer.BatchBytes: v1 fixed fields/byte lengths plus variable header lengths.
// The production bound does not use this formula or depend on private SDK APIs.
func pinnedWriterSize(message kafka.Message) uint64 {
	var scratch [binary.MaxVarintLen64]byte
	variable := func(n int) int { return binary.PutVarint(scratch[:], int64(n)) }
	total := 22 + len(message.Key) + len(message.Value) + variable(len(message.Headers))
	for _, header := range message.Headers {
		total += variable(len(header.Key)) + len(header.Key) + variable(len(header.Value)) + len(header.Value)
	}
	return uint64(total)
}

func assertEncodedBound(t *testing.T, events []Event) {
	t.Helper()
	var bound, accounted uint64
	var records []protocol.Record
	for i, event := range events {
		body, err := json.Marshal(envelope{EventID: event.EventID, EventType: event.EventType,
			AggregateType: event.AggregateType, AggregateID: event.AggregateID,
			Version: event.AggregateVersion, Payload: json.RawMessage(event.Payload)})
		if err != nil {
			t.Fatal("synthetic envelope failed to marshal")
		}
		message := kafka.Message{Key: []byte(event.AggregateID), Value: body, Headers: []kafka.Header{
			{Key: "eventType", Value: []byte(event.EventType)}, {Key: "eventId", Value: []byte(event.EventID)},
			{Key: "version", Value: []byte(strconv.Itoa(event.AggregateVersion))}}}
		oneBound := eventEncodedUpperBound(event)
		if pinnedWriterSize(message) > oneBound {
			t.Fatal("conservative bound underestimated actual envelope/key/header accounting")
		}
		bound += oneBound
		accounted += pinnedWriterSize(message)
		headers := make([]protocol.Header, len(message.Headers))
		for j, header := range message.Headers {
			headers[j] = protocol.Header{Key: header.Key, Value: header.Value}
		}
		records = append(records, protocol.Record{Offset: int64(i), Time: time.Unix(0, 0),
			Key: protocol.NewBytes(message.Key), Value: protocol.NewBytes(message.Value), Headers: headers})
	}
	set := protocol.RecordSet{Version: 2, Records: protocol.NewRecordReader(records...)}
	var encoded bytes.Buffer
	if _, err := set.WriteTo(&encoded); err != nil {
		t.Fatal(err)
	}
	if uint64(encoded.Len()) > bound || accounted > bound {
		t.Fatal("bound underestimated the actual SDK record-set encoding or writer accounting")
	}
}

func TestEncodedUpperBoundCoversEscapingUnicodeHeadersAndRecordFraming(t *testing.T) {
	values := []string{"", "plain", "\x00\b\f\n\r\t\"\\<>&", "中文🙂\u2028\u2029", string([]byte{0xff, 0xfe}),
		strings.Repeat("<>&", 10000), strings.Repeat("long-header-\"\\", 1000), strings.Repeat("\x00", 2000)}
	maximum := int(^uint(0) >> 1)
	for _, value := range values {
		for _, version := range []int{-maximum - 1, 0, maximum} {
			event := boundedEvent(1, value)
			event.EventID, event.EventType, event.AggregateType, event.AggregateID = value, value, value, value
			event.AggregateVersion = version
			assertEncodedBound(t, []Event{event})
		}
	}
	var many []Event
	for i := 0; i < MaxBatchSize; i++ {
		many = append(many, boundedEvent(uint64(i+1), "<>&\u2028中文"))
	}
	assertEncodedBound(t, many)
	// RawMessage may arrive from MySQL with literal HTML/Unicode separators;
	// unlike json.Marshal's default output these exercise payload expansion itself.
	for _, rawText := range []string{strings.Repeat("<>&", 10000), strings.Repeat("\u2028\u2029", 10000)} {
		event := boundedEvent(1, "")
		event.Payload = []byte(`{"text":"` + rawText + `"}`)
		assertEncodedBound(t, []Event{event})
	}
}

func TestEncodedUpperBoundDeterministicProperties(t *testing.T) {
	random := rand.New(rand.NewSource(173))
	alphabet := []rune{'a', '<', '>', '&', '\x00', '"', '\\', '中', '🙂', '\u2028', '\u2029'}
	word := func() string {
		letters := make([]rune, random.Intn(200))
		for i := range letters {
			letters[i] = alphabet[random.Intn(len(alphabet))]
		}
		return string(letters)
	}
	for i := 0; i < 300; i++ {
		event := boundedEvent(uint64(i+1), word())
		event.EventID, event.EventType, event.AggregateType, event.AggregateID = word(), word(), word(), word()
		event.AggregateVersion = random.Int()
		assertEncodedBound(t, []Event{event})
	}
}

func TestEncodedUpperBoundSaturatesInsteadOfOverflowing(t *testing.T) {
	maximum := ^uint64(0)
	for _, lengths := range [][5]uint64{{maximum, 0, 0, 0, 0}, {0, maximum, 0, 0, 0},
		{0, maximum / 8, maximum / 8, 0, 0}, {maximum / 6, 1, 1, 1, 1}} {
		if encodedUpperBound(lengths[0], lengths[1], lengths[2], lengths[3], lengths[4]) != maximum {
			t.Fatal("overflowing synthetic lengths must conservatively saturate")
		}
	}
}

func TestClaimBatchBoundedValidatesBudgetBeforeDependencyAccess(t *testing.T) {
	repo := NewRepository(nil)
	for _, limits := range [][2]int{{1, 0}, {1, -1}, {1, MaxBatchBytes + 1}, {0, 1}, {MaxBatchSize + 1, 1}} {
		if events, err := repo.ClaimBatchBounded(context.Background(), limits[0], limits[1]); events != nil || !errors.Is(err, ErrInvalidBatch) {
			t.Fatal("invalid byte/count limits must fail without accessing dependencies")
		}
	}
}

func TestClaimBatchBoundedChoosesExactPrefixAndLeavesTailUnleased(t *testing.T) {
	first, second, third := boundedEvent(1, "a"), boundedEvent(2, "b"), boundedEvent(3, "c")
	for _, extra := range []int{0, -1} {
		script := &batchScript{rows: []Event{first, second, third}}
		budget := int(eventEncodedUpperBound(first)+eventEncodedUpperBound(second)) + extra
		events, err := scriptedBatchRepository(t, script).ClaimBatchBounded(context.Background(), 3, budget)
		want := 2
		if extra < 0 {
			want = 1
		}
		if err != nil || len(events) != want || len(script.execIDs) != want || script.commits != 1 {
			t.Fatal("only the fitting prefix may be leased and charged an attempt")
		}
	}
	large := boundedEvent(2, strings.Repeat("x", 10000))
	script := &batchScript{rows: []Event{first, large, third}}
	events, err := scriptedBatchRepository(t, script).ClaimBatchBounded(context.Background(), 3, int(eventEncodedUpperBound(first)+eventEncodedUpperBound(third)))
	if err != nil || len(events) != 1 || !reflect.DeepEqual(script.execIDs, []uint64{1}) {
		t.Fatal("a non-fitting middle row cannot be skipped to lease a later smaller event")
	}
}

func TestClaimBatchBoundedLargeSingletonAndExhaustedHead(t *testing.T) {
	large, small := boundedEvent(1, strings.Repeat("x", 10000)), boundedEvent(2, "small")
	script := &batchScript{rows: []Event{large, small}}
	events, err := scriptedBatchRepository(t, script).ClaimBatchBounded(context.Background(), 2, 1)
	if err != nil || len(events) != 1 || events[0].ID != 1 || !reflect.DeepEqual(script.execIDs, []uint64{1}) {
		t.Fatal("an oversized first eligible event must make progress as one singleton")
	}
	large.Attempts = maxAttempts
	script = &batchScript{rows: []Event{large, small}}
	events, err = scriptedBatchRepository(t, script).ClaimBatchBounded(context.Background(), 2, int(eventEncodedUpperBound(small)))
	if err != nil || len(events) != 1 || events[0].ID != 2 || !reflect.DeepEqual(script.execIDs, []uint64{1, 2}) {
		t.Fatal("parking an exhausted head must not consume bytes or swallow the next fitting event")
	}
}

func TestClaimBatchBoundedRollbackAndOriginalUnboundedContract(t *testing.T) {
	rows := []Event{boundedEvent(1, "one"), boundedEvent(2, "two")}
	cause := errors.New("synthetic update failure")
	script := &batchScript{rows: rows, failAt: 1, execError: cause}
	if events, err := scriptedBatchRepository(t, script).ClaimBatchBounded(context.Background(), 2, MaxBatchBytes); events != nil || !errors.Is(err, cause) || script.rollbacks != 1 {
		t.Fatal("bounded claim failure must roll back the leased prefix and expose no events")
	}
	script = &batchScript{rows: rows}
	if events, err := scriptedBatchRepository(t, script).ClaimBatch(context.Background(), 2); err != nil || len(events) != 2 {
		t.Fatal("the original ClaimBatch must keep its byte-unbounded contract")
	}
}
