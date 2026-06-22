// Package session 是会话管理器：把存储、沙箱、工作区、agent、模型网关编排成
// 一个完整的会话生命周期。
//
// 核心："关闭页面不中断"——SendMessage 启动一个使用 context.Background() 的后台
// goroutine 运行 agent 循环，HTTP 请求立即返回，前端通过 SSE 订阅事件即可随时
// 查看进度、断线重连回放。多个会话各自独立运行，互不影响。
package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/akagiyui/suki-chat/internal/agent"
	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
	"github.com/google/uuid"
)

// 错误。
var (
	ErrForbidden     = errors.New("session: 无权访问该会话")
	ErrBusy          = errors.New("session: 会话正在运行，请稍候")
	ErrQuotaExceeded = errors.New("session: 配额不足")
)

const defaultSystemPrompt = "你是运行在云端隔离容器中的 AI 助手。你可以使用 web_fetch 抓取网页文本，" +
	"使用 run_shell 在隔离沙箱的 /workspace 目录执行 shell 命令，" +
	"使用 screenshot_page 在真实的隐身浏览器中打开网页并整页截图（截图会直接展示给用户，适合“截图/看页面长什么样”的需求）。" +
	"请用简洁的中文回答，必要时主动调用工具。"

// Config 是会话管理器配置。
type Config struct {
	Image        string
	CPUs         float64
	MemoryMB     int64
	PidsLimit    int64
	Network      string
	MaxIters     int
	SystemPrompt string
	BrowserCDP   string // 隐身浏览器 CDP 端点；为空则不启用截图工具
	ArtifactsDir string // 会话工件（截图）落盘目录
}

// Manager 管理所有会话的生命周期与运行。
type Manager struct {
	users    store.UserRepo
	sessions store.SessionRepo
	events   store.EventStore
	provider sandbox.Provider
	ws       workspace.Store
	client   model.Client
	cfg      Config

	mu      sync.Mutex
	runtime map[string]*sessionRuntime
}

// sessionRuntime 是单个会话的运行时状态（不持久化）。
type sessionRuntime struct {
	mu      sync.Mutex
	running bool
	sandbox sandbox.Sandbox
	history []model.Message
}

// NewManager 创建会话管理器。
func NewManager(s *store.MemoryStore, provider sandbox.Provider, ws workspace.Store, client model.Client, cfg Config) *Manager {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if cfg.MaxIters <= 0 {
		cfg.MaxIters = 8
	}
	return &Manager{
		users:    s.Users(),
		sessions: s.Sessions(),
		events:   s.Events(),
		provider: provider,
		ws:       ws,
		client:   client,
		cfg:      cfg,
		runtime:  make(map[string]*sessionRuntime),
	}
}

func (m *Manager) runtimeFor(sessionID string) *sessionRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt := m.runtime[sessionID]
	if rt == nil {
		rt = &sessionRuntime{}
		m.runtime[sessionID] = rt
	}
	return rt
}

