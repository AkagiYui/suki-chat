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
	"context"
	"errors"
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
const egressURL = "http://suki-egress:8888" // 会话容器的唯一出网通道

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
	BrowserImage string        // 浏览器镜像（CloakBrowser）
	ControlURL   string        // 容器回连控制平面的地址（如 http://host.docker.internal:8182）
	CPUs         float64       // 单 runner CPU 上限
	MemoryMB     int64         // 单 runner 内存上限
	PidsLimit    int64         // 单 runner 进程数上限
	Network      string        // 容器网络（suki-net，runner 与浏览器同网）
	IdleTimeout  time.Duration // 空闲多久回收容器
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

	mu      sync.Mutex
	runtime map[string]*sessionRuntime
	queues  map[string]chan string // 每会话待处理消息队列（runner 长轮询拉取）

	browserMu sync.Mutex
	browsers  map[string]*browserHandle // 浏览器容器：key=user-<uid> 或 sess-<sid>
}

type sessionRuntime struct {
	mu           sync.Mutex
	running      bool
	runnerUp     bool
	runnerName   string
	lastActivity time.Time
}

type browserHandle struct {
	name         string
	lastActivity time.Time
}

// NewManager 创建会话管理器。
func NewManager(s store.Store, runner RunnerBackend, ws workspace.Store, tokens TokenIssuer, cfg Config) *Manager {
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
		runtime:  make(map[string]*sessionRuntime),
		queues:   make(map[string]chan string),
		browsers: make(map[string]*browserHandle),
	}
}

// EnsureBrowser 确保该会话可用的浏览器容器在运行，返回其 CDP 地址（容器名:9222）。
// 默认每用户共享一个浏览器；会话勾选独立浏览器时为该会话单独起一个。
// runner 在需要截图时按需调用（经内部接口），故浏览器是懒启动。
func (m *Manager) EnsureBrowser(ctx context.Context, sess *store.Session) (string, error) {
	key := "user-" + sess.UserID
	labelKey, labelVal := "suki.user", sess.UserID
	if sess.IndependentBrowser {
		key = "sess-" + sess.ID
		labelKey, labelVal = "suki.session", sess.ID
	}
	name := "suki-browser-" + key
	cdp := "http://" + name + ":9222"

	m.browserMu.Lock()
	defer m.browserMu.Unlock()
	if h := m.browsers[key]; h != nil {
		h.lastActivity = time.Now()
		return cdp, nil
	}
	_, _, err := m.runner.RunService(ctx, sandbox.ServiceSpec{
		Name:  name,
		Image: m.cfg.BrowserImage,
		// --proxy-server：页面加载走出网代理；HTTP_PROXY：cloakserve 自身下载也走代理。
		Cmd:       []string{"cloakserve", "--proxy-server=" + egressURL},
		Env:       map[string]string{"HTTP_PROXY": egressURL, "HTTPS_PROXY": egressURL},
		Port:      9222,
		Publish:   false, // 走容器网络，runner 按名直连，不暴露主机端口
		Hardened:  false, // Chromium 需要 capability
		Resources: sandbox.ResourceLimits{MemoryMB: 1024, PidsLimit: 1024},
		Network:   m.cfg.Network,
		Labels: map[string]string{
			sandbox.ManagedLabel: "true",
			labelKey:             labelVal,
			"suki.kind":          "browser",
		},
	})
	if err != nil {
		return "", err
	}
	m.browsers[key] = &browserHandle{name: name, lastActivity: time.Now()}
	return cdp, nil
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

	go m.launch(sessionID, userID, s.Model, text)
	return nil
}

// launch 后台：即时回显用户消息、确保 runner 容器在跑、把消息入队等 runner 拉取。
// runner 纯出站（长轮询拉取 + 经出网代理回连），控制平面不主动连入容器。
func (m *Manager) launch(sessionID, userID, modelID, text string) {
	ctx := context.Background()
	emit := func(typ string, data any) { _, _ = m.events.Append(ctx, sessionID, typ, data) }
	emit("user_message", map[string]any{"content": text})

	if err := m.ensureRunner(ctx, sessionID, userID, modelID); err != nil {
		emit("error", map[string]any{"message": "启动会话容器失败: " + err.Error()})
		m.MarkDone(ctx, sessionID, true)
		return
	}
	m.enqueue(sessionID, text) // runner 长轮询会取走并处理；完成时上报 done 事件
}

