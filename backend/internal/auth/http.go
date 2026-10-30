package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cityloop/fullstack-go/internal/platform/httpapi"
	"github.com/cityloop/fullstack-go/internal/platform/taildiag"
)

const identityKey = "cityloop.auth.user"

// Register exposes only migrated user operations. Unknown legacy routes remain
// 404; later modules explicitly attach RequireLogin to protected routes.
func Register(routes gin.IRouter, service *Service) {
	routes.POST("/user/code", func(c *gin.Context) {
		ctx, cancel := requestContext(c)
		defer cancel()
		code, err := service.SendCode(ctx, c.Query("phone"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: code})
	})
	routes.POST("/user/login", func(c *gin.Context) {
		var form LoginForm
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
		if err := decoder.Decode(&form); err != nil {
			c.JSON(http.StatusBadRequest, httpapi.Result{ErrorMsg: "请求参数格式错误"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			c.JSON(http.StatusBadRequest, httpapi.Result{ErrorMsg: "请求参数格式错误"})
			return
		}
		ctx, cancel := requestContext(c)
		defer cancel()
		token, err := service.Login(ctx, form)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: token})
	})
	routes.GET("/user/me", RequireLogin(service), func(c *gin.Context) {
		user, _ := CurrentUser(c)
		c.JSON(http.StatusOK, httpapi.Result{Success: true, Data: user})
	})
	routes.POST("/user/logout", RequireLogin(service), func(c *gin.Context) {
		ctx, cancel := requestContext(c)
		defer cancel()
		if err := service.Logout(ctx, headerToken(c.GetHeader("authorization"))); err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, httpapi.Result{Success: true})
	})
}

// RequireLogin is method/route scoped rather than a path-prefix policy. Neither
// unsupported routes nor request JSON can manufacture an authenticated user.
func RequireLogin(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := requestContext(c)
		span := taildiag.Begin(ctx, taildiag.AuthResolve, 0)
		user, err := service.Resolve(ctx, headerToken(c.GetHeader("authorization")))
		span.End(err != nil)
		cancel()
		if err != nil {
			writeError(c, err)
			return
		}
		c.Set(identityKey, *user)
		c.Next()
	}
}

func CurrentUser(c *gin.Context) (User, bool) {
	value, present := c.Get(identityKey)
	if !present {
		return User{}, false
	}
	user, valid := value.(User)
	return user, valid
}

func requestContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 5*time.Second)
}

func headerToken(value string) string { return strings.TrimPrefix(value, "Bearer ") }

func writeError(c *gin.Context, err error) {
	status := http.StatusOK
	message := err.Error()
	switch {
	case errors.Is(err, errUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, errPhone), errors.Is(err, errSMS), errors.Is(err, errThrottled), errors.Is(err, errCode):
	default:
		status, message = http.StatusServiceUnavailable, errUnavailable.Error()
	}
	c.AbortWithStatusJSON(status, httpapi.Result{ErrorMsg: message})
}
