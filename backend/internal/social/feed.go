package social

import (
	"context"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
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
	Source     string     `json:"source,omitempty"`
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
	limit int) (page FeedPage, returnedErr error) {
	ctx, finishObservation := taildiag.Start(ctx, service.observations, false)
	span := taildiag.Begin(ctx, taildiag.FeedService, 0)
	defer func() {
		span.End(returnedErr != nil)
		finishObservation(returnedErr != nil)
	}()
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
	var result FeedPage
	facts := taildiag.Begin(ctx, taildiag.FeedFacts, 0)
	if service.projections {
		err = service.readSnapshot(ctx, func(reader *Service) error {
			var queryErr error
			result, queryErr = reader.feedPage(ctx, user, cursorAt, cursorKey, hasCursor, limit)
			return queryErr
		})
	} else {
		result, err = service.feedPage(ctx, user, cursorAt, cursorKey, hasCursor, limit)
	}
	facts.End(err != nil)
	if err != nil {
		return result, err
	}
	// Facts, authorization, own likes and the cursor are already fixed. Release
	// the snapshot connection before a cold cache borrows from the same DB pool.
	return service.withFeedCounts(ctx, result), nil
}

func (service *Service) withFeedCounts(ctx context.Context, page FeedPage) FeedPage {
	if service.counts == nil || len(page.Items) == 0 {
		return page
	}
	ids := make([]uint64, len(page.Items))
	for index, item := range page.Items {
		id, err := strconv.ParseUint(item.ID, 10, 64)
		if err != nil || id == 0 {
			return page
		}
		ids[index] = id
	}
	// Public counts retain their bounded-cache semantics. Any cache error keeps
	// the counts already read with the authorized page, without failing the feed.
	span := taildiag.Begin(ctx, taildiag.FeedCounts, len(ids))
	counts, err := service.counts.GetMany(ctx, ids)
	span.End(err != nil)
	if err != nil {
		return page
	}
	for index, id := range ids {
		if cached, ok := counts[id]; ok {
			page.Items[index].Liked = int(cached.Likes)
			page.Items[index].Comments = int(cached.Comments)
		}
	}
	return page
}

func (service *Service) feedPage(ctx context.Context, user uint64, cursorAt time.Time,
	cursorKey uint64, hasCursor bool, limit int) (FeedPage, error) {
	// Read the author catalog first to narrow candidates. The final SQL still
	// verifies the current relation before LIMIT. Global-order and author-first
	// indexes are alternatives; the actual plan can still join and sort.
	authors := make([]uint64, 0)
	if err := service.db.WithContext(ctx).Raw(`SELECT follow_user_id FROM tb_follow
		WHERE user_id=? AND active=1`, user).Scan(&authors).Error; err != nil {
		return FeedPage{}, err
	}
	if len(authors) == 0 {
		return FeedPage{Items: []FeedItem{}, Source: sourceDatabase}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(authors)), ",")
	where := ` WHERE b.status='PUBLISHED' AND b.user_id IN (` + placeholders + `)`
	args := make([]any, 0, len(authors)+3)
	for _, author := range authors {
		args = append(args, author)
	}
	// The catalog only narrows candidates. The current relation and public-state
	// predicates remain in the final, pre-LIMIT SQL authorization boundary.
	where += ` AND EXISTS(SELECT 1 FROM tb_follow f
		WHERE f.user_id=? AND f.follow_user_id=b.user_id AND f.active=1)`
	args = append(args, user)
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
	// One join assembles shop labels for the page. Index selection and sorting
	// depend on the author distribution and must be checked in the actual plan.
	projected := service.projections && service.feedCatalogComplete(ctx, authors)
	query := func(useCatalog bool) error {
		catalog := ""
		if useCatalog {
			catalog = ` INNER JOIN tb_community_note_projection p ON p.note_id=b.id
				AND p.author_id=b.user_id AND p.version=b.publish_version
				AND p.published_at=b.create_time`
		}
		return service.db.WithContext(ctx).Raw(`SELECT b.id,b.user_id,b.shop_id,s.name AS shop_name,
			b.title,b.images,b.content,b.liked,b.comments,b.create_time
			FROM tb_blog b LEFT JOIN tb_shop s ON s.id=b.shop_id`+catalog+where+
			` ORDER BY b.create_time DESC,b.id DESC LIMIT ?`, args...).Scan(&rows).Error
	}
	if err := query(projected); err != nil {
		if !projected || ctx.Err() != nil {
			return FeedPage{}, err
		}
		// Projection availability is optional even after a successful completeness
		// check. Retry only the read against the same authoritative snapshot.
		rows = rows[:0]
		projected = false
		if err = query(false); err != nil {
			return FeedPage{}, err
		}
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
		if err := service.db.WithContext(ctx).Raw(`SELECT blog_id FROM tb_blog_like
			WHERE user_id=? AND active=1 AND blog_id IN (`+likePlaceholders+`)`,
			likeArgs...).Scan(&likedIDs).Error; err != nil {
			return FeedPage{}, err
		}
		for _, id := range likedIDs {
			liked[id] = true
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
		item := FeedItem{ID: strconv.FormatUint(row.ID, 10),
			UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content,
			Images: images, Liked: int(row.Liked), Comments: int(row.Comments),
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
	page := FeedPage{Items: items, Source: sourceDatabase}
	if projected {
		page.Source = sourceProjection
	}
	if len(items) == limit {
		last := items[len(items)-1]
		page.NextCursor = &Cursor{PublishedAt: last.CreateTime, ID: last.ID}
	}
	return page, nil
}