// ensureRunner 确保该会话的 runner 容器在运行（按需创建，出站-only，无发布端口）。
func (m *Manager) ensureRunner(ctx context.Context, sessionID, userID, modelID string) error {
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.runnerUp {
		rt.lastActivity = time.Now()
		return nil
	}
	mount, err := m.ws.Provision(ctx, sessionID, nil)
	if err != nil {
		return err
	}
	token, err := m.tokens.IssueRunner(userID, sessionID, time.Now())
	if err != nil {
		return err
	}
	name := "suki-runner-" + sessionID
	_, _, err = m.runner.RunService(ctx, sandbox.ServiceSpec{
		Name:     name,
		Image:    m.cfg.RunnerImage,
		Publish:  false, // 出站-only：不发布端口，控制平面不连入
		Hardened: true,
		Env: map[string]string{
			"SUKI_CONTROL_URL":  m.cfg.ControlURL,
			"SUKI_RUNNER_TOKEN": token,
			"SUKI_SESSION_ID":   sessionID,
			"SUKI_MODEL":        modelID,
			"SUKI_EGRESS_URL":   egressURL, // 联网/回连控制平面都经此代理（deny-by-default）
		},
		Mount:     mount,
		Resources: sandbox.ResourceLimits{CPUs: m.cfg.CPUs, MemoryMB: m.cfg.MemoryMB, PidsLimit: m.cfg.PidsLimit},
		Network:   m.cfg.Network, // suki-net（internal）：可按名直连浏览器与出网代理
		Labels: map[string]string{
			sandbox.ManagedLabel: "true",
			"suki.session":       sessionID,
			"suki.user":          userID,
			"suki.kind":          "runner",
		},
	})
	if err != nil {
		return err
	}
	rt.runnerUp = true
	rt.runnerName = name
	rt.lastActivity = time.Now()
	return nil
}

func (m *Manager) queueFor(sessionID string) chan string {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[sessionID]
	if q == nil {
		q = make(chan string, 8)
		m.queues[sessionID] = q
	}
	return q
}

func (m *Manager) enqueue(sessionID, text string) {
	select {
	case m.queueFor(sessionID) <- text:
	default: // 队列满则丢弃（busy 已防并发，正常不会发生）
	}
}

// NextMessage 供 runner 长轮询：阻塞等待该会话的下一条消息（带超时，让 runner 周期性重连）。
func (m *Manager) NextMessage(ctx context.Context, sessionID string) (string, bool) {
	select {
	case msg := <-m.queueFor(sessionID):
		m.touch(sessionID)
		return msg, true
	case <-time.After(25 * time.Second):
		return "", false
	case <-ctx.Done():
		return "", false
	}
}

// MarkDone 由事件接收处在 runner 上报 done/error 时调用：清运行标记、更新会话状态。
func (m *Manager) MarkDone(ctx context.Context, sessionID string, isError bool) {
	rt := m.runtimeFor(sessionID)
	rt.mu.Lock()
	rt.running = false
	rt.lastActivity = time.Now()
	rt.mu.Unlock()
	status := store.SessionIdle
	if isError {
		status = store.SessionError
	}
	_ = m.setStatus(ctx, sessionID, status)
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

	// 回收空闲的浏览器容器。
	m.browserMu.Lock()
	for key, h := range m.browsers {
		if time.Since(h.lastActivity) > m.cfg.IdleTimeout {
			_ = m.runner.RemoveContainer(ctx, h.name)
			delete(m.browsers, key)
			reaped++
		}
	}
	m.browserMu.Unlock()
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

// SaveArtifact 把会话工件（如截图）写入 <ArtifactsDir>/<sessionID>/<name>，返回 API 路径。
func (m *Manager) SaveArtifact(sessionID, name string, data []byte) (string, error) {
	dir := filepath.Join(m.cfg.ArtifactsDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return "/api/sessions/" + sessionID + "/artifacts/" + name, nil
}
