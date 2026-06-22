package server

import (
	"net/http"
	"strings"

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

// handleAdminListContainers 列出所有受控容器（仅 suki.managed=true，基础设施不在内），
// 让管理员看到每个用户当前有哪些活跃容器。
func (s *Server) handleAdminListContainers(c *gin.Context) {
	containers, err := s.sessions.ListManagedContainers(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(containers))
	for _, ct := range containers {
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		id := ct.ID
		if len(id) > 12 {
			id = id[:12]
		}
		out = append(out, gin.H{
			"id":          id,
			"name":        name,
			"user":        ct.Labels["suki.user"],
			"session":     ct.Labels["suki.session"],
			"kind":        ct.Labels["suki.kind"],
			"state":       ct.State,
			"createdUnix": ct.CreatedUnix,
		})
	}
	c.JSON(http.StatusOK, gin.H{"containers": out})
}
