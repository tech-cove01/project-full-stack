package communityprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	"gorm.io/gorm"
)

var sourceTopic = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

// Runtime combines the atomic read-model repository with durable failure
// storage. A successful Apply/Park is the consumer's only offset-commit proof.
type Runtime struct{ *Repository }

func NewRuntime(repository *Repository) *Runtime { return &Runtime{repository} }

func validCoordinates(projection string, message Message) bool {
	return (projection == Follow || projection == Feed) && sourceTopic.MatchString(message.Topic) &&
		message.Topic != "." && message.Topic != ".." && message.Partition >= 0 && message.Offset >= 0
}

func checkedEvent(message Message) error {
	event, err := Decode(message.Body)
	if err != nil {
		return err
	}
	if !bytes.Equal(message.Key, []byte(event.AggregateID)) {
		return ErrInvalid
	}
	expected := map[string]string{"eventId": event.EventID, "eventType": event.EventType, "version": strconv.Itoa(event.Version)}
	seen := map[string]bool{}
	for _, header := range message.Headers {
		if len(header.Key) > 128 || len(header.Value) > 1024 {
			return ErrInvalid
		}
		if value, present := expected[header.Key]; present {
			if seen[header.Key] || string(header.Value) != value {
				return ErrInvalid
			}
			seen[header.Key] = true
		}
	}
	return nil
}

func (runtime *Runtime) Apply(ctx context.Context, projection string, message Message) error {
	if !validCoordinates(projection, message) || len(message.Key) > 1024 || len(message.Body) > 1<<20 || len(message.Headers) > 32 {
		return ErrInvalid
	}
	if err := checkedEvent(message); err != nil {
		return err
	}
	return runtime.Repository.Apply(ctx, projection, message.Body)
}

func messageDigest(message Message) ([32]byte, []byte, error) {
	headers, err := json.Marshal(message.Headers)
	if err != nil {
		return [32]byte{}, nil, err
	}
	// Length-independent structured encoding protects key/body/header boundaries.
	value, err := json.Marshal(struct{ Key, Body, Headers []byte }{nonNil(message.Key), nonNil(message.Body), headers})
	return sha256.Sum256(value), headers, err
}

func (runtime *Runtime) Park(ctx context.Context, projection string, message Message, reason string) error {
	if !validCoordinates(projection, message) || reason != "apply_failed" {
		return ErrInvalid
	}
	hash, headers, err := messageDigest(message)
	if err != nil {
		return err
	}
	return runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO tb_community_consumer_failure
 (projection,topic,partition_id,source_offset,message_hash,message_key,message_body,message_headers,reason)
 VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE projection=projection`, projection, message.Topic,
			message.Partition, message.Offset, hash[:], nonNil(message.Key), nonNil(message.Body), string(headers), reason).Error; err != nil {
			return err
		}
		var existing struct{ MessageHash []byte }
		if err := tx.Raw(`SELECT message_hash FROM tb_community_consumer_failure
 WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? FOR UPDATE`,
			projection, message.Topic, message.Partition, message.Offset).Scan(&existing).Error; err != nil {
			return err
		}
		if !bytes.Equal(existing.MessageHash, hash[:]) {
			return ErrConflict
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func nonNil(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

// Replay never edits the original message to manufacture success. Invalid
// payloads remain parked; a resolved row and applied effect commit together.
func (runtime *Runtime) Replay(ctx context.Context, projection, topic string, partition int, offset int64) error {
	if !validCoordinates(projection, Message{Topic: topic, Partition: partition, Offset: offset}) {
		return ErrInvalid
	}
	return runtime.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			MessageKey, MessageBody, MessageHeaders, MessageHash []byte
			State                                                string
		}
		if err := tx.Raw(`SELECT message_key,message_body,message_headers,message_hash,state FROM tb_community_consumer_failure
 WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? FOR UPDATE`, projection, topic, partition, offset).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("consumer failure not found")
		}
		row := rows[0]
		message := Message{Topic: topic, Partition: partition, Offset: offset, Key: row.MessageKey, Body: row.MessageBody}
		if json.Unmarshal(row.MessageHeaders, &message.Headers) != nil {
			return ErrInvalid
		}
		hash, _, err := messageDigest(message)
		if err != nil || !bytes.Equal(hash[:], row.MessageHash) {
			return ErrConflict
		}
		if row.State == "RESOLVED" {
			return nil
		}
		if err := NewRuntime(New(tx)).Apply(ctx, projection, message); err != nil {
			return err
		}
		return tx.Exec(`UPDATE tb_community_consumer_failure SET state='RESOLVED',resolved_at=NOW(3)
 WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? AND state='PARKED'`, projection, topic, partition, offset).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
