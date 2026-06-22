// Package server 是 HTTP 控制平面：路由、鉴权、会话 API、SSE 事件流与 Admin。
package server

import (
	"net/http"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/gin-gonic/gin"
)

// Server 持有控制平面依赖。
type Server struct {
	store    store.Store
	tokens   *auth.TokenManager
	sessions *session.Manager
	cfg      config.Config
}

// New 创建 Server。
func New(st store.Store, tokens *auth.TokenManager, sessions *session.Manager, cfg config.Config) *Server {
	return &Server{store: st, tokens: tokens, sessions: sessions, cfg: cfg}
}

// Router 构建 Gin 路由。
func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
		api.GET("/models", s.handleModels)

		api.POST("/auth/register", s.handleRegister)
		api.POST("/auth/login", s.handleLogin)

		// 会话容器内运行时回连控制平面（runner 令牌鉴权）：模型网关代理 + 事件上报。
		internal := api.Group("/internal")
		internal.Use(s.runnerAuth())
		{
			internal.POST("/v1/chat/completions", s.handleInternalChat)
			internal.POST("/sessions/:id/events", s.handleInternalEvents)
			internal.POST("/sessions/:id/browser", s.handleInternalBrowser)
			internal.POST("/sessions/:id/artifacts", s.handleInternalArtifact)
		}

		authed := api.Group("")
		authed.Use(s.authRequired())
		{
			authed.GET("/me", s.handleMe)

			authed.GET("/sessions", s.handleListSessions)
			authed.POST("/sessions", s.handleCreateSession)
			authed.GET("/sessions/:id", s.handleGetSession)
			authed.POST("/sessions/:id/messages", s.handleSendMessage)
			authed.GET("/sessions/:id/events", s.handleSessionEvents)
			authed.GET("/sessions/:id/artifacts/:name", s.handleArtifact)
			authed.POST("/sessions/:id/hibernate", s.handleHibernate)
			authed.DELETE("/sessions/:id", s.handleDeleteSession)

			admin := authed.Group("/admin")
			admin.Use(s.adminRequired())
			{
				admin.GET("/users", s.handleAdminListUsers)
				admin.GET("/sessions", s.handleAdminListSessions)
				admin.GET("/containers", s.handleAdminListContainers)
			}
		}
	}
	return r
}

// handleModels 返回可用模型列表（供前端选择）。
func (s *Server) handleModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"models": []gin.H{
		{"id": s.cfg.DeepSeek.FastModel, "label": "DeepSeek Flash（快速）"},
		{"id": s.cfg.DeepSeek.ProModel, "label": "DeepSeek Pro（强力）"},
	}})
}

// ---- 通用响应辅助 ----

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

func serverError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
