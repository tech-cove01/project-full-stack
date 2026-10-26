package order

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/auth"
	"github.com/cityloop/fullstack-go/internal/orderadmission"
	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

func Register(routes gin.IRouter, service *Service, identity *auth.Service) {
	routes.POST("/voucher-order/seckill/:id", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		id, err := service.Seckill(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: id})
	})
	routes.GET("/voucher-order/:id", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Get(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
	routes.GET("/voucher-order", auth.RequireLogin(identity), func(c *gin.Context) {
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
	routes.POST("/voucher-order/:id/pay", auth.RequireLogin(identity), func(c *gin.Context) {
		user, _ := auth.CurrentUser(c)
		ctx, cancel := requestContext(c)
		defer cancel()
		view, err := service.Pay(ctx, user.ID, c.Param("id"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: view})
	})
}

func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 5*time.Second)
}

func parseQuery(c *gin.Context) (Query, error) {
	query := Query{Current: 1, PageSize: 10}
	for _, field := range []struct {
		name   string
		target *uint64
	}{{"current", &query.Current}, {"pageSize", &query.PageSize}} {
		if values, present := c.Request.URL.Query()[field.name]; present {
			if len(values) != 1 {
				return query, ErrPagination
			}
			value, err := unsignedID(values[0])
			if err != nil {
				return query, ErrPagination
			}
			*field.target = value
		}
	}
	if query.PageSize > 50 {
		return query, ErrPagination
	}
	return query, nil
}

func writeError(c *gin.Context, err error) {
	status, message := http.StatusServiceUnavailable, "服务暂时无法处理请求，请稍后重试。"
	switch {
	case errors.Is(err, ErrInvalidID), errors.Is(err, ErrPagination):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrNotFound):
		status, message = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrConflict):
		status, message = http.StatusConflict, ErrConflict.Error()
	case errors.Is(err, ErrActivity), errors.Is(err, orderadmission.ErrSoldOut), errors.Is(err, orderadmission.ErrAlreadyPurchased),
		errors.Is(err, orderadmission.ErrNotStarted), errors.Is(err, orderadmission.ErrEnded), errors.Is(err, orderadmission.ErrClosed):
		status, message = http.StatusOK, err.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
