package notesearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/cityloop/fullstack-go/internal/communityprojection"
	"gorm.io/gorm"
)

const Projection = "search-v1"

var (
	ErrSyncInvalid  = errors.New("invalid search projection message")
	ErrSyncConflict = errors.New("search projection conflicts with durable state")
	ErrNeedsRebuild = errors.New("search projection requires controlled rebuild")
	searchTopic     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)
)

// Sync applies public-note facts to Elasticsearch. ES and MySQL cannot share a
// transaction: Write may happen twice, but a receipt is never written before a
// successful external-version write. A replay therefore converges safely.
type Sync struct {
	db     *gorm.DB
	client *Client
}

func NewSync(db *gorm.DB, client *Client) *Sync { return &Sync{db: db, client: client} }

func validSearchCoordinates(projection string, message communityprojection.Message) bool {
	return projection == Projection && searchTopic.MatchString(message.Topic) && message.Topic != "." && message.Topic != ".." && message.Partition >= 0 && message.Offset >= 0
}

func decodeSearchMessage(projection string, message communityprojection.Message) (communityprojection.Event, error) {
	if !validSearchCoordinates(projection, message) || len(message.Key) > 1024 || len(message.Body) > 1<<20 || len(message.Headers) > 32 {
		return communityprojection.Event{}, ErrSyncInvalid
	}
	event, err := communityprojection.Decode(message.Body)
	if err != nil || !bytes.Equal(message.Key, []byte(event.AggregateID)) {
		return communityprojection.Event{}, ErrSyncInvalid
	}
	expected := map[string]string{"eventId": event.EventID, "eventType": event.EventType, "version": strconv.Itoa(event.Version)}
	seen := map[string]bool{}
	for _, header := range message.Headers {
		if len(header.Key) > 128 || len(header.Value) > 1024 {
			return communityprojection.Event{}, ErrSyncInvalid
		}
		if value, present := expected[header.Key]; present {
			if seen[header.Key] || string(header.Value) != value {
				return communityprojection.Event{}, ErrSyncInvalid
			}
			seen[header.Key] = true
		}
	}
	return event, nil
}

type searchControl struct {
	Name, IndexAlias, ActiveGeneration string
	RebuiltAt                          *time.Time
}

func lockSearchControl(tx *gorm.DB) (searchControl, error) {
	var rows []searchControl
	if err := tx.Raw(`SELECT name,index_alias,active_generation,rebuilt_at FROM tb_note_search_control WHERE name=? FOR UPDATE`, Projection).Scan(&rows).Error; err != nil {
		return searchControl{}, err
	}
	if len(rows) != 1 {
		return searchControl{}, ErrNeedsRebuild
	}
	return rows[0], nil
}

func (sync *Sync) Apply(ctx context.Context, projection string, message communityprojection.Message) error {
	event, err := decodeSearchMessage(projection, message)
	if err != nil {
		return err
	}
	if sync.db == nil || sync.client == nil {
		return ErrNeedsRebuild
	}
	return sync.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return sync.apply(tx, event) }, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func (sync *Sync) apply(tx *gorm.DB, event communityprojection.Event) error {
	control, err := lockSearchControl(tx)
	if err != nil {
		return err
	}
	state := "IGNORED"
	if event.EventType == "NOTE_PUBLISHED" {
		state = "APPLIED"
		if control.IndexAlias != sync.client.Index() || control.ActiveGeneration == "" {
			return ErrNeedsRebuild
		}
	}
	var receipts []struct {
		EventHash []byte
		State     string
	}
	if err = tx.Raw(`SELECT event_hash,state FROM tb_community_projection_event WHERE projection=? AND event_id=? FOR UPDATE`, Projection, event.EventID).Scan(&receipts).Error; err != nil {
		return err
	}
	if len(receipts) > 0 {
		if len(receipts) != 1 || !bytes.Equal(receipts[0].EventHash, event.Hash[:]) || receipts[0].State != state {
			return ErrSyncConflict
		}
		return nil
	}
	if state == "APPLIED" {
		document, err := readSearchFact(tx, event.NoteID, true)
		if err != nil {
			return err
		}
		if document.UserID != event.AuthorID || document.Version < event.Version {
			return communityprojection.ErrFactUnavailable
		}
		hash, err := searchDocumentHash(document)
		if err != nil {
			return err
		}
		var stored []struct {
			Version      int
			DocumentHash []byte
		}
		if err = tx.Raw(`SELECT version,document_hash FROM tb_note_search_state WHERE note_id=? FOR UPDATE`, document.ID).Scan(&stored).Error; err != nil {
			return err
		}
		if len(stored) > 0 && (stored[0].Version > document.Version || stored[0].Version == document.Version && !bytes.Equal(stored[0].DocumentHash, hash[:])) {
			return ErrSyncConflict
		}
		// Do not skip Write merely because SQL state matches: a controlled ES
		// rebuild may have changed physical generation, and external_gte makes
		// redoing the exact document/version safe after uncertain SQL commits.
		if err = sync.client.Write(tx.Statement.Context, sync.client.Index(), document); err != nil {
			return err
		}
		if err = tx.Exec(`INSERT INTO tb_note_search_state(note_id,version,document_hash) VALUES(?,?,?) ON DUPLICATE KEY UPDATE version=VALUES(version),document_hash=VALUES(document_hash),applied_at=NOW(3)`, document.ID, document.Version, hash[:]).Error; err != nil {
			return err
		}
	}
	return tx.Exec(`INSERT INTO tb_community_projection_event(projection,event_id,event_hash,state) VALUES(?,?,?,?)`, Projection, event.EventID, event.Hash[:], state).Error
}

