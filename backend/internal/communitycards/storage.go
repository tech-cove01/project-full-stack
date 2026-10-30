package communitycards

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type loadFacts struct{ rows, bytes uint64 }
type storedValue struct {
	ref  Ref
	body []byte
	ttl  time.Duration
}

type backend interface {
	read(context.Context, []Ref) (map[Ref][]byte, error)
	load(context.Context, []Ref) (map[Ref]Card, loadFacts, error)
	write(context.Context, []storedValue) error
}

type storage struct {
	redis *redis.Client
	db    *gorm.DB
}

func key(ref Ref) string {
	return "community:note-card:v1:" + strconv.FormatUint(ref.ID, 10) + ":" + strconv.FormatUint(ref.Version, 10)
}

func (store storage) read(ctx context.Context, refs []Ref) (map[Ref][]byte, error) {
	keys := make([]string, len(refs))
	for index, ref := range refs {
		keys[index] = key(ref)
	}
	values, err := store.redis.MGet(ctx, keys...).Result()
	result := make(map[Ref][]byte, len(refs))
	if err != nil {
		return result, err
	}
	for index, value := range values {
		if text, ok := value.(string); ok {
			result[refs[index]] = []byte(text)
		}
	}
	return result, nil
}

func (store storage) load(ctx context.Context, refs []Ref) (map[Ref]Card, loadFacts, error) {
	ids, args := make(map[uint64]bool, len(refs)), make([]any, 0, len(refs))
	for _, ref := range refs {
		if !ids[ref.ID] {
			ids[ref.ID] = true
			args = append(args, ref.ID)
		}
	}
	var rows []struct {
		ID, PublishVersion     uint64
		Title, Content, Images string
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")
	err := store.db.WithContext(ctx).Raw(`SELECT id,publish_version,title,content,images FROM tb_blog
		WHERE status='PUBLISHED' AND id IN (`+placeholders+`)`, args...).Scan(&rows).Error
	facts := loadFacts{rows: uint64(len(rows))}
	result := make(map[Ref]Card, len(rows))
	for _, row := range rows {
		facts.bytes += uint64(len(row.Title) + len(row.Content) + len(row.Images))
		images := make([]string, 0)
		for _, image := range strings.Split(row.Images, ",") {
			if image = strings.TrimSpace(image); image != "" {
				images = append(images, image)
			}
		}
		result[Ref{ID: row.ID, Version: row.PublishVersion}] = Card{Title: row.Title, Content: row.Content, Images: images}
	}
	return result, facts, err
}

func (store storage) write(ctx context.Context, values []storedValue) error {
	_, err := store.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, value := range values {
			pipe.Set(ctx, key(value.ref), value.body, value.ttl)
		}
		return nil
	})
	return err
}
