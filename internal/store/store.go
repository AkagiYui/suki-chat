// Package store 定义领域模型与仓储接口，并提供内存实现。
//
// MVP 使用内存实现，便于零依赖测试与单机运行；接口设计保证日后可无缝替换为
// PostgreSQL 实现（控制平面其余代码只依赖这些接口）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 公共错误。
var (
	ErrNotFound      = errors.New("store: 记录不存在")
	ErrConflict      = errors.New("store: 记录冲突（如邮箱已注册）")
	ErrQuotaExceeded = errors.New("store: 配额不足")
)

// Role 是用户角色。
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User 是平台用户。
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"` // 绝不序列化给前端
	Role         Role      `json:"role"`
	QuotaTokens  int64     `json:"quotaTokens"` // 剩余 token 配额（内部配额制）
	CreatedAt    time.Time `json:"createdAt"`
}

// SessionStatus 是会话生命周期状态。
type SessionStatus string

const (
	SessionCreated    SessionStatus = "created"
	SessionRunning    SessionStatus = "running"
	SessionIdle       SessionStatus = "idle"
	SessionHibernated SessionStatus = "hibernated"
	SessionStopped    SessionStatus = "stopped"
	SessionError      SessionStatus = "error"
)

// Session 是用户的一个云端会话。每个会话对应一个隔离 runner 容器。
type Session struct {
	ID                 string        `json:"id"`
	UserID             string        `json:"userId"`
	Title              string        `json:"title"`
	Model              string        `json:"model"`
	Status             SessionStatus `json:"status"`
	IndependentBrowser bool          `json:"independentBrowser"` // true=独立浏览器；默认共享用户浏览器
	CreatedAt          time.Time     `json:"createdAt"`
	UpdatedAt          time.Time     `json:"updatedAt"`
}

// Event 是会话事件日志中的一条（append-only），用于流式下发与断线回放。
type Event struct {
	Seq       int64           `json:"seq"`
	SessionID string          `json:"sessionId"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}

// UserRepo 是用户仓储。
type UserRepo interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context) ([]*User, error)
	// AddQuota 原子调整配额：delta 为负表示扣减，扣减后为负则返回 ErrQuotaExceeded。
	// 返回调整后的余额。
	AddQuota(ctx context.Context, id string, delta int64) (int64, error)
}

// SessionRepo 是会话仓储。
type SessionRepo interface {
	Create(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id string) (*Session, error)
	ListByUser(ctx context.Context, userID string) ([]*Session, error)
	ListAll(ctx context.Context) ([]*Session, error)
	Update(ctx context.Context, s *Session) error
	Delete(ctx context.Context, id string) error
}

// EventStore 是会话事件存储，支持追加、按序回放与实时订阅。
//
// 这是"关闭页面不中断 + 多端查看 + 断线回放"的核心：
// 服务端把 agent 每一步写入事件日志，前端通过 SSE 订阅；
// 重连时按 afterSeq 回放历史再接实时。
type EventStore interface {
	// Append 追加一条事件，自动分配单调递增的 Seq。data 会被 JSON 序列化。
	Append(ctx context.Context, sessionID, typ string, data any) (Event, error)
	// List 返回 seq > afterSeq 的历史事件（afterSeq=0 表示全部）。
	List(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error)
	// Subscribe 订阅某会话后续的实时事件，返回只读通道与取消函数。
	Subscribe(sessionID string) (<-chan Event, func())
}
