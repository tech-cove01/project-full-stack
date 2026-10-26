package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/activitypub"
	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.GET("/merchant/activities", auth.RequireLogin(identity), func(c *gin.Context) {
		query, err := parseQuery(c)
		if err != nil {
			writeError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		items, total, err := service.List(ctx, user.ID, query)
		if err != nil {
			writeError(c, err)
			return
		}
		count := strconv.FormatUint(total, 10)
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: items, Total: &count})
	})
	routes.POST("/merchant/activities", auth.RequireLogin(identity), func(c *gin.Context) {
		request, err := decodeCreate(c.Writer, c.Request)
		if err != nil {
			writeError(c, err)
			return
		}
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		result, err := service.Create(ctx, user.ID, c.GetHeader("Idempotency-Key"), request)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: result})
	})
	mutate := func(offline bool) gin.HandlerFunc {
		return func(c *gin.Context) {
			user, _ := auth.CurrentUser(c)
			ctx, cancel := requestContext(c)
			defer cancel()
			var view View
			var err error
			if offline {
				view, err = service.Offline(ctx, user.ID, c.Param("id"))
			} else {
				view, err = service.Retry(ctx, user.ID, c.Param("id"))
			}
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
		}
	}
	routes.POST("/merchant/activities/:id/retry-publication", auth.RequireLogin(identity), mutate(false))
	routes.POST("/merchant/activities/:id/offline", auth.RequireLogin(identity), mutate(true))
}

func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 5*time.Second)
}

func decodeCreate(writer http.ResponseWriter, request *http.Request) (CreateRequest, error) {
	var form CreateRequest
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 16384))
	if err != nil || !utf8.Valid(body) {
		return form, invalid("发布参数格式错误")
	}
	// encoding/json accepts duplicate names and case-insensitive struct fields.
	// An exact whitelist keeps the signed payload unambiguous at the boundary.
	fields := map[string]bool{"shopId": true, "title": true, "subTitle": true, "rules": true,
		"payValue": true, "actualValue": true, "totalStock": true, "beginTime": true,
		"endTime": true, "redeemBeginTime": true, "redeemEndTime": true}
	seen := make(map[string]bool)
	check := json.NewDecoder(bytes.NewReader(body))
	opening, err := check.Token()
	if err != nil || opening != json.Delim('{') {
		return form, invalid("发布参数必须是JSON对象")
	}
	for check.More() {
		token, err := check.Token()
		if err != nil {
			return form, invalid("发布参数格式错误")
		}
		key, ok := token.(string)
		if !ok || !fields[key] || seen[key] {
			return form, invalid("发布参数包含重复或不允许的字段")
		}
		seen[key] = true
		var value json.RawMessage
		if check.Decode(&value) != nil {
			return form, invalid("发布参数格式错误")
		}
	}
	if closing, err := check.Token(); err != nil || closing != json.Delim('}') {
		return form, invalid("发布参数格式错误")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&form); err != nil {
		return form, invalid("发布参数格式错误或包含不允许的字段")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return form, invalid("发布参数格式错误")
	}
	return form, nil
}

func parseQuery(c *gin.Context) (Query, error) {
	q := Query{Current: 1, PageSize: 10}
	values := c.Request.URL.Query()
	for _, param := range []struct {
		name   string
		target *uint64
	}{{"current", &q.Current}, {"pageSize", &q.PageSize}} {
		if list, present := values[param.name]; present {
			if len(list) != 1 {
				return q, invalid("分页参数格式错误")
			}
			n, err := positiveID(list[0])
			if err != nil {
				return q, err
			}
			*param.target = n
		}
	}
	for _, param := range []struct {
		name   string
		target *string
	}{{"shopId", &q.ShopID}, {"status", &q.Status}} {
		if list, present := values[param.name]; present {
			if len(list) != 1 || list[0] == "" {
				return q, invalid("筛选参数格式错误")
			}
			*param.target = list[0]
		}
	}
	return q, validateQuery(q)
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	var validation validationError
	switch {
	case errors.As(err, &validation):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrConflict), errors.Is(err, ErrOffline):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, ErrUnsafe), errors.Is(err, activitypub.ErrUnsafe), errors.Is(err, activitypub.ErrStale):
		message = ErrUnsafe.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
