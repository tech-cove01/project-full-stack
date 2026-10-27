// Package auth owns the existing phone-code login and Redis session contract.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	CodeTTL     = 2 * time.Minute
	ThrottleTTL = time.Minute
	SessionTTL  = 25 * 24 * time.Hour
)

var (
	// Retain the Java phone-prefix set, fixing its accidental comma character.
	phonePattern = regexp.MustCompile(`^1(?:[38][0-9]|4[579]|5[0-35-9]|66|7[0135678]|9[89])[0-9]{8}$`)
	codePattern  = regexp.MustCompile(`^[0-9]{6}$`)
	tokenPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

	errPhone        = errors.New("手机号格式错误！")
	errSMS          = errors.New("短信服务尚未接入，请在本地 dev 环境体验")
	errThrottled    = errors.New("验证码发送过于频繁，请稍后重试")
	errCode         = errors.New("验证码错误")
	errUnauthorized = errors.New("请先登录")
	errUnavailable  = errors.New("服务暂时无法处理请求，请稍后重试。")
)

// User deliberately excludes phone, password and merchant roles. Request
// handlers receive a value copy; identities never come from request JSON.
type User struct {
	ID       string `json:"id"`
	NickName string `json:"nickName"`
	Icon     string `json:"icon"`
}

type DevelopmentCode struct {
	Code string `json:"code"`
	Mode string `json:"mode"`
}

type LoginForm struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

type userStore interface {
	FindOrCreate(context.Context, string, string) (User, error)
}

type sessionStore interface {
	IssueCode(context.Context, string, string) (bool, error)
	ConsumeCode(context.Context, string, string) (bool, error)
	CreateSession(context.Context, string, User) (bool, error)
	RefreshSession(context.Context, string) (*User, error)
	DeleteSession(context.Context, string) error
}

type Service struct {
	users       userStore
	sessions    sessionStore
	environment string
}

// NewService shares the application-owned pools; it neither migrates schema
// nor starts background work. database.Open supplies the configured real pools.
func NewService(db *gorm.DB, cache redis.UniversalClient, environment string) *Service {
	return &Service{users: mysqlUsers{db: db}, sessions: redisSessions{client: cache}, environment: environment}
}

func (service *Service) SendCode(ctx context.Context, phone string) (*DevelopmentCode, error) {
	if !phonePattern.MatchString(phone) {
		return nil, errPhone
	}
	// A real SMS provider is not implemented; production/test never issue codes.
	if service.environment != "development" {
		return nil, errSMS
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return nil, errUnavailable
	}
	code := fmt.Sprintf("%06d", number.Int64())
	issued, err := service.sessions.IssueCode(ctx, phone, code)
	if err != nil {
		return nil, errUnavailable
	}
	if !issued {
		return nil, errThrottled
	}
	return &DevelopmentCode{Code: code, Mode: "development"}, nil
}

func (service *Service) Login(ctx context.Context, form LoginForm) (string, error) {
	if !phonePattern.MatchString(form.Phone) {
		return "", errPhone
	}
	if !codePattern.MatchString(form.Code) {
		return "", errCode
	}
	consumed, err := service.sessions.ConsumeCode(ctx, form.Phone, form.Code)
	if err != nil {
		return "", errUnavailable
	}
	if !consumed {
		return "", errCode
	}
	suffix, err := randomHex(5)
	if err != nil {
		return "", errUnavailable
	}
	user, err := service.users.FindOrCreate(ctx, form.Phone, "user_"+suffix)
	if err != nil {
		return "", errUnavailable
	}
	// Never overwrite an existing session even in the event of a random collision.
	for attempt := 0; attempt < 3; attempt++ {
		token, err := randomHex(16)
		if err != nil {
			return "", errUnavailable
		}
		created, err := service.sessions.CreateSession(ctx, token, user)
		if err != nil {
			return "", errUnavailable
		}
		if created {
			return token, nil
		}
	}
	return "", errUnavailable
}

func (service *Service) Resolve(ctx context.Context, token string) (*User, error) {
	if !tokenPattern.MatchString(token) {
		return nil, errUnauthorized
	}
	user, err := service.sessions.RefreshSession(ctx, token)
	if err != nil {
		return nil, errUnavailable
	}
	if user == nil {
		return nil, errUnauthorized
	}
	return user, nil
}

func (service *Service) Logout(ctx context.Context, token string) error {
	if !tokenPattern.MatchString(token) {
		return errUnauthorized
	}
	if err := service.sessions.DeleteSession(ctx, token); err != nil {
		return errUnavailable
	}
	return nil
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
