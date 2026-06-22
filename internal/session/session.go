// Package session 是会话管理器：编排每会话一个 runner 容器的完整生命周期。
//
// 架构（R4 起）：agent 运行时跑在每会话独立的 runner 容器内（pi）。控制平面只负责
// 编排——按会话起/停 runner 容器、转发用户消息、把容器上报的事件落入事件日志（→ SSE）。
// "关闭页面不中断"：run() 在 context.Background() 的后台 goroutine 里把消息投递给容器并
// 等待其完成，HTTP 请求立即返回；容器在服务端持续运行。
//
// 控制平面只管理由它自己创建（带 suki.managed 标签）的容器；基础设施（如 Postgres）不带
// 标签，绝不被触碰。
package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

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

const runnerPort = 8088

// RunnerBackend 是会话 runner 容器的编排后端（由 sandbox.DockerProvider 实现）。
type RunnerBackend interface {
	RunService(ctx context.Context, spec sandbox.ServiceSpec) (id string, hostPort int, err error)
	StopContainer(ctx context.Context, idOrName string) error
	RemoveContainer(ctx context.Context, idOrName string) error
	ListManaged(ctx context.Context) ([]sandbox.ManagedContainer, error)
}

// TokenIssuer 签发 runner 令牌（由 auth.TokenManager 实现）。
type TokenIssuer interface {
	IssueRunner(userID, sessionID string, now time.Time) (string, error)
}

// Config 是会话管理器配置。
type Config struct {
	RunnerImage  string        // 会话 runner 镜像
	ControlURL   string        // 容器回连控制平面的地址（如 http://host.docker.internal:8182）
	CPUs         float64       // 单 runner CPU 上限
	MemoryMB     int64         // 单 runner 内存上限
	PidsLimit    int64         // 单 runner 进程数上限
	Network      string        // runner 网络
	IdleTimeout  time.Duration // 空闲多久回收 runner 容器
	ArtifactsDir string        // 会话工件目录（截图等）
}

// Manager 管理所有会话的生命周期与运行。
type Manager struct {
	users    store.UserRepo
	sessions store.SessionRepo
	events   store.EventStore
	runner   RunnerBackend
	ws       workspace.Store
	tokens   TokenIssuer
	cfg      Config
	http     *http.Client

	mu      sync.Mutex
	runtime map[string]*sessionRuntime
}

type sessionRuntime struct {
	mu           sync.Mutex
	running      bool
	runnerUp     bool
	runnerName   string
	hostPort     int
	lastActivity time.Time
}

