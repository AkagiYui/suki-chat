package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleListSessions(c *gin.Context) {
	list, err := s.store.Sessions().ListByUser(c.Request.Context(), currentUserID(c))
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": list})
}

func (s *Server) handleCreateSession(c *gin.Context) {
	var req struct {
		Title              string `json:"title"`
		Model              string `json:"model"`
		IndependentBrowser bool   `json:"independentBrowser"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.Model == "" {
		req.Model = s.cfg.DeepSeek.FastModel
	}
	if req.Model != s.cfg.DeepSeek.FastModel && req.Model != s.cfg.DeepSeek.ProModel {
		badRequest(c, "不支持的模型")
		return
	}
	sess, err := s.sessions.Create(c.Request.Context(), currentUserID(c), req.Title, req.Model, req.IndependentBrowser)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session": sess})
}

// loadOwnedSession 加载会话并校验归属（管理员可访问任意会话）。
func (s *Server) loadOwnedSession(c *gin.Context) (*store.Session, bool) {
	sess, err := s.store.Sessions().GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return nil, false
	}
	if sess.UserID != currentUserID(c) && c.GetString(ctxRole) != string(store.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该会话"})
		return nil, false
	}
	return sess, true
}

func (s *Server) handleGetSession(c *gin.Context) {
	sess, ok := s.loadOwnedSession(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": sess})
}

func (s *Server) handleSendMessage(c *gin.Context) {
	var req struct {
		Text string `json:"text" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "消息内容不能为空")
		return
	}
	err := s.sessions.Send(c.Request.Context(), c.Param("id"), currentUserID(c), req.Text)
	switch err {
	case nil:
		c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
	case session.ErrBusy:
		c.JSON(http.StatusConflict, gin.H{"error": "会话正在运行，请稍候"})
	case session.ErrQuotaExceeded:
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "配额不足"})
	case session.ErrForbidden:
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该会话"})
	case store.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
	default:
		serverError(c, err)
	}
}

func (s *Server) handleHibernate(c *gin.Context) {
	err := s.sessions.Hibernate(c.Request.Context(), c.Param("id"), currentUserID(c))
	s.respondSimpleSessionOp(c, err)
}

func (s *Server) handleDeleteSession(c *gin.Context) {
	err := s.sessions.Delete(c.Request.Context(), c.Param("id"), currentUserID(c))
	s.respondSimpleSessionOp(c, err)
}

func (s *Server) respondSimpleSessionOp(c *gin.Context, err error) {
	switch err {
	case nil:
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	case session.ErrBusy:
		c.JSON(http.StatusConflict, gin.H{"error": "会话正在运行，请稍候"})
	case session.ErrForbidden:
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该会话"})
	case store.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
	default:
		serverError(c, err)
	}
}

// handleSessionEvents 是 SSE 事件流：先按 last_seq 回放历史，再实时推送。
// 这是"关闭页面不中断 + 断线重连回放"的客户端入口。
func (s *Server) handleSessionEvents(c *gin.Context) {
	sess, ok := s.loadOwnedSession(c)
	if !ok {
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		serverError(c, fmt.Errorf("当前环境不支持流式响应"))
		return
	}

	// 断线重连：浏览器自动带上 Last-Event-ID；也支持显式 last_seq 查询参数。
	var lastSeq int64
	if v := c.Query("last_seq"); v != "" {
		lastSeq, _ = strconv.ParseInt(v, 10, 64)
	} else if v := c.GetHeader("Last-Event-ID"); v != "" {
		lastSeq, _ = strconv.ParseInt(v, 10, 64)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 先订阅，再回放：避免回放与订阅之间漏掉事件（重复的按 seq 去重）。
	ch, cancel := s.store.Events().Subscribe(sess.ID)
	defer cancel()

	writeEvent := func(ev store.Event) {
		fmt.Fprintf(c.Writer, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, ev.Data)
		flusher.Flush()
	}

	history, _ := s.store.Events().List(c.Request.Context(), sess.ID, lastSeq)
	var lastSent int64
	for _, ev := range history {
		writeEvent(ev)
		lastSent = ev.Seq
	}

	ctx := c.Request.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done(): // 客户端断开（关页面）——后台 agent 仍继续运行
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Seq <= lastSent { // 与回放去重
				continue
			}
			writeEvent(ev)
			lastSent = ev.Seq
		case <-heartbeat.C:
			fmt.Fprint(c.Writer, ": ping\n\n") // 注释行心跳，保持连接
			flusher.Flush()
		}
	}
}