func readSearchFact(tx *gorm.DB, noteID string, locked bool) (Document, error) {
	query := `SELECT id,shop_id,user_id,title,content,publish_version AS version,create_time FROM tb_blog WHERE id=? AND status='PUBLISHED'`
	if locked {
		query += " FOR SHARE"
	}
	var rows []struct {
		ID, ShopID, UserID uint64
		Title, Content     string
		Version            int
		CreateTime         time.Time
	}
	if err := tx.Raw(query, noteID).Scan(&rows).Error; err != nil {
		return Document{}, err
	}
	if len(rows) != 1 || rows[0].Version < 1 || rows[0].CreateTime.IsZero() {
		return Document{}, communityprojection.ErrFactUnavailable
	}
	row := rows[0]
	return Document{ID: strconv.FormatUint(row.ID, 10), ShopID: strconv.FormatUint(row.ShopID, 10), UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content, Version: row.Version, PublishedAt: row.CreateTime.UTC().Format(time.RFC3339Nano)}, nil
}

func searchDocumentHash(document Document) ([32]byte, error) {
	raw, err := json.Marshal(document)
	return sha256.Sum256(raw), err
}

// Failure records use GO-072's immutable shared storage format, but this type
// only accepts search-v1 and never changes the follow/feed runtime contract.
func searchMessageDigest(message communityprojection.Message) ([32]byte, []byte, error) {
	headers, err := json.Marshal(message.Headers)
	if err != nil {
		return [32]byte{}, nil, err
	}
	encoded, err := json.Marshal(struct{ Key, Body, Headers []byte }{searchNonNil(message.Key), searchNonNil(message.Body), headers})
	return sha256.Sum256(encoded), headers, err
}
func searchNonNil(value []byte) []byte {
	if value == nil {
		return []byte{}
	}
	return value
}

func (sync *Sync) Park(ctx context.Context, projection string, message communityprojection.Message, reason string) error {
	if !validSearchCoordinates(projection, message) || reason != "apply_failed" {
		return ErrSyncInvalid
	}
	if sync.db == nil {
		return ErrNeedsRebuild
	}
	hash, headers, err := searchMessageDigest(message)
	if err != nil {
		return err
	}
	return sync.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO tb_community_consumer_failure(projection,topic,partition_id,source_offset,message_hash,message_key,message_body,message_headers,reason) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE projection=projection`, Projection, message.Topic, message.Partition, message.Offset, hash[:], searchNonNil(message.Key), searchNonNil(message.Body), string(headers), reason).Error; err != nil {
			return err
		}
		var rows []struct{ MessageHash []byte }
		if err := tx.Raw(`SELECT message_hash FROM tb_community_consumer_failure WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? FOR UPDATE`, Projection, message.Topic, message.Partition, message.Offset).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 || !bytes.Equal(rows[0].MessageHash, hash[:]) {
			return ErrSyncConflict
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

// Replay resolves the parked row and receipt in one SQL transaction. If ES
// succeeded but SQL rolled back, redoing the same external version is safe.
func (sync *Sync) Replay(ctx context.Context, topic string, partition int, offset int64) error {
	if !validSearchCoordinates(Projection, communityprojection.Message{Topic: topic, Partition: partition, Offset: offset}) {
		return ErrSyncInvalid
	}
	if sync.db == nil || sync.client == nil {
		return ErrNeedsRebuild
	}
	return sync.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []struct {
			MessageKey, MessageBody, MessageHeaders, MessageHash []byte
			State                                                string
		}
		if err := tx.Raw(`SELECT message_key,message_body,message_headers,message_hash,state FROM tb_community_consumer_failure WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? FOR UPDATE`, Projection, topic, partition, offset).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != 1 {
			return errors.New("search failure not found")
		}
		row := rows[0]
		message := communityprojection.Message{Topic: topic, Partition: partition, Offset: offset, Key: row.MessageKey, Body: row.MessageBody}
		if json.Unmarshal(row.MessageHeaders, &message.Headers) != nil {
			return ErrSyncInvalid
		}
		hash, _, err := searchMessageDigest(message)
		if err != nil || !bytes.Equal(hash[:], row.MessageHash) {
			return ErrSyncConflict
		}
		if row.State == "RESOLVED" {
			return nil
		}
		event, err := decodeSearchMessage(Projection, message)
		if err != nil {
			return err
		}
		if err = sync.apply(tx, event); err != nil {
			return err
		}
		return tx.Exec(`UPDATE tb_community_consumer_failure SET state='RESOLVED',resolved_at=NOW(3) WHERE projection=? AND topic=? AND partition_id=? AND source_offset=? AND state='PARKED'`, Projection, topic, partition, offset).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

type SyncStats struct {
	Projection       string     `json:"projection"`
	IndexAlias       string     `json:"indexAlias"`
	ActiveGeneration string     `json:"activeGeneration"`
	Receipts         int64      `json:"receipts"`
	Parked           int64      `json:"parked"`
	Resolved         int64      `json:"resolved"`
	Indexed          int64      `json:"indexed"`
	RebuiltAt        *time.Time `json:"rebuiltAt,omitempty"`
}

func (sync *Sync) Stats(ctx context.Context) (SyncStats, error) {
	result := SyncStats{Projection: Projection}
	if sync.db == nil {
		return result, ErrNeedsRebuild
	}
	err := sync.db.WithContext(ctx).Raw(`SELECT c.index_alias,c.active_generation,c.rebuilt_at,
 (SELECT COUNT(*) FROM tb_community_projection_event WHERE projection=?) AS receipts,
 (SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND state='PARKED') AS parked,
 (SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection=? AND state='RESOLVED') AS resolved,
 (SELECT COUNT(*) FROM tb_note_search_state) AS indexed FROM tb_note_search_control c WHERE name=?`, Projection, Projection, Projection, Projection).Scan(&result).Error
	return result, err
}
