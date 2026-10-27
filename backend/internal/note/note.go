// Package note owns shop notes: creation and public detail. Likes, follows and
// the feed build on the same fact table but live in their own tasks.
package note

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"gorm.io/gorm"
)

var (
	ErrInput     = errors.New("标题、正文或图片不合法")
	ErrNotFound  = errors.New("笔记不存在")
	ErrForbidden = errors.New("无权操作该笔记")
)

const (
	maxTitleRunes   = 100
	maxContentRunes = 2000
	maxImages       = 9
	maxImageLength  = 512
)

type View struct {
	ID             string   `json:"id"`
	ShopID         *string  `json:"shopId,omitempty"`
	UserID         string   `json:"userId"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Images         []string `json:"images"`
	Liked          int      `json:"liked"`
	Comments       int      `json:"comments"`
	Status         string   `json:"status"`
	PublishVersion int      `json:"publishVersion"`
	CreateTime     string   `json:"createTime"`
	UpdateTime     string   `json:"updateTime"`
}

type CreateRequest struct {
	ShopID  string   `json:"shopId"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Images  []string `json:"images"`
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

func zone() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return location
}

func display(value time.Time) string { return value.In(zone()).Format("2006-01-02 15:04:05") }

// validateImages keeps the list short and the URLs attributable: a note must not
// become a place to inject script URLs or oversized payloads.
func validateImages(images []string) ([]string, error) {
	if len(images) > maxImages {
		return nil, ErrInput
	}
	cleaned := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" || len(image) > maxImageLength ||
			!(strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://")) {
			return nil, ErrInput
		}
		cleaned = append(cleaned, image)
	}
	return cleaned, nil
}

func clean(value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > max || value == "" {
		return "", ErrInput
	}
	return value, nil
}

// Create stores one published note. The shop, when given, must exist; the author
// always comes from the session.
func (service *Service) Create(ctx context.Context, userID string, request CreateRequest) (View, error) {
	user, err := strconv.ParseUint(userID, 10, 64)
	if err != nil || user == 0 {
		return View{}, ErrInput
	}
	title, err := clean(request.Title, maxTitleRunes)
	if err != nil {
		return View{}, err
	}
	content, err := clean(request.Content, maxContentRunes)
	if err != nil {
		return View{}, err
	}
	images, err := validateImages(request.Images)
	if err != nil {
		return View{}, err
	}
	shopID := uint64(0)
	if request.ShopID != "" {
		parsed, err := strconv.ParseUint(request.ShopID, 10, 64)
		if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != request.ShopID {
			return View{}, ErrInput
		}
		var found struct{ ID uint64 }
		if err = service.db.WithContext(ctx).Raw("SELECT id FROM tb_shop WHERE id=?", parsed).Scan(&found).Error; err != nil {
			return View{}, err
		}
		if found.ID == 0 {
			return View{}, ErrNotFound
		}
		shopID = parsed
	}
	result := service.db.WithContext(ctx).Exec(`INSERT INTO tb_blog
		(shop_id,user_id,title,images,content,status,publish_version)
		VALUES (?,?,?,?,?,'PUBLISHED',1)`, shopID, user, title, strings.Join(images, ","), content)
	if result.Error != nil {
		return View{}, result.Error
	}
	var id uint64
	if err = service.db.WithContext(ctx).Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; err != nil || id == 0 {
		return View{}, ErrNotFound
	}
	return service.Detail(ctx, strconv.FormatUint(id, 10))
}

type blogRow struct {
	ID             uint64
	ShopID         uint64
	UserID         uint64
	Title          string
	Images         string
	Content        string
	Liked          uint64
	Comments       uint64
	Status         string
	PublishVersion int
	CreateTime     time.Time
	UpdateTime     time.Time
}

// Detail returns a published note. A missing row and an unpublished one are the
// same 404 so the endpoint cannot be used to probe drafts.
func (service *Service) Detail(ctx context.Context, id string) (View, error) {
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != id {
		return View{}, ErrNotFound
	}
	var rows []blogRow
	if err = service.db.WithContext(ctx).Raw(`SELECT id,shop_id,user_id,title,images,content,
		liked,comments,status,publish_version,create_time,update_time
		FROM tb_blog WHERE id=?`, parsed).Scan(&rows).Error; err != nil {
		return View{}, err
	}
	if len(rows) != 1 || rows[0].Status != "PUBLISHED" {
		return View{}, ErrNotFound
	}
	row := rows[0]
	images := make([]string, 0)
	for _, image := range strings.Split(row.Images, ",") {
		if image = strings.TrimSpace(image); image != "" {
			images = append(images, image)
		}
	}
	view := View{ID: strconv.FormatUint(row.ID, 10), UserID: strconv.FormatUint(row.UserID, 10),
		Title: row.Title, Content: row.Content, Images: images, Liked: int(row.Liked),
		Comments: int(row.Comments), Status: row.Status, PublishVersion: row.PublishVersion,
		CreateTime: display(row.CreateTime), UpdateTime: display(row.UpdateTime)}
	if row.ShopID != 0 {
		shop := strconv.FormatUint(row.ShopID, 10)
		view.ShopID = &shop
	}
	return view, nil
}
