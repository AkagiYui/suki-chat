package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleAdminListUsers 列出所有用户（管理员）。
func (s *Server) handleAdminListUsers(c *gin.Context) {
	users, err := s.store.Users().List(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// handleAdminListSessions 列出所有用户的所有会话（管理员）。
func (s *Server) handleAdminListSessions(c *gin.Context) {
	sessions, err := s.store.Sessions().ListAll(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}
