package shop

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
)

var (
	errPagination = errors.New("页码必须为正整数，pageSize 必须在 1 到 50 之间")
	errTypeID     = errors.New("分类ID必须为正十进制整数")
	errName       = errors.New("店铺名称必须为不超过 128 个字符的有效文本")
	errNearby     = errors.New("附近坐标查询尚未迁移")
)

// Register keeps every public operation method-scoped. Closed legacy writes
// get 403 on known route shapes; unrelated paths continue through the JSON 404.
func Register(routes gin.IRouter, service *Service) {
	routes.GET("/shop/of/name", listHandler(service, false))
	routes.GET("/shop/of/type", listHandler(service, true))
	routes.GET("/shop/:id", func(c *gin.Context) {
		id, err := positiveInteger(c.Param("id"))
		if err != nil {
			badRequest(c, "店铺ID必须为正十进制整数")
			return
		}
		ctx, cancel := browseContext(c)
		defer cancel()
		shop, err := service.store.Detail(ctx, id)
		if err != nil {
			unavailable(c)
			return
		}
		if shop == nil {
			c.JSON(http.StatusOK, httpapi.Result{ErrorMsg: "店铺不存在！"})
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: shop})
	})
	routes.GET("/shop-type/list", func(c *gin.Context) {
		ctx, cancel := browseContext(c)
		defer cancel()
		types, err := service.store.Types(ctx)
		if err != nil {
			unavailable(c)
			return
		}
		if types == nil {
			types = []ShopType{}
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: types})
	})
	routes.GET("/voucher/list/:shopId", func(c *gin.Context) {
		id, err := positiveInteger(c.Param("shopId"))
		if err != nil {
			badRequest(c, "店铺ID必须为正十进制整数")
			return
		}
		ctx, cancel := browseContext(c)
		defer cancel()
		vouchers, err := service.store.Vouchers(ctx, id)
		if err != nil {
			unavailable(c)
			return
		}
		if vouchers == nil {
			vouchers = []Voucher{}
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: vouchers})
	})
	closedWrite := func(c *gin.Context) {
		c.AbortWithStatusJSON(http.StatusForbidden, httpapi.Result{ErrorMsg: "商家写接口暂未开放"})
	}
	for _, path := range []string{
		"/shop", "/shop/:id", "/shop/of/name", "/shop/of/type", "/shop-type", "/shop-type/list",
		"/voucher", "/voucher/seckill", "/voucher/list/:shopId",
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			routes.Handle(method, path, closedWrite)
		}
	}
}

func listHandler(service *Service, byType bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, err := parseQuery(c, byType)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		ctx, cancel := browseContext(c)
		defer cancel()
		shops, total, err := service.store.List(ctx, query)
		if err != nil {
			unavailable(c)
			return
		}
		if shops == nil {
			shops = []Shop{}
		}
		count := strconv.FormatUint(total, 10)
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: shops, Total: &count})
	}
}

func parseQuery(c *gin.Context, byType bool) (Query, error) {
	query := Query{Name: strings.TrimSpace(c.Query("name")), Current: 1, PageSize: 10}
	if byType {
		query.PageSize = 5
		if _, x := c.GetQueryArray("x"); x {
			return Query{}, errNearby
		}
		if _, y := c.GetQueryArray("y"); y {
			return Query{}, errNearby
		}
	}
	if !utf8.ValidString(query.Name) || utf8.RuneCountInString(query.Name) > 128 {
		return Query{}, errName
	}
	for _, parameter := range []struct {
		name   string
		target *uint64
	}{
		{"current", &query.Current}, {"pageSize", &query.PageSize},
	} {
		if values, present := c.Request.URL.Query()[parameter.name]; present {
			if len(values) != 1 {
				return Query{}, errPagination
			}
			value, err := positiveInteger(values[0])
			if err != nil {
				return Query{}, errPagination
			}
			*parameter.target = value
		}
	}
	if query.PageSize > 50 {
		return Query{}, errPagination
	}
	values, present := c.Request.URL.Query()["typeId"]
	if present {
		if len(values) != 1 {
			return Query{}, errTypeID
		}
		id, err := positiveInteger(values[0])
		if err != nil {
			return Query{}, errTypeID
		}
		query.TypeID = &id
	} else if byType {
		return Query{}, errTypeID
	}
	return query, nil
}

func positiveInteger(value string) (uint64, error) {
	if value == "" {
		return 0, errPagination
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errPagination
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, errPagination
	}
	return parsed, nil
}

func browseContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 5*time.Second)
}

func badRequest(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, httpapi.Result{ErrorMsg: message})
}

func unavailable(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, httpapi.Result{ErrorMsg: "服务暂时无法处理请求，请稍后重试。"})
}