// Create 新建会话。
func (m *Manager) Create(ctx context.Context, userID, title, modelID string) (*store.Session, error) {
	if title == "" {
		title = "新会话"
	}
	now := time.Now()
	s := &store.Session{
		ID:        uuid.NewString(),
		UserID:    userID,
		Title:     title,
		Model:     modelID,
		Status:    store.SessionCreated,
		Node:      m.provider.Name(),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.sessions.Create(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Send 向会话发送一条用户消息，在后台启动 agent 运行（立即返回）。
func (m *Manager) Send(ctx context.Context, sessionID, userID, text string) error {
	s, err := m.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.UserID != userID {
		return ErrForbidden
	}
	u, err := m.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.QuotaTokens <= 0 {
		return ErrQuotaExceeded
	}

	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	if rt.running {
		rt.mu.Unlock()
		return ErrBusy
	}
	rt.running = true
	rt.mu.Unlock()

	s.Status = store.SessionRunning
	_ = m.sessions.Update(ctx, s)

	// 后台运行：使用 context.Background()，与发起请求的连接解耦——关页面也不中断。
	go m.run(sessionID, userID, s.Model, text)
	return nil
}

func (m *Manager) run(sessionID, userID, modelID, text string) {
	ctx := context.Background()
	rt := m.runtimeFor(sessionID)
	defer func() {
		rt.mu.Lock()
		rt.running = false
		rt.mu.Unlock()
	}()

	emit := func(typ string, data any) {
		_, _ = m.events.Append(ctx, sessionID, typ, data)
	}

	sb, err := m.ensureSandbox(ctx, sessionID, rt)
	if err != nil {
		emit(agent.EvtError, map[string]any{"message": "创建会话沙箱失败: " + err.Error()})
		m.setStatus(ctx, sessionID, store.SessionError)
		return
	}

	emit("user_message", map[string]any{"content": text})

	tools := []agent.Tool{agent.WebFetchTool(), agent.RunShellTool(sb)}
	if m.cfg.BrowserCDP != "" {
		save := func(name string, png []byte) (string, error) {
			return m.saveArtifact(sessionID, name, png)
		}
		tools = append(tools, agent.ScreenshotTool(m.cfg.BrowserCDP, save, emit))
	}
	ag := agent.New(m.client, modelID, tools, m.cfg.MaxIters, emit)

	rt.mu.Lock()
	history := rt.history
	if len(history) == 0 {
		history = []model.Message{{Role: model.RoleSystem, Content: m.cfg.SystemPrompt}}
	}
	rt.mu.Unlock()

	res, runErr := ag.Run(ctx, history, text)

	rt.mu.Lock()
	rt.history = res.Messages
	rt.mu.Unlock()

	// 计量：按 token 用量扣减内部配额。
	if res.Usage.TotalTokens > 0 {
		_, _ = m.users.AddQuota(ctx, userID, -int64(res.Usage.TotalTokens))
	}

	if runErr != nil {
		m.setStatus(ctx, sessionID, store.SessionError)
		return
	}
	m.setStatus(ctx, sessionID, store.SessionIdle)
}

// ensureSandbox 确保会话有一个运行中的沙箱（按需创建：先备工作区，再起容器）。
func (m *Manager) ensureSandbox(ctx context.Context, sessionID string, rt *sessionRuntime) (sandbox.Sandbox, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sandbox != nil {
		return rt.sandbox, nil
	}
	mount, err := m.ws.Provision(ctx, sessionID, nil)
	if err != nil {
		return nil, err
	}
	sb, err := m.provider.Create(ctx, sandbox.SandboxSpec{
		SessionID: sessionID,
		Image:     m.cfg.Image,
		Resources: sandbox.ResourceLimits{CPUs: m.cfg.CPUs, MemoryMB: m.cfg.MemoryMB, PidsLimit: m.cfg.PidsLimit},
		Mount:     mount,
		Network:   m.cfg.Network,
	})
	if err != nil {
		return nil, err
	}
	rt.sandbox = sb
	return sb, nil
}

// Hibernate 休眠会话：快照工作区、停止沙箱，保留可恢复状态。
func (m *Manager) Hibernate(ctx context.Context, sessionID, userID string) error {
	if err := m.authorize(ctx, sessionID, userID); err != nil {
		return err
	}
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.running {
		return ErrBusy
	}
	if rt.sandbox != nil {
		_, _ = m.ws.Snapshot(ctx, sessionID, sandbox.MountRef{})
		_ = rt.sandbox.Stop(ctx)
		rt.sandbox = nil
	}
	return m.setStatus(ctx, sessionID, store.SessionHibernated)
}

// Delete 删除会话：移除沙箱、销毁工作区、删除记录。
func (m *Manager) Delete(ctx context.Context, sessionID, userID string) error {
	if err := m.authorize(ctx, sessionID, userID); err != nil {
		return err
	}
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	if rt.sandbox != nil {
		_ = rt.sandbox.Remove(ctx)
		rt.sandbox = nil
	}
	rt.mu.Unlock()

	_ = m.ws.Destroy(ctx, sessionID)

	m.mu.Lock()
	delete(m.runtime, sessionID)
	m.mu.Unlock()

	return m.sessions.Delete(ctx, sessionID)
}

func (m *Manager) authorize(ctx context.Context, sessionID, userID string) error {
	s, err := m.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if s.UserID != userID {
		return ErrForbidden
	}
	return nil
}

func (m *Manager) setStatus(ctx context.Context, sessionID string, status store.SessionStatus) error {
	s, err := m.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	s.Status = status
	return m.sessions.Update(ctx, s)
}

// saveArtifact 把会话工件（如截图）写入 <ArtifactsDir>/<sessionID>/<name>，
// 返回前端可访问的 API 路径。
func (m *Manager) saveArtifact(sessionID, name string, data []byte) (string, error) {
	dir := filepath.Join(m.cfg.ArtifactsDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return "/api/sessions/" + sessionID + "/artifacts/" + name, nil
}