// NewManager 创建会话管理器。
func NewManager(s *store.MemoryStore, runner RunnerBackend, ws workspace.Store, tokens TokenIssuer, cfg Config) *Manager {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 15 * time.Minute
	}
	return &Manager{
		users:    s.Users(),
		sessions: s.Sessions(),
		events:   s.Events(),
		runner:   runner,
		ws:       ws,
		tokens:   tokens,
		cfg:      cfg,
		http:     &http.Client{Timeout: 15 * time.Minute}, // agent 一轮可能较久
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

// Create 新建会话。independentBrowser 为 true 时该会话使用独立浏览器（默认共享用户浏览器）。
func (m *Manager) Create(ctx context.Context, userID, title, modelID string, independentBrowser bool) (*store.Session, error) {
	if title == "" {
		title = "新会话"
	}
	now := time.Now()
	s := &store.Session{
		ID:                 uuid.NewString(),
		UserID:             userID,
		Title:              title,
		Model:              modelID,
		Status:             store.SessionCreated,
		IndependentBrowser: independentBrowser,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := m.sessions.Create(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Send 向会话发送一条用户消息，在后台投递给 runner 容器（立即返回）。
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

	emit := func(typ string, data any) { _, _ = m.events.Append(ctx, sessionID, typ, data) }
	emit("user_message", map[string]any{"content": text})

	hostPort, err := m.ensureRunner(ctx, sessionID, userID, modelID)
	if err != nil {
		emit("error", map[string]any{"message": "启动会话容器失败: " + err.Error()})
		_ = m.setStatus(ctx, sessionID, store.SessionError)
		return
	}

	if err := m.postToRunner(ctx, hostPort, text); err != nil {
		emit("error", map[string]any{"message": "会话容器执行失败: " + err.Error()})
		_ = m.setStatus(ctx, sessionID, store.SessionError)
		return
	}
	m.touch(sessionID)
	_ = m.setStatus(ctx, sessionID, store.SessionIdle)
}

// ensureRunner 确保该会话的 runner 容器在运行，返回其主机端口（按需创建）。
func (m *Manager) ensureRunner(ctx context.Context, sessionID, userID, modelID string) (int, error) {
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.runnerUp {
		rt.lastActivity = time.Now()
		return rt.hostPort, nil
	}

	mount, err := m.ws.Provision(ctx, sessionID, nil)
	if err != nil {
		return 0, err
	}
	token, err := m.tokens.IssueRunner(userID, sessionID, time.Now())
	if err != nil {
		return 0, err
	}

	name := "suki-runner-" + sessionID
	_, hostPort, err := m.runner.RunService(ctx, sandbox.ServiceSpec{
		Name:  name,
		Image: m.cfg.RunnerImage,
		Port:  runnerPort,
		Env: map[string]string{
			"SUKI_CONTROL_URL":  m.cfg.ControlURL,
			"SUKI_RUNNER_TOKEN": token,
			"SUKI_SESSION_ID":   sessionID,
			"SUKI_MODEL":        modelID,
			"SUKI_BROWSER_CDP":  "", // 浏览器在后续阶段接入
		},
		Mount:     mount,
		Resources: sandbox.ResourceLimits{CPUs: m.cfg.CPUs, MemoryMB: m.cfg.MemoryMB, PidsLimit: m.cfg.PidsLimit},
		Network:   m.cfg.Network,
		Labels: map[string]string{
			sandbox.ManagedLabel: "true",
			"suki.session":       sessionID,
			"suki.user":          userID,
			"suki.kind":          "runner",
		},
	})
	if err != nil {
		return 0, err
	}
	if err := m.waitRunnerReady(ctx, hostPort); err != nil {
		_ = m.runner.RemoveContainer(ctx, name)
		return 0, err
	}
	rt.runnerUp = true
	rt.runnerName = name
	rt.hostPort = hostPort
	rt.lastActivity = time.Now()
	return hostPort, nil
}

func (m *Manager) waitRunnerReady(ctx context.Context, hostPort int) error {
	deadline := time.Now().Add(40 * time.Second)
	url := fmt.Sprintf("http://127.0.0.1:%d/health", hostPort)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("runner 容器未在限定时间内就绪")
}

func (m *Manager) postToRunner(ctx context.Context, hostPort int, message string) error {
	body, _ := json.Marshal(map[string]string{"message": message})
	url := fmt.Sprintf("http://127.0.0.1:%d/run", hostPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runner 返回 %d", resp.StatusCode)
	}
	return nil
}

// Hibernate 休眠：停止并移除 runner 容器，保留工作区。
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
	m.teardownRunner(ctx, rt)
	return m.setStatus(ctx, sessionID, store.SessionHibernated)
}

// Delete 删除会话：移除 runner 容器、销毁工作区、删除记录。
func (m *Manager) Delete(ctx context.Context, sessionID, userID string) error {
	if err := m.authorize(ctx, sessionID, userID); err != nil {
		return err
	}
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	m.teardownRunner(ctx, rt)
	rt.mu.Unlock()

	_ = m.ws.Destroy(ctx, sessionID)
	m.mu.Lock()
	delete(m.runtime, sessionID)
	m.mu.Unlock()
	return m.sessions.Delete(ctx, sessionID)
}

// teardownRunner 停止并移除 runner 容器（调用方持有 rt.mu）。
func (m *Manager) teardownRunner(ctx context.Context, rt *sessionRuntime) {
	if rt.runnerName != "" {
		_ = m.runner.RemoveContainer(ctx, rt.runnerName)
	}
	rt.runnerUp = false
	rt.runnerName = ""
	rt.hostPort = 0
}

// ListManagedContainers 返回所有受控容器（供管理员查看）。
func (m *Manager) ListManagedContainers(ctx context.Context) ([]sandbox.ManagedContainer, error) {
	return m.runner.ListManaged(ctx)
}

// ReapIdle 回收空闲超时且未在运行的 runner 容器（休眠）。返回回收数量。
func (m *Manager) ReapIdle(ctx context.Context) int {
	m.mu.Lock()
	ids := make([]string, 0, len(m.runtime))
	for id := range m.runtime {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	reaped := 0
	for _, id := range ids {
		rt := m.runtimeFor(id)
		rt.mu.Lock()
		idle := !rt.running && rt.runnerUp && time.Since(rt.lastActivity) > m.cfg.IdleTimeout
		if idle {
			m.teardownRunner(ctx, rt)
			reaped++
		}
		rt.mu.Unlock()
		if idle {
			_ = m.setStatus(ctx, id, store.SessionHibernated)
		}
	}
	return reaped
}

// StartReaper 启动后台空闲回收循环。
func (m *Manager) StartReaper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.ReapIdle(ctx)
			}
		}
	}()
}

func (m *Manager) touch(sessionID string) {
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	rt.lastActivity = time.Now()
	rt.mu.Unlock()
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

// saveArtifact 把会话工件（如截图）写入 <ArtifactsDir>/<sessionID>/<name>，返回 API 路径。
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
