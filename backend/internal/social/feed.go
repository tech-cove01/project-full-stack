package social

import (
	"context"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/cityloop/fullstack-go/internal/communitycache"
)

const (
	feedDefaultLimit = 10
	feedMaxLimit     = 50
	feedTimeLayout   = "2006-01-02 15:04:05"
)

type Cursor struct {
	PublishedAt string `json:"publishedAt"`
	ID          string `json:"id"`
}

type FeedItem struct {
	ID       string   `json:"id"`
	UserID   string   `json:"userId"`
	ShopID   *string  `json:"shopId,omitempty"`
	ShopName string   `json:"shopName,omitempty"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Images   []string `json:"images"`
	Liked    int      `json:"liked"`
	Comments int      `json:"comments"`
	// LikedByMe is filled from one batched query for the whole page, never one
	// round trip per card.
	LikedByMe  bool   `json:"likedByMe"`
	CreateTime string `json:"createTime"`
}

type FeedPage struct {
	Items      []FeedItem `json:"items"`
	NextCursor *Cursor    `json:"nextCursor,omitempty"`
}

func zone() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return location
}

// Feed lists published notes from followed authors with a (publishedAt,id) key.
// The compound key keeps pages stable when two notes share a timestamp: the id
// breaks the tie, so a later page cannot repeat or skip a row.
func (service *Service) Feed(ctx context.Context, userID, cursorTime, cursorID string,
	limit int) (FeedPage, error) {
	user, err := positive(userID)
	if err != nil {
		return FeedPage{}, err
	}
	if limit <= 0 {
		limit = feedDefaultLimit
	}
	if limit > feedMaxLimit {
		return FeedPage{}, ErrPagination
	}
	// Validate the cursor before any query: a malformed page pointer must be a 400
	// without touching storage.
	var cursorAt time.Time
	var cursorKey uint64
	hasCursor := cursorTime != "" || cursorID != ""
	if hasCursor {
		if cursorTime == "" || cursorID == "" {
			return FeedPage{}, ErrPagination
		}
		parsed, err := time.ParseInLocation(feedTimeLayout, cursorTime, zone())
		if err != nil {
			return FeedPage{}, ErrPagination
		}
		key, err := strconv.ParseUint(cursorID, 10, 64)
		if err != nil || key == 0 {
			return FeedPage{}, ErrPagination
		}
		cursorAt, cursorKey = parsed, key
	}
	// Two steps on purpose: a join makes MySQL sort the whole joined set
	// (Using temporary; Using filesort). Reading the author ids first lets the
	// note query scan idx_blog_feed backwards and stop at the page limit.
	authors := make([]uint64, 0)
	if err = service.db.WithContext(ctx).Raw(`SELECT follow_user_id FROM tb_follow
		WHERE user_id=? AND active=1`, user).Scan(&authors).Error; err != nil {
		return FeedPage{}, err
	}
	if len(authors) == 0 {
		return FeedPage{Items: []FeedItem{}}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(authors)), ",")
	where := ` WHERE b.status='PUBLISHED' AND b.user_id IN (` + placeholders + `)`
	args := make([]any, 0, len(authors)+3)
	for _, author := range authors {
		args = append(args, author)
	}
	if hasCursor {
		where += " AND (b.create_time < ? OR (b.create_time = ? AND b.id < ?))"
		args = append(args, cursorAt, cursorAt, cursorKey)
	}
	args = append(args, limit)
	rows := make([]struct {
		ID         uint64
		UserID     uint64
		ShopID     uint64
		ShopName   *string
		Title      string
		Images     string
		Content    string
		Liked      uint64
		Comments   uint64
		CreateTime time.Time
	}, 0, limit)
	// One join assembles the shop label for the whole page instead of querying it
	// per card; the feed still scans idx_blog_feed for the ordered page.
	if err = service.db.WithContext(ctx).Raw(`SELECT b.id,b.user_id,b.shop_id,s.name AS shop_name,
		b.title,b.images,b.content,b.liked,b.comments,b.create_time
		FROM tb_blog b LEFT JOIN tb_shop s ON s.id=b.shop_id`+where+
		` ORDER BY b.create_time DESC,b.id DESC LIMIT ?`, args...).Scan(&rows).Error; err != nil {
		return FeedPage{}, err
	}
	// One query for the caller's own likes, not one per card.
	liked := make(map[uint64]bool, len(rows))
	if len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		likePlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		likeArgs := make([]any, 0, len(ids)+1)
		likeArgs = append(likeArgs, user)
		for _, id := range ids {
			likeArgs = append(likeArgs, id)
		}
		likedIDs := make([]uint64, 0)
		if err = service.db.WithContext(ctx).Raw(`SELECT blog_id FROM tb_blog_like
			WHERE user_id=? AND active=1 AND blog_id IN (`+likePlaceholders+`)`,
			likeArgs...).Scan(&likedIDs).Error; err != nil {
			return FeedPage{}, err
		}
		for _, id := range likedIDs {
			liked[id] = true
		}
	}
	// Counters come from the page cache in one MGET; a miss falls back to the rows
	// already loaded, so the feed never issues a query per card.
	cachedCounts := map[uint64]communitycache.Counts{}
	if service.counts != nil {
		ids := make([]uint64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if loaded, cacheErr := service.counts.GetMany(ctx, ids); cacheErr == nil {
			cachedCounts = loaded
		}
	}
	items := make([]FeedItem, 0, len(rows))
	for _, row := range rows {
		images := make([]string, 0)
		for _, image := range strings.Split(row.Images, ",") {
			if image = strings.TrimSpace(image); image != "" {
				images = append(images, image)
			}
		}
		likes, comments := int(row.Liked), int(row.Comments)
		if cached, ok := cachedCounts[row.ID]; ok {
			likes, comments = int(cached.Likes), int(cached.Comments)
		}
		item := FeedItem{ID: strconv.FormatUint(row.ID, 10),
			UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content,
			Images: images, Liked: likes, Comments: comments,
			LikedByMe: liked[row.ID], CreateTime: row.CreateTime.In(zone()).Format(feedTimeLayout)}
		if row.ShopID != 0 {
			shop := strconv.FormatUint(row.ShopID, 10)
			item.ShopID = &shop
		}
		if row.ShopName != nil {
			item.ShopName = *row.ShopName
		}
		items = append(items, item)
	}
	page := FeedPage{Items: items}
	if len(items) == limit {
		last := items[len(items)-1]
		page.NextCursor = &Cursor{PublishedAt: last.CreateTime, ID: last.ID}
	}
	return page, nil
}
