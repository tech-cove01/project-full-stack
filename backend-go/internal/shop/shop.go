// Package shop provides the public, read-only shop/category/activity browser.
// The first Go browsing slice reads MySQL directly; it has no shared caches.
package shop

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Shop retains the Java public DTO. Average price is whole yuan; unlike
// voucher amounts it must not be divided by 100 by the frontend.
type Shop struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	TypeID     string  `json:"typeId"`
	Images     string  `json:"images"`
	Area       *string `json:"area,omitempty"`
	Address    string  `json:"address"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	AvgPrice   *string `json:"avgPrice,omitempty"`
	Sold       uint32  `json:"sold"`
	Comments   uint32  `json:"comments"`
	Score      uint32  `json:"score"`
	OpenHours  *string `json:"openHours,omitempty"`
	CreateTime *string `json:"createTime,omitempty"`
	UpdateTime *string `json:"updateTime,omitempty"`
}

type ShopType struct {
	ID   string  `json:"id"`
	Name *string `json:"name,omitempty"`
	Icon *string `json:"icon,omitempty"`
	Sort int32   `json:"sort"`
}

// Voucher amounts are decimal strings of cents. Only selected public columns
// are exposed, never merchant publication internals or redemption credentials.
type Voucher struct {
	ID          string  `json:"id"`
	ShopID      string  `json:"shopId"`
	Title       string  `json:"title"`
	SubTitle    *string `json:"subTitle,omitempty"`
	Rules       *string `json:"rules,omitempty"`
	PayValue    string  `json:"payValue"`
	ActualValue string  `json:"actualValue"`
	Type        uint8   `json:"type"`
	Stock       int32   `json:"stock"`
	BeginTime   *string `json:"beginTime,omitempty"`
	EndTime     *string `json:"endTime,omitempty"`
}

type Query struct {
	Name     string
	TypeID   *uint64
	Current  uint64
	PageSize uint64
}

type repository interface {
	List(context.Context, Query) ([]Shop, uint64, error)
	Detail(context.Context, uint64) (*Shop, error)
	Types(context.Context) ([]ShopType, error)
	Vouchers(context.Context, uint64) ([]Voucher, error)
}

type Service struct{ store repository }

// NewService shares the existing application pool and configured Shanghai
// location. It never mutates schema, warms Redis, or starts background work.
func NewService(db *gorm.DB, location *time.Location) *Service {
	if location == nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return &Service{store: mysqlStore{db: db, location: location}}
}
