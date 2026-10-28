package aiknowledge

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/merchant"
)

var (
	ErrForbidden = errors.New("无权管理该门店知识")
	ErrNotFound  = errors.New("知识不存在")
	ErrConflict  = errors.New("知识当前状态不允许此操作")
	ErrUnsafe    = errors.New("知识索引状态需要核对，请稍后重试")
	ErrInput     = errors.New("标题或正文为空、过长或格式错误")
)

const (
	maxTitleRunes = 255
	maxBodyRunes  = 8000
	scopeShop     = "SHOP_PUBLIC"
	zoneName      = "Asia/Shanghai"
	// minScore keeps weak lexical overlap out of the retrieval result instead of
	// padding the context with unrelated rules.
	minScore = 0.18
)

type View struct {
	ID               string  `json:"id"`
	Scope            string  `json:"scope"`
	ShopID           *string `json:"shopId,omitempty"`
	Title            string  `json:"title"`
	Body             string  `json:"body"`
	Status           string  `json:"status"`
	PublishedVersion *int    `json:"publishedVersion,omitempty"`
	Epoch            int64   `json:"epoch"`
	Disabled         bool    `json:"disabled"`
	UpdateTime       string  `json:"updateTime"`
}

// ListItem omits the body: a merchant list is a navigation surface, and the
// full text is only needed when editing one document.
type ListItem struct {
	ID               string  `json:"id"`
	Body             string  `json:"body"`
	Scope            string  `json:"scope"`
	ShopID           *string `json:"shopId,omitempty"`
	Title            string  `json:"title"`
	Status           string  `json:"status"`
	PublishedVersion *int    `json:"publishedVersion,omitempty"`
	Epoch            int64   `json:"epoch"`
	Disabled         bool    `json:"disabled"`
	UpdateTime       string  `json:"updateTime"`
}

