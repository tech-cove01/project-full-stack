// Package merchant resolves current merchant roles and shop access from MySQL.
// Login sessions contain identity only; no authorization decision is cached.
package merchant

import (
	"errors"
	"strconv"

	"gorm.io/gorm"
)

type Role string

const (
	Owner   Role = "OWNER"
	Manager Role = "MANAGER"
	Staff   Role = "STAFF"
)

var (
	ErrForbidden = errors.New("无权操作该门店")
	ErrInvalidID = errors.New("ID必须为正十进制整数字符串")
)

type ShopSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type MerchantSummary struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Role  Role          `json:"role"`
	Shops []ShopSummary `json:"shops"`
}

type Workspace struct {
	Merchants []MerchantSummary `json:"merchants"`
}

// Membership identifies the membership that grants access to one concrete
// shop. A manager role on another merchant never grants this membership.
type Membership struct {
	MemberID   string
	MerchantID string
	Role       Role
}

func (membership Membership) IsManager() bool {
	return membership.Role == Owner || membership.Role == Manager
}

type Service struct{ db *gorm.DB }

func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// Access must be constructed with the same database handle as its caller.
// In a future business transaction use NewAccess(tx), never the global pool.
// Returned scopes describe reads, not reusable authorization tokens. Reads
// inside tx obey its isolation: a repeated plain SELECT under REPEATABLE READ
// may retain an older snapshot. Future mutations must use a current locking
// read or conditional write covering authorization and the concrete resource;
// merely constructing NewAccess(tx) is not a concurrent-revocation barrier.
type Access struct{ db *gorm.DB }

func NewAccess(db *gorm.DB) *Access { return &Access{db: db} }

func parseID(value string) (uint64, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != value {
		return 0, ErrInvalidID
	}
	return id, nil
}

func decimal(id uint64) string { return strconv.FormatUint(id, 10) }
