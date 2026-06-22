package server

import (
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

	raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		badRequest(c, "非法的请求体")
		return
	}
	body["stream"] = false // 强制非流式，保证能可靠读取 usage 计量
	fwd, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost,
		s.cfg.DeepSeek.BaseURL+"/chat/completions", bytes.NewReader(fwd))
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
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	// 计量：从响应 usage 扣减配额。
	var parsed struct {
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(respBody, &parsed)
	if parsed.Usage.TotalTokens > 0 {
		_, _ = s.store.Users().AddQuota(c.Request.Context(), userID, -int64(parsed.Usage.TotalTokens))
	}

	c.Data(resp.StatusCode, "application/json", respBody)
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