// PublicView is what a user-facing citation can quote: one immutable published
// version with the epoch needed to detect a later replacement.
type PublicView struct {
	ID         string `json:"id"`
	DocumentID string `json:"documentId"`
	Scope      string `json:"scope"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Version    int    `json:"version"`
	Epoch      int64  `json:"epoch"`
	UpdateTime string `json:"updateTime"`
}

type Service struct {
	db       *gorm.DB
	provider ai.Provider
	topK     int
	minScore float64
}

func NewService(db *gorm.DB, provider ai.Provider) *Service {
	if provider == nil {
		provider = ai.NewStub(256)
	}
	return &Service{db: db, provider: provider, topK: 5, minScore: minScore}
}

// WithRetrieval controls the public search defaults without changing public
// document visibility or allowing request input to lower the score threshold.
func (service *Service) WithRetrieval(topK int, score float64) *Service {
	if topK >= 1 && topK <= 20 {
		service.topK = topK
	}
	if score >= 0 && score <= 1 {
		service.minScore = score
	}
	return service
}

type documentRow struct {
	ID               uint64
	Scope            string
	ShopID           *uint64
	Title            string
	Body             string
	Status           string
	PublishedVersion *int
	Epoch            int64
	Disabled         bool
	UpdateTime       time.Time
}

const documentColumns = `id,scope,shop_id,title,body,status,published_version,epoch,disabled,update_time`

func zone() *time.Location {
	location, err := time.LoadLocation(zoneName)
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return location
}

func decimal(id uint64) string { return strconv.FormatUint(id, 10) }

func positive(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value {
		return 0, ErrInput
	}
	return parsed, nil
}

func cleanText(value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || utf8.RuneCountInString(value) > max || value == "" || ai.SensitiveInput(value) {
		return "", ErrInput
	}
	return value, nil
}

func toView(row documentRow) View {
	view := View{ID: decimal(row.ID), Scope: row.Scope, Title: row.Title, Body: row.Body,
		Status: row.Status, PublishedVersion: row.PublishedVersion, Epoch: row.Epoch,
		Disabled: row.Disabled, UpdateTime: row.UpdateTime.In(zone()).Format("2006-01-02 15:04:05")}
	if row.ShopID != nil {
		shop := decimal(*row.ShopID)
		view.ShopID = &shop
	}
	return view
}

func (service *Service) view(ctx context.Context, id uint64) (View, error) {
	var rows []documentRow
	if err := service.db.WithContext(ctx).Raw("SELECT "+documentColumns+
		" FROM tb_ai_document WHERE id=?", id).Scan(&rows).Error; err != nil {
		return View{}, err
	}
	if len(rows) != 1 {
		return View{}, ErrNotFound
	}
	return toView(rows[0]), nil
}

// authorize checks the current membership of the document's real shop. The shop
// is never taken from the request.
func (service *Service) authorize(ctx context.Context, db *gorm.DB, userID string, id uint64) (documentRow, merchant.Membership, error) {
	var rows []documentRow
	if err := db.WithContext(ctx).Raw("SELECT "+documentColumns+
		" FROM tb_ai_document WHERE id=? FOR UPDATE", id).Scan(&rows).Error; err != nil {
		return documentRow{}, merchant.Membership{}, err
	}
	if len(rows) != 1 {
		return documentRow{}, merchant.Membership{}, ErrNotFound
	}
	row := rows[0]
	if row.Scope != scopeShop || row.ShopID == nil {
		return documentRow{}, merchant.Membership{}, ErrForbidden
	}
	merchantID, err := service.manageShop(ctx, db, userID, decimal(*row.ShopID))
	if err != nil {
		if errors.Is(err, merchant.ErrForbidden) {
			return documentRow{}, merchant.Membership{}, ErrForbidden
		}
		return documentRow{}, merchant.Membership{}, err
	}
	return row, merchant.Membership{MerchantID: merchantID}, nil
}

func (service *Service) CreateDraft(ctx context.Context, userID, shopID, title, body string) (View, error) {
	shop, err := positive(shopID)
	if err != nil {
		return View{}, err
	}
	title, err = cleanText(title, maxTitleRunes)
	if err != nil {
		return View{}, err
	}
	body, err = cleanText(body, maxBodyRunes)
	if err != nil {
		return View{}, err
	}
	user, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	if _, err = merchant.NewAccess(service.db).RequireManager(ctx, userID, decimal(shop)); err != nil {
		if errors.Is(err, merchant.ErrForbidden) {
			return View{}, ErrForbidden
		}
		return View{}, err
	}
	var id uint64
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := service.manageShop(ctx, tx, userID, shopID); err != nil {
			return err
		}
		result := tx.Exec(`INSERT INTO tb_ai_document
			(scope,shop_id,title,body,status,epoch,created_by,updated_by)
			VALUES (?,?,?,?,'DRAFT',0,?,?)`, scopeShop, shop, title, body, user, user)
		if result.Error != nil {
			return result.Error
		}
		var row struct{ ID uint64 }
		if err := tx.Raw("SELECT LAST_INSERT_ID() AS id").Scan(&row).Error; err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

func (service *Service) UpdateDraft(ctx context.Context, userID, documentID, title, body string) (View, error) {
	id, err := positive(documentID)
	if err != nil {
		return View{}, err
	}
	title, err = cleanText(title, maxTitleRunes)
	if err != nil {
		return View{}, err
	}
	body, err = cleanText(body, maxBodyRunes)
	if err != nil {
		return View{}, err
	}
	user, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, membership, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Disabled {
			return ErrConflict
		}
		if row.Title == title && row.Body == body {
			return nil
		}
		result := tx.Exec(`UPDATE tb_ai_document SET title=?,body=?,updated_by=? WHERE id=?`,
			title, body, user, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnsafe
		}
		return audit(ctx, tx, user, id, decimal(*row.ShopID), membership.MerchantID, "KNOWLEDGE_UPDATE", 0)
	}, nil)
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

func (service *Service) Disable(ctx context.Context, userID, documentID string) (View, error) {
	id, err := positive(documentID)
	if err != nil {
		return View{}, err
	}
	user, err := positive(userID)
	if err != nil {
		return View{}, err
	}
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, membership, err := service.authorize(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		if row.Disabled {
			return nil
		}
		result := tx.Exec(`UPDATE tb_ai_document SET disabled=1,status='DISABLED',epoch=epoch+1,updated_by=?
			WHERE id=? AND disabled=0`, user, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUnsafe
		}
		version := 0
		if row.PublishedVersion != nil {
			version = *row.PublishedVersion
		}
		return audit(ctx, tx, user, id, decimal(*row.ShopID), membership.MerchantID, "KNOWLEDGE_DISABLE", version)
	}, nil)
	if err != nil {
		return View{}, err
	}
	return service.view(ctx, id)
}

func (service *Service) List(ctx context.Context, userID, shopID string) ([]ListItem, error) {
	shop, err := positive(shopID)
	if err != nil {
		return nil, err
	}
	membership, err := merchant.NewAccess(service.db).MembershipForShop(ctx, userID, decimal(shop))
	if err != nil {
		if errors.Is(err, merchant.ErrForbidden) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	rows := make([]documentRow, 0)
	query := "SELECT " + documentColumns + " FROM tb_ai_document WHERE scope=? AND shop_id=? ORDER BY id DESC"
	if !membership.IsManager() {
		query = `SELECT d.id,d.scope,d.shop_id,v.title,v.body,d.status,d.published_version,d.epoch,d.disabled,d.update_time
 FROM tb_ai_document d JOIN tb_ai_document_version v ON v.document_id=d.id AND v.version=d.published_version
 WHERE d.scope=? AND d.shop_id=? AND d.disabled=0 AND d.status='PUBLISHED'
 AND v.status='READY' AND v.epoch=d.epoch AND v.title IS NOT NULL ORDER BY d.id DESC`
	}
	if err = service.db.WithContext(ctx).Raw(query, scopeShop, shop).Scan(&rows).Error; err != nil {
		return nil, err
	}
	items := make([]ListItem, 0, len(rows))
	for _, row := range rows {
		var shopText *string
		if row.ShopID != nil {
			value := decimal(*row.ShopID)
			shopText = &value
		}
		items = append(items, ListItem{ID: decimal(row.ID), Body: row.Body, Scope: row.Scope, ShopID: shopText, Title: row.Title, Status: row.Status,
			PublishedVersion: row.PublishedVersion, Epoch: row.Epoch, Disabled: row.Disabled,
			UpdateTime: row.UpdateTime.In(zone()).Format("2006-01-02 15:04:05")})
	}
	return items, nil
}

// Public returns a published, non-disabled version. An unknown document, a
// disabled one and a draft all look the same to an anonymous caller.
func (service *Service) Public(ctx context.Context, documentID string, version *int) (PublicView, error) {
	id, err := positive(documentID)
	if err != nil {
		return PublicView{}, ErrNotFound
	}
	var rows []struct {
		ID               uint64
		Scope            string
		Title            string
		PublishedVersion int
		Epoch            int64
		Body             string
		CreateTime       time.Time
	}
	target := "d.published_version"
	args := []any{id}
	if version != nil {
		target = "?"
		args = append([]any{*version}, args...)
	}
	if err = service.db.WithContext(ctx).Raw(`SELECT d.id,d.scope,v.title,v.version AS published_version,
		v.epoch,v.body,v.create_time
		FROM tb_ai_document d
		JOIN tb_ai_document_version v ON v.document_id=d.id AND v.version=`+target+`
		WHERE d.id=? AND d.disabled=0 AND d.status='PUBLISHED' AND d.published_version=v.version
		  AND d.epoch=v.epoch AND d.scope IN ('PUBLIC','SHOP_PUBLIC') AND v.status='READY' AND v.title IS NOT NULL`, args...).Scan(&rows).Error; err != nil {
		return PublicView{}, err
	}
	if len(rows) != 1 {
		return PublicView{}, ErrNotFound
	}
	row := rows[0]
	return PublicView{ID: decimal(row.ID), DocumentID: decimal(row.ID), Scope: row.Scope, Title: row.Title, Body: row.Body,
		Version: row.PublishedVersion, Epoch: row.Epoch,
		UpdateTime: row.CreateTime.In(zone()).Format("2006-01-02 15:04:05")}, nil
}
