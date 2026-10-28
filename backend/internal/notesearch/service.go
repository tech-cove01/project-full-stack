package notesearch

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"gorm.io/gorm"
)

type Service struct {
	db     *gorm.DB
	client *Client
	key    [32]byte
	ready  bool
	now    func() time.Time
}
type SearchRequest struct {
	Query, ShopID, Cursor, Sort string
	Limit                       int
}
type Item struct {
	ID         string   `json:"id"`
	UserID     string   `json:"userId"`
	ShopID     *string  `json:"shopId,omitempty"`
	ShopName   string   `json:"shopName,omitempty"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Images     []string `json:"images"`
	CreateTime string   `json:"createTime"`
}
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}
type pageCursor struct {
	PIT     string            `json:"pit"`
	Query   string            `json:"query"`
	Shop    string            `json:"shop"`
	Sort    string            `json:"sort"`
	Limit   int               `json:"limit"`
	Expires int64             `json:"expires"`
	After   []json.RawMessage `json:"after"`
}

func NewService(db *gorm.DB, client *Client) *Service {
	s := &Service{db: db, client: client, now: time.Now}
	_, err := rand.Read(s.key[:])
	s.ready = err == nil
	return s
}
func normalize(request SearchRequest) (SearchRequest, error) {
	request.Query = strings.TrimSpace(request.Query)
	if !validText(request.Query, 100) || (request.ShopID != "" && !validID(request.ShopID)) {
		return request, ErrInput
	}
	if request.Sort == "" {
		request.Sort = "relevance"
	}
	if request.Sort != "relevance" && request.Sort != "recent" {
		return request, ErrInput
	}
	if request.Limit == 0 {
		request.Limit = 10
	}
	if request.Limit < 1 || request.Limit > 20 || len(request.Cursor) > 24000 {
		return request, ErrInput
	}
	return request, nil
}
func (s *Service) decode(request SearchRequest) (pageCursor, error) {
	var cursor pageCursor
	parts := strings.Split(request.Cursor, ".")
	if len(parts) != 2 {
		return cursor, ErrCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, ErrCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, ErrCursor
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), signature) || json.Unmarshal(body, &cursor) != nil {
		return cursor, ErrCursor
	}
	if cursor.Query != request.Query || cursor.Shop != request.ShopID || cursor.Sort != request.Sort || cursor.Limit != request.Limit || cursor.Expires <= s.now().Unix() || cursor.Expires > s.now().Add(time.Minute).Unix() || cursor.PIT == "" || len(cursor.PIT) > 12000 || len(cursor.After) == 0 {
		return cursor, ErrCursor
	}
	return cursor, nil
}
func (s *Service) encode(cursor pageCursor) string {
	body, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Search pages one PIT snapshot of raw Elasticsearch candidates. Current SQL
// facts decide visibility and returned text; filtering may intentionally shorten
// a page. The next cursor advances past the last raw hit, even on an empty page.
func (s *Service) Search(ctx context.Context, request SearchRequest) (Page, error) {
	request, err := normalize(request)
	if err != nil {
		return Page{}, err
	}
	if s.client == nil || s.db == nil || !s.ready {
		return Page{}, ErrUnavailable
	}
	var cursor pageCursor
	if request.Cursor != "" {
		cursor, err = s.decode(request)
		if err != nil {
			return Page{}, err
		}
	} else {
		cursor.PIT, err = s.client.openPIT(ctx)
		if err != nil {
			return Page{}, err
		}
	}
	pit := cursor.PIT
	keep := false
	defer func() {
		if !keep {
			s.client.closePIT(pit)
		}
	}()
	candidates, newPIT, err := s.client.query(ctx, pit, request, cursor.After)
	if newPIT != "" {
		pit = newPIT
	}
	if err != nil {
		return Page{}, err
	}
	items := make([]Item, 0, len(candidates))
	if len(candidates) > 0 {
		ids := make([]uint64, len(candidates))
		for i, item := range candidates {
			ids[i], _ = strconv.ParseUint(item.ID, 10, 64)
		}
		var facts []struct {
			ID, UserID, ShopID               uint64
			ShopName, Title, Content, Images string
			PublishVersion                   int
			CreateTime                       time.Time
		}
		if err = s.db.WithContext(ctx).Raw(`SELECT b.id,b.user_id,b.shop_id,s.name AS shop_name,b.title,b.content,b.images,b.publish_version,b.create_time FROM tb_blog b LEFT JOIN tb_shop s ON s.id=b.shop_id WHERE b.id IN ? AND b.status='PUBLISHED' AND (b.shop_id=0 OR s.id IS NOT NULL)`, ids).Scan(&facts).Error; err != nil {
			return Page{}, ErrUnavailable
		}
		byID := make(map[string]int, len(facts))
		for i, row := range facts {
			byID[strconv.FormatUint(row.ID, 10)] = i
		}
		zone, _ := time.LoadLocation("Asia/Shanghai")
		for _, hit := range candidates {
			index, exists := byID[hit.ID]
			if !exists {
				continue
			}
			row := facts[index]
			shop := strconv.FormatUint(row.ShopID, 10)
			if row.PublishVersion != hit.Version || (request.ShopID != "" && request.ShopID != shop) {
				continue
			}
			images := make([]string, 0)
			for _, image := range strings.Split(row.Images, ",") {
				if image = strings.TrimSpace(image); image != "" {
					images = append(images, image)
				}
			}
			item := Item{ID: hit.ID, UserID: strconv.FormatUint(row.UserID, 10), Title: row.Title, Content: row.Content, Images: images, CreateTime: row.CreateTime.In(zone).Format("2006-01-02 15:04:05"), ShopName: row.ShopName}
			if row.ShopID != 0 {
				item.ShopID = &shop
			}
			items = append(items, item)
		}
	}
	page := Page{Items: items}
	if len(candidates) == request.Limit {
		page.NextCursor = s.encode(pageCursor{PIT: pit, Query: request.Query, Shop: request.ShopID, Sort: request.Sort, Limit: request.Limit, Expires: s.now().Add(time.Minute).Unix(), After: candidates[len(candidates)-1].After})
		keep = true
	}
	return page, nil
}
