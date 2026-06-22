package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/gin-gonic/gin"
)

const ctxSessionID = "sid"

// upstreamClient 用于把会话容器的模型请求转发到上游。
var upstreamClient = &http.Client{Timeout: 180 * time.Second}

// runnerAuth 校验会话容器内运行时（runner）令牌。
func (s *Server) runnerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := s.tokens.Parse(bearerToken(c))
		if err != nil || claims.Role != auth.RoleRunner || claims.SessionID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "无效的 runner 令牌"})
			return
		}
		c.Set(ctxUserID, claims.UserID)
		c.Set(ctxSessionID, claims.SessionID)
		c.Next()
	}
}

// handleInternalChat 是给会话容器用的模型网关代理（OpenAI 兼容）。
//
// 容器内的 agent 运行时绝不直连上游、也拿不到真实 API Key：它把请求发到这里，
// 控制平面用服务端密钥转发到 DeepSeek，并按 token 用量扣减该用户的配额。
func (s *Server) handleInternalChat(c *gin.Context) {
	userID := c.GetString(ctxUserID)
	u, err := s.store.Users().GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
		return
	}
	if u.QuotaTokens <= 0 {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "配额不足"})
		return
	}

	// 透明转发：保持 stream/stream_options 原样（pi-ai 用流式 + include_usage）。
	raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		s.cfg.DeepSeek.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		serverError(c, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.DeepSeek.APIKey)

	resp, err := upstreamClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "上游不可用: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 边转发边计量：逐行透传上游响应（SSE 或单 JSON），从 usage 块提取 token 数。
	c.Status(resp.StatusCode)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	flusher, _ := c.Writer.(http.Flusher)
	reader := bufio.NewReader(resp.Body)
	var totalTokens int
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.Writer.Write(line)
			if flusher != nil {
				flusher.Flush()
			}
			if n := usageFromLine(line); n > 0 {
				totalTokens = n
			}
		}
		if readErr != nil {
			break
		}
	}
	if totalTokens > 0 {
		_, _ = s.store.Users().AddQuota(c.Request.Context(), userID, -int64(totalTokens))
	}
}

// usageFromLine 从一行响应（可能是 SSE 的 "data: {...}" 或整段 JSON）中提取 usage.total_tokens。
func usageFromLine(line []byte) int {
	b := bytes.TrimSpace(line)
	if !bytes.Contains(b, []byte("total_tokens")) {
		return 0
	}
	b = bytes.TrimPrefix(b, []byte("data:"))
	b = bytes.TrimSpace(b)
	var parsed struct {
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(b, &parsed) == nil {
		return parsed.Usage.TotalTokens
	}
	return 0
}

// handleInternalBrowser 为会话按需提供浏览器 CDP 地址（默认每用户共享，独立会话单独起）。
func (s *Server) handleInternalBrowser(c *gin.Context) {
	sid := c.GetString(ctxSessionID)
	if c.Param("id") != sid {
		c.JSON(http.StatusForbidden, gin.H{"error": "会话不匹配"})
		return
	}
	sess, err := s.store.Sessions().GetByID(c.Request.Context(), sid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return
	}
	cdp, err := s.sessions.EnsureBrowser(c.Request.Context(), sess)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cdpUrl": cdp})
}

// handleInternalArtifact 接收会话容器上传的工件（如截图 PNG），保存并返回可访问 URL。
func (s *Server) handleInternalArtifact(c *gin.Context) {
	sid := c.GetString(ctxSessionID)
	if c.Param("id") != sid {
		c.JSON(http.StatusForbidden, gin.H{"error": "会话不匹配"})
		return
	}
	name := c.Query("name")
	if !artifactNameRe.MatchString(name) {
		badRequest(c, "非法文件名")
		return
	}
	data, _ := io.ReadAll(io.LimitReader(c.Request.Body, 16<<20))
	url, err := s.sessions.SaveArtifact(sid, name, data)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// handleInternalEvents 接收会话容器上报的事件，写入事件日志（→ SSE 扇出/回放）。
func (s *Server) handleInternalEvents(c *gin.Context) {
	if c.Param("id") != c.GetString(ctxSessionID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "会话不匹配"})
		return
	}
	var ev struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := c.ShouldBindJSON(&ev); err != nil || ev.Type == "" {
		badRequest(c, "非法事件")
		return
	}
	if _, err := s.store.Events().Append(c.Request.Context(), c.Param("id"), ev.Type, ev.Data); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
