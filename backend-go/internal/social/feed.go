package social

import (
	"context"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
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
	ID         string   `json:"id"`
	UserID     string   `json:"userId"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Images     []string `json:"images"`
	Liked      int      `json:"liked"`
	Comments   int      `json:"comments"`
	CreateTime string   `json:"createTime"`
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
	where := ` WHERE status='PUBLISHED' AND user_id IN (` + placeholders + `)`
	args := make([]any, 0, len(authors)+3)
	for _, author := range authors {
		args = append(args, author)
	}
	if hasCursor {
		where += " AND (create_time < ? OR (create_time = ? AND id < ?))"
		args = append(args, cursorAt, cursorAt, cursorKey)
	}
	args = append(args, limit)
	rows := make([]struct {
		ID         uint64
		UserID     uint64
		Title      string
		Images     string
		Content    string
		Liked      uint64
		Comments   uint64
		CreateTime time.Time
	}, 0, limit)
	if err = service.db.WithContext(ctx).Raw(`SELECT id,user_id,title,images,content,liked,comments,create_time
		FROM tb_blog`+where+` ORDER BY create_time DESC,id DESC LIMIT ?`, args...).Scan(&rows).Error; err != nil {
		return FeedPage{}, err
	}
	items := make([]FeedItem, 0, len(rows))
	for _, row := range rows {
		images := make([]string, 0)
		for _, image := range strings.Split(row.Images, ",") {
			if image = strings.TrimSpace(image); image != "" {
				images = append(images, image)
			}
		}
		items = append(items, FeedItem{ID: strconv.FormatUint(row.ID, 10),
			UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content,
			Images: images, Liked: int(row.Liked), Comments: int(row.Comments),
			CreateTime: row.CreateTime.In(zone()).Format(feedTimeLayout)})
	}
	page := FeedPage{Items: items}
	if len(items) == limit {
		last := items[len(items)-1]
		page.NextCursor = &Cursor{PublishedAt: last.CreateTime, ID: last.ID}
	}
	return page, nil
}
