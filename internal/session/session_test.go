package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
)

// fakeRunner 记录编排调用，RunService 返回一个指向 stub HTTP 服务的端口。
type fakeRunner struct {
	mu      sync.Mutex
	created []sandbox.ServiceSpec
	removed []string
	port    int
}

func (f *fakeRunner) RunService(_ context.Context, spec sandbox.ServiceSpec) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	return "cid-" + spec.Name, f.port, nil
}
func (f *fakeRunner) StopContainer(context.Context, string) error { return nil }
func (f *fakeRunner) RemoveContainer(_ context.Context, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, n)
	return nil
}
func (f *fakeRunner) ListManaged(context.Context) ([]sandbox.ManagedContainer, error) {
	return []sandbox.ManagedContainer{{ID: "c1", Labels: map[string]string{"suki.user": "u1", "suki.kind": "runner"}, State: "running"}}, nil
}

type fakeTokens struct{}

func (fakeTokens) IssueRunner(string, string, time.Time) (string, error) { return "runner-token", nil }

func newTestManager(t *testing.T, idle time.Duration) (*Manager, *store.MemoryStore, *fakeRunner) {
	t.Helper()
	st := store.NewMemoryStore()
	// stub runner：/health 与 /run 都返回 200
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(stub.Close)
	u, _ := url.Parse(stub.URL)
	port, _ := strconv.Atoi(u.Port())
	fr := &fakeRunner{port: port}
	mgr := NewManager(st, fr, workspace.NewLocalDirStore(t.TempDir()), fakeTokens{}, Config{
		RunnerImage: "suki-runner:dev", ControlURL: "http://control", IdleTimeout: idle,
	})
	return mgr, st, fr
}

func waitStatus(t *testing.T, st *store.MemoryStore, sid string, want store.SessionStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := st.Sessions().GetByID(context.Background(), sid)
		if s != nil && s.Status == want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("超时未到状态 %s", want)
}

func TestSendSpawnsRunner(t *testing.T) {
	ctx := context.Background()
	mgr, st, fr := newTestManager(t, time.Hour)
	_ = st.Users().Create(ctx, &store.User{ID: "u1", Email: "a@b.com", QuotaTokens: 1000})
	sess, err := mgr.Create(ctx, "u1", "t", "deepseek-v4-flash", false)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	if err := mgr.Send(ctx, sess.ID, "u1", "你好"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}
	waitStatus(t, st, sess.ID, store.SessionIdle)

	// 控制平面即时回显 user_message
	evs, _ := st.Events().List(ctx, sess.ID, 0)
	if len(evs) == 0 || evs[0].Type != "user_message" {
		t.Fatalf("应有 user_message 事件, got %+v", evs)
	}
	// 起了一个带正确标签/环境的 runner 容器
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.created) != 1 {
		t.Fatalf("应起 1 个 runner, got %d", len(fr.created))
	}
	spec := fr.created[0]
	if spec.Image != "suki-runner:dev" || spec.Labels["suki.kind"] != "runner" ||
		spec.Labels["suki.session"] != sess.ID || spec.Labels["suki.user"] != "u1" ||
		spec.Labels[sandbox.ManagedLabel] != "true" {
		t.Fatalf("runner 标签/镜像不符: %+v", spec.Labels)
	}
	if spec.Env["SUKI_SESSION_ID"] != sess.ID || spec.Env["SUKI_RUNNER_TOKEN"] == "" {
		t.Fatalf("runner 环境变量不符: %+v", spec.Env)
	}
}

func TestRunnerReusedAcrossMessages(t *testing.T) {
	ctx := context.Background()
	mgr, st, fr := newTestManager(t, time.Hour)
	_ = st.Users().Create(ctx, &store.User{ID: "u1", QuotaTokens: 1000})
	sess, _ := mgr.Create(ctx, "u1", "t", "deepseek-v4-flash", false)

	_ = mgr.Send(ctx, sess.ID, "u1", "1")
	waitStatus(t, st, sess.ID, store.SessionIdle)
	_ = mgr.Send(ctx, sess.ID, "u1", "2")
	waitStatus(t, st, sess.ID, store.SessionIdle)

	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.created) != 1 {
		t.Fatalf("同会话多条消息应复用同一 runner, got %d", len(fr.created))
	}
}

func TestReapIdle(t *testing.T) {
	ctx := context.Background()
	mgr, st, fr := newTestManager(t, 10*time.Millisecond)
	_ = st.Users().Create(ctx, &store.User{ID: "u1", QuotaTokens: 1000})
	sess, _ := mgr.Create(ctx, "u1", "t", "deepseek-v4-flash", false)
	_ = mgr.Send(ctx, sess.ID, "u1", "x")
	waitStatus(t, st, sess.ID, store.SessionIdle)

	time.Sleep(30 * time.Millisecond) // 超过空闲阈值
	if n := mgr.ReapIdle(ctx); n != 1 {
		t.Fatalf("应回收 1 个空闲 runner, got %d", n)
	}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.removed) != 1 {
		t.Fatalf("应移除 runner 容器, removed=%v", fr.removed)
	}
	s, _ := st.Sessions().GetByID(ctx, sess.ID)
	if s.Status != store.SessionHibernated {
		t.Fatalf("回收后应休眠, got %s", s.Status)
	}
}

func TestSendQuotaAndForbidden(t *testing.T) {
	ctx := context.Background()
	mgr, st, _ := newTestManager(t, time.Hour)
	_ = st.Users().Create(ctx, &store.User{ID: "u1", QuotaTokens: 0})
	_ = st.Users().Create(ctx, &store.User{ID: "u2", QuotaTokens: 100})
	sess, _ := mgr.Create(ctx, "u1", "t", "deepseek-v4-flash", false)

	if err := mgr.Send(ctx, sess.ID, "u1", "x"); err != ErrQuotaExceeded {
		t.Fatalf("应 ErrQuotaExceeded, got %v", err)
	}
	if err := mgr.Send(ctx, sess.ID, "u2", "x"); err != ErrForbidden {
		t.Fatalf("应 ErrForbidden, got %v", err)
	}
}

func TestDeleteRemovesRunner(t *testing.T) {
	ctx := context.Background()
	mgr, st, fr := newTestManager(t, time.Hour)
	_ = st.Users().Create(ctx, &store.User{ID: "u1", QuotaTokens: 1000})
	sess, _ := mgr.Create(ctx, "u1", "t", "deepseek-v4-flash", false)
	_ = mgr.Send(ctx, sess.ID, "u1", "x")
	waitStatus(t, st, sess.ID, store.SessionIdle)

	if err := mgr.Delete(ctx, sess.ID, "u1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := st.Sessions().GetByID(ctx, sess.ID); err != store.ErrNotFound {
		t.Fatalf("会话应已删除, got %v", err)
	}
	fr.mu.Lock()
	defer fr.mu.Unlock()
	if len(fr.removed) == 0 {
		t.Fatal("应移除 runner 容器")
	}
}
