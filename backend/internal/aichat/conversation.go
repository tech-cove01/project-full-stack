// Package aichat owns the assistant conversation surface: Redis-backed sessions,
// retrieval, self-scoped tools and the SSE turn. Business authorization always
// comes from the authenticated user of the request.
package aichat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrNotFound = errors.New("会话不存在")
	ErrInput    = errors.New("消息内容为空、过长或格式错误")
)

const (
	conversationTTL = 24 * time.Hour
	maxMessages     = 40
	maxMessageRunes = 2000
)

type Message struct {
	Role        string   `json:"role"`
	Text        string   `json:"text"`
	CitationIDs []string `json:"citationIds,omitempty"`
	CreateTime  string   `json:"createTime"`
	Time        string   `json:"time"`
}

type Conversation struct {
	ID         string    `json:"id"`
	ShopID     *string   `json:"shopId,omitempty"`
	CreateTime string    `json:"createTime"`
	CreatedAt  string    `json:"createdAt"`
	Messages   []Message `json:"messages"`
}

type Conversations struct{ redis *redis.Client }

func NewConversations(client *redis.Client) *Conversations { return &Conversations{redis: client} }

// appendScript checks existence and writes in one step, so a delete that lands
// while a turn is finishing cannot recreate the conversation.
var appendScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
redis.call('RPUSH', KEYS[2], ARGV[1])
redis.call('LTRIM', KEYS[2], -tonumber(ARGV[2]), -1)
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[3]))
redis.call('HSET', KEYS[1], 'updatedAt', ARGV[4])
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[3]))
return 1
`)

func keyConversation(userID uint64, conversationID string) string {
	return "ai:conv:" + strconv.FormatUint(userID, 10) + ":" + conversationID
}

func keyMessages(userID uint64, conversationID string) string {
	return "ai:msgs:" + strconv.FormatUint(userID, 10) + ":" + conversationID
}

func newID() (string, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(entropy[:]), nil
}

func now() string { return time.Now().Format("2006-01-02 15:04:05") }

func (store *Conversations) Create(ctx context.Context, userID uint64, shopID *uint64) (Conversation, error) {
	id, err := newID()
	if err != nil {
		return Conversation{}, err
	}
	created := now()
	key := keyConversation(userID, id)
	values := map[string]any{"createTime": created, "updatedAt": created}
	conversation := Conversation{ID: id, CreateTime: created, CreatedAt: created, Messages: make([]Message, 0)}
	if shopID != nil {
		shop := strconv.FormatUint(*shopID, 10)
		values["shopId"] = shop
		conversation.ShopID = &shop
	}
	pipe := store.redis.TxPipeline()
	pipe.HSet(ctx, key, values)
	pipe.Expire(ctx, key, conversationTTL)
	if _, err = pipe.Exec(ctx); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func (store *Conversations) Get(ctx context.Context, userID uint64, id string) (Conversation, error) {
	raw, err := store.redis.HGetAll(ctx, keyConversation(userID, id)).Result()
	if err != nil {
		return Conversation{}, err
	}
	if len(raw) == 0 {
		return Conversation{}, ErrNotFound
	}
	messages, err := store.History(ctx, userID, id)
	if err != nil {
		return Conversation{}, err
	}
	result := Conversation{ID: id, CreateTime: raw["createTime"], CreatedAt: raw["createTime"], Messages: messages}
	if value := raw["shopId"]; value != "" {
		result.ShopID = &value
	}
	return result, nil
}

func (store *Conversations) Exists(ctx context.Context, userID uint64, id string) (bool, error) {
	count, err := store.redis.Exists(ctx, keyConversation(userID, id)).Result()
	return count > 0, err
}

func (store *Conversations) History(ctx context.Context, userID uint64, id string) ([]Message, error) {
	exists, err := store.Exists(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	raw, err := store.redis.LRange(ctx, keyMessages(userID, id), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(raw))
	for _, item := range raw {
		var message Message
		if json.Unmarshal([]byte(item), &message) == nil {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func (store *Conversations) Delete(ctx context.Context, userID uint64, id string) error {
	exists, err := store.Exists(ctx, userID, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	pipe := store.redis.TxPipeline()
	pipe.Del(ctx, keyConversation(userID, id), keyMessages(userID, id))
	_, err = pipe.Exec(ctx)
	return err
}

// Append drops the message when the conversation was deleted meanwhile.
func (store *Conversations) Append(ctx context.Context, userID uint64, id string, message Message) error {
	message.CreateTime = now()
	message.Time = message.CreateTime
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = appendScript.Run(ctx, store.redis,
		[]string{keyConversation(userID, id), keyMessages(userID, id)},
		string(encoded), strconv.Itoa(maxMessages),
		strconv.Itoa(int(conversationTTL.Seconds())), message.CreateTime).Result()
	return err
}
