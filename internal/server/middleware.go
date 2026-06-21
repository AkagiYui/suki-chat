package server

import (
	"net/http"
	"strings"

	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/gin-gonic/gin"
)

const (
	ctxUserID = "uid"
	ctxRole   = "role"
)

// authRequired 校验 JWT。令牌可来自 Authorization 头或 query 参数 token
// （SSE 的 EventSource 无法设置请求头，故支持 query 传递）。
func (s *Server) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "缺少令牌"})
			return
		}
		claims, err := s.tokens.Parse(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "令牌无效或已过期"})
			return
		}
		c.Set(ctxUserID, claims.UserID)
		c.Set(ctxRole, claims.Role)
		c.Next()
	}
}

// adminRequired 要求管理员角色。
func (s *Server) adminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(ctxRole) != string(store.RoleAdmin) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
			return
		}
		c.Next()
	}
}

func bearerToken(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return c.Query("token")
}

func currentUserID(c *gin.Context) string { return c.GetString(ctxUserID) }
