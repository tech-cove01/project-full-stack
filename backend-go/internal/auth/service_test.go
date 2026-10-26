package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

type usersFunc func(context.Context, string, string) (User, error)

func (find usersFunc) FindOrCreate(ctx context.Context, phone, name string) (User, error) {
	return find(ctx, phone, name)
}

type sessionStub struct {
	issue   func(context.Context, string, string) (bool, error)
	consume func(context.Context, string, string) (bool, error)
	create  func(context.Context, string, User) (bool, error)
	refresh func(context.Context, string) (*User, error)
	delete  func(context.Context, string) error
}

func (store sessionStub) IssueCode(ctx context.Context, phone, code string) (bool, error) {
	return store.issue(ctx, phone, code)
}
func (store sessionStub) ConsumeCode(ctx context.Context, phone, code string) (bool, error) {
	return store.consume(ctx, phone, code)
}
func (store sessionStub) CreateSession(ctx context.Context, token string, user User) (bool, error) {
	return store.create(ctx, token, user)
}
func (store sessionStub) RefreshSession(ctx context.Context, token string) (*User, error) {
	return store.refresh(ctx, token)
}
func (store sessionStub) DeleteSession(ctx context.Context, token string) error {
	return store.delete(ctx, token)
}

func TestPhonePrefixesRetainNormalJavaSetWithoutComma(t *testing.T) {
	// Independently enumerate the old valid prefixes, rather than reproducing
	// its regex (whose [0-3,5-9] accidentally admitted a non-digit comma).
	valid := map[int]bool{}
	for _, prefix := range []int{
		130, 131, 132, 133, 134, 135, 136, 137, 138, 139,
		145, 147, 149, 150, 151, 152, 153, 155, 156, 157, 158, 159,
		166, 170, 171, 173, 175, 176, 177, 178,
		180, 181, 182, 183, 184, 185, 186, 187, 188, 189, 198, 199,
	} {
		valid[prefix] = true
	}
	for prefix := 100; prefix < 200; prefix++ {
		if phonePattern.MatchString(fmt.Sprintf("%d00000000", prefix)) != valid[prefix] {
			t.Fatalf("unexpected membership for three-digit prefix %d", prefix)
		}
	}
	for _, invalid := range []string{"15,00000000", "1380000000", "138000000000", " 13800000000", "13800000000\n", "１３８００００００００"} {
		if phonePattern.MatchString(invalid) {
			t.Fatal("noncanonical phone was accepted")
		}
	}
}

func TestSendCodeSeparatesDevelopmentAndDoesNotTouchStoresOnRejectedInput(t *testing.T) {
	for _, environment := range []string{"production", "test"} {
		service := &Service{environment: environment}
		if _, err := service.SendCode(context.Background(), "13800000000"); !errors.Is(err, errSMS) {
			t.Fatal("non-development environment must refuse code issuance without accessing stores")
		}
	}
	service := &Service{environment: "development"}
	if _, err := service.SendCode(context.Background(), "invalid"); !errors.Is(err, errPhone) {
		t.Fatal("invalid phone must fail before accessing stores")
	}
	for _, test := range []struct {
		name    string
		issued  bool
		failure error
		want    error
	}{
		{"issued", true, nil, nil}, {"throttled", false, nil, errThrottled},
		{"dependency", false, errors.New("private driver diagnostic"), errUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service.sessions = sessionStub{issue: func(_ context.Context, phone, code string) (bool, error) {
				if phone != "13800000000" || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
					t.Fatal("issuance did not use the validated phone and six decimal digits")
				}
				return test.issued, test.failure
			}}
			code, err := service.SendCode(context.Background(), "13800000000")
			if !errors.Is(err, test.want) {
				t.Fatal("unexpected code issuance error category")
			}
			if err == nil && (code == nil || code.Mode != "development" || len(code.Code) != 6) {
				t.Fatal("development response did not contain the expected code contract")
			}
		})
	}
}

func TestLoginRejectsBeforeUserCreationAndHidesDependencyErrors(t *testing.T) {
	form := LoginForm{Phone: "13800000000", Code: "123456"}
	for _, test := range []struct {
		name    string
		form    LoginForm
		consume func(context.Context, string, string) (bool, error)
		want    error
	}{
		{"phone", LoginForm{Phone: "bad", Code: form.Code}, nil, errPhone},
		{"code_format", LoginForm{Phone: form.Phone, Code: "12345"}, nil, errCode},
		{"mismatch", form, func(context.Context, string, string) (bool, error) { return false, nil }, errCode},
		{"redis_error", form, func(context.Context, string, string) (bool, error) {
			return false, errors.New("private Redis diagnostic")
		}, errUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{sessions: sessionStub{consume: test.consume}}
			if _, err := service.Login(context.Background(), test.form); !errors.Is(err, test.want) {
				t.Fatal("login did not reject before user creation with the expected public error")
			}
		})
	}
	service := &Service{
		users: usersFunc(func(context.Context, string, string) (User, error) {
			return User{}, errors.New("private SQL credential and query")
		}),
		sessions: sessionStub{consume: func(context.Context, string, string) (bool, error) { return true, nil }},
	}
	if _, err := service.Login(context.Background(), form); !errors.Is(err, errUnavailable) {
		t.Fatal("SQL errors must be summarized without driver details")
	}
}

func TestLoginUsesDatabaseIdentityAndRetriesSessionCollisionWithoutOverwriting(t *testing.T) {
	identity := User{ID: "9007199254740993", NickName: "existing profile", Icon: ""}
	creates := 0
	service := &Service{
		users: usersFunc(func(_ context.Context, phone, name string) (User, error) {
			if phone != "13800000000" || !regexp.MustCompile(`^user_[a-f0-9]{10}$`).MatchString(name) {
				t.Fatal("new-user defaults did not match the nickname contract")
			}
			return identity, nil
		}),
		sessions: sessionStub{
			consume: func(context.Context, string, string) (bool, error) { return true, nil },
			create: func(_ context.Context, token string, user User) (bool, error) {
				creates++
				if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(token) || user != identity {
					t.Fatal("session did not receive a cryptographic token and database identity")
				}
				return creates > 1, nil
			},
		},
	}
	if token, err := service.Login(context.Background(), LoginForm{Phone: "13800000000", Code: "123456"}); err != nil || len(token) != 32 || creates != 2 {
		t.Fatal("session collision must cause a fresh token attempt")
	}
	creates = 0
	store := service.sessions.(sessionStub)
	store.create = func(context.Context, string, User) (bool, error) { creates++; return false, nil }
	service.sessions = store
	if _, err := service.Login(context.Background(), LoginForm{Phone: "13800000000", Code: "123456"}); !errors.Is(err, errUnavailable) || creates != 3 {
		t.Fatal("session collision retries must be bounded")
	}
}

func TestResolveRejectsMalformedHeadersBeforeRedisAndSummarizesErrors(t *testing.T) {
	service := &Service{}
	for _, malformed := range []string{"", "short", strings.Repeat("g", 32), strings.Repeat("a", 33), "Bearer " + strings.Repeat("a", 32)} {
		if _, err := service.Resolve(context.Background(), malformed); !errors.Is(err, errUnauthorized) {
			t.Fatal("malformed token must be rejected before Redis")
		}
	}
	service.sessions = sessionStub{refresh: func(context.Context, string) (*User, error) { return nil, errors.New("private Redis diagnostic") }}
	if _, err := service.Resolve(context.Background(), strings.Repeat("a", 32)); !errors.Is(err, errUnavailable) {
		t.Fatal("Redis errors must not be mistaken for missing login or expose diagnostics")
	}
}
