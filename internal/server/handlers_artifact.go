package server

import (
	"net/http"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

// 工件文件名白名单：仅允许字母数字与 . _ -，杜绝路径穿越。
var artifactNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// handleArtifact 提供会话工件（如截图 PNG）。归属校验同会话（管理员可访问任意）。
// 令牌可走 query 参数，便于 <img src> 直接加载。
func (s *Server) handleArtifact(c *gin.Context) {
	sess, ok := s.loadOwnedSession(c)
	if !ok {
		return
	}
	name := c.Param("name")
	if !artifactNameRe.MatchString(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法文件名"})
		return
	}
	c.File(filepath.Join(s.cfg.ArtifactsDir, sess.ID, name))
}
