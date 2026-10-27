package merchantorder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/merchant"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

// Register exposes the read-only merchant order and audit lists. Redeem and
// refund are registered separately so each mutation keeps its own review unit.
func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.GET("/merchant/orders", auth.RequireLogin(identity), func(c *gin.Context) {
		query, err := parseListQuery(c)
		if err != nil {
			writeError(c, err)
			return
		}
		user, valid := auth.CurrentUser(c)
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, httpapi.Result{ErrorMsg: "请先登录"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		items, total, err := service.List(ctx, user.ID, query)
		if err != nil {
			writeError(c, err)
			return
		}
		count := strconv.FormatUint(total, 10)
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: items, Total: &count})
	})
	routes.GET("/merchant/audits", auth.RequireLogin(identity), func(c *gin.Context) {
		query, err := parseListQuery(c)
		if err != nil {
			writeError(c, err)
			return
		}
		user, valid := auth.CurrentUser(c)
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, httpapi.Result{ErrorMsg: "请先登录"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		items, total, err := service.Audits(ctx, user.ID, query)
		if err != nil {
			writeError(c, err)
			return
		}
		count := strconv.FormatUint(total, 10)
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: items, Total: &count})
	})
	// The redeem token is a bearer value, but the membership is still checked in
	// the service: holding a token never substitutes for authorization.
	routes.POST("/merchant/orders/redeem", auth.RequireLogin(identity), func(c *gin.Context) {
		var body struct {
			ShopID string `json:"shopId"`
			Token  string `json:"token"`
		}
		if err := decodeBody(c, &body); err != nil {
			writeError(c, err)
			return
		}
		user, valid := auth.CurrentUser(c)
		if !valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, httpapi.Result{ErrorMsg: "请先登录"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		result, err := service.Redeem(ctx, user.ID, body.ShopID, body.Token)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: result})
	})
}

// decodeBody caps the payload and rejects unknown fields: the request DTO is a
// whitelist, never a way to inject operator, role or state.
func decodeBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidQuery
	}
	return nil
}

// parseListQuery rejects duplicated or non-numeric pagination instead of
// guessing which value the caller meant.
func parseListQuery(c *gin.Context) (Query, error) {
	query := Query{Current: 1, PageSize: 10}
	values := c.Request.URL.Query()
	for _, field := range []struct {
		name   string
		target *uint64
	}{{"current", &query.Current}, {"pageSize", &query.PageSize}} {
		if raw, present := values[field.name]; present {
			if len(raw) != 1 {
				return query, ErrInvalidQuery
			}
			parsed, err := positive(raw[0])
			if err != nil {
				return query, ErrInvalidQuery
			}
			*field.target = parsed
		}
	}
	for _, field := range []struct {
		name   string
		target *string
	}{{"shopId", &query.ShopID}, {"status", &query.Status},
		{"orderId", &query.OrderID}, {"action", &query.Action}} {
		if raw, present := values[field.name]; present {
			if len(raw) != 1 {
				return query, ErrInvalidQuery
			}
			*field.target = raw[0]
		}
	}
	return query, nil
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	switch {
	case errors.Is(err, ErrInvalidQuery):
		status, message = http.StatusBadRequest, ErrInvalidQuery.Error()
	case errors.Is(err, merchant.ErrForbidden), errors.Is(err, ErrForbidden):
		status, message = http.StatusForbidden, ErrForbidden.Error()
	case errors.Is(err, ErrToken):
		status, message = http.StatusNotFound, ErrToken.Error()
	case errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, ErrConflict.Error()
	case errors.Is(err, ErrUnsafe):
		status, message = http.StatusServiceUnavailable, ErrUnsafe.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
