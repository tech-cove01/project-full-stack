package notesearch

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/gin-gonic/gin"
)

func Register(routes gin.IRouter, service *Service) {
	routes.GET("/notes/search", func(c *gin.Context) {
		request := SearchRequest{Query: c.Query("q"), ShopID: c.Query("shopId"), Cursor: c.Query("cursor"), Sort: c.Query("sort")}
		if values, ok := c.Request.URL.Query()["limit"]; ok {
			number, err := strconv.Atoi(values[0])
			if err != nil || number < 1 || number > 20 {
				c.JSON(http.StatusBadRequest, httpapi.Result{ErrorMsg: ErrInput.Error()})
				return
			}
			request.Limit = number
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		page, err := service.Search(ctx, request)
		if err != nil {
			status := http.StatusServiceUnavailable
			message := ErrUnavailable.Error()
			if errors.Is(err, ErrInput) || errors.Is(err, ErrCursor) {
				status = http.StatusBadRequest
				message = err.Error()
			}
			c.JSON(status, httpapi.Result{ErrorMsg: message})
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: page})
	})
}
