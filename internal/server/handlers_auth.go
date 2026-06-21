package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type credentials struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

func (s *Server) handleRegister(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "邮箱格式不正确，或密码少于 8 位")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		serverError(c, err)
		return
	}
	u := &store.User{
		ID:           uuid.NewString(),
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		PasswordHash: hash,
		Role:         store.RoleUser,
		QuotaTokens:  s.cfg.DefaultQuotaTokens,
		CreatedAt:    time.Now(),
	}
	if err := s.store.Users().Create(c.Request.Context(), u); err != nil {
		if err == store.ErrConflict {
			c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已注册"})
			return
		}
		serverError(c, err)
		return
	}
	s.issueAndRespond(c, http.StatusCreated, u)
}

func (s *Server) handleLogin(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "邮箱或密码格式不正确")
		return
	}
	u, err := s.store.Users().GetByEmail(c.Request.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	ok, err := auth.VerifyPassword(req.Password, u.PasswordHash)
	if err != nil || !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
		return
	}
	s.issueAndRespond(c, http.StatusOK, u)
}

func (s *Server) handleMe(c *gin.Context) {
	u, err := s.store.Users().GetByID(c.Request.Context(), currentUserID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": u})
}

func (s *Server) issueAndRespond(c *gin.Context, status int, u *store.User) {
	token, err := s.tokens.Issue(u.ID, string(u.Role), time.Now())
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(status, gin.H{"token": token, "user": u})
}
