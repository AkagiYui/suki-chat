package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
	"github.com/gin-gonic/gin"
)

// stubRunner 是 session.RunnerBackend 的桩：RunService 返回一个指向 stub HTTP 服务的端口。
type stubRunner struct{ port int }

func (s stubRunner) RunService(_ context.Context, spec sandbox.ServiceSpec) (string, int, error) {
	return "cid", s.port, nil
}
func (stubRunner) StopContainer(context.Context, string) error   { return nil }
func (stubRunner) RemoveContainer(context.Context, string) error { return nil }
func (stubRunner) ListManaged(context.Context) ([]sandbox.ManagedContainer, error) {
	return []sandbox.ManagedContainer{{
		ID: "c1abc", Names: []string{"/suki-runner-s1"},
		Labels: map[string]string{sandbox.ManagedLabel: "true", "suki.user": "u1", "suki.session": "s1", "suki.kind": "runner"},
		State:  "running",
	}}, nil
}

func setup(t *testing.T) (*httptest.Server, *store.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st := store.NewMemoryStore()
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(stub.Close)
	u, _ := url.Parse(stub.URL)
	port, _ := strconv.Atoi(u.Port())
	mgr := session.NewManager(st, stubRunner{port: port}, workspace.NewLocalDirStore(t.TempDir()), tokens, session.Config{
		RunnerImage: "suki-runner:dev", ControlURL: "http://control", IdleTimeout: time.Hour,
	})
	cfg := config.Config{DefaultQuotaTokens: 1000, DeepSeek: config.DeepSeekConfig{FastModel: "deepseek-v4-flash", ProModel: "deepseek-v4-pro"}}
	ts := httptest.NewServer(New(st, tokens, mgr, cfg).Router())
	t.Cleanup(ts.Close)
	return ts, st
}

func doJSON(t *testing.T, method, url, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, url, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	_ = json.Unmarshal(raw, &out)
	return resp, out
}

func registerUser(t *testing.T, ts *httptest.Server, email string) string {
	t.Helper()
	resp, out := doJSON(t, http.MethodPost, ts.URL+"/api/auth/register", "", map[string]string{"email": email, "password": "password123"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("注册失败: %d %v", resp.StatusCode, out)
	}
	return out["token"].(string)
}

func TestAuthFlow(t *testing.T) {
	ts, _ := setup(t)

	token := registerUser(t, ts, "a@b.com")
	if token == "" {
		t.Fatal("应返回 token")
	}

	// 重复注册冲突
	resp, _ := doJSON(t, http.MethodPost, ts.URL+"/api/auth/register", "", map[string]string{"email": "a@b.com", "password": "password123"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复注册应 409, got %d", resp.StatusCode)
	}

	// 错误密码登录
	resp, _ = doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", "", map[string]string{"email": "a@b.com", "password": "wrongpass1"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401, got %d", resp.StatusCode)
	}

	// 正确登录
	resp, out := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login", "", map[string]string{"email": "a@b.com", "password": "password123"})
	if resp.StatusCode != http.StatusOK || out["token"] == "" {
		t.Fatalf("登录失败: %d %v", resp.StatusCode, out)
	}

	// /me 需要鉴权
	resp, _ = doJSON(t, http.MethodGet, ts.URL+"/api/me", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无令牌访问 /me 应 401, got %d", resp.StatusCode)
	}
	resp, out = doJSON(t, http.MethodGet, ts.URL+"/api/me", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me 失败: %d", resp.StatusCode)
	}
}

func TestSessionAPIAndSSE(t *testing.T) {
	ts, st := setup(t)
	token := registerUser(t, ts, "a@b.com")

	// 创建会话
	resp, out := doJSON(t, http.MethodPost, ts.URL+"/api/sessions", token, map[string]string{"title": "测试", "model": "deepseek-v4-flash"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建会话失败: %d %v", resp.StatusCode, out)
	}
	sessID := out["session"].(map[string]any)["id"].(string)

	// 发送消息（异步）
	resp, _ = doJSON(t, http.MethodPost, ts.URL+"/api/sessions/"+sessID+"/messages", token, map[string]string{"text": "执行 echo"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("发送消息应 202, got %d", resp.StatusCode)
	}

	// 轮询直到完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := st.Sessions().GetByID(context.Background(), sessID)
		if s.Status == store.SessionIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// SSE 回放：用 token query 参数 + last_seq=0，带超时读取已缓冲的历史事件
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/sessions/"+sessID+"/events?last_seq=0&token="+token, nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 请求失败: %v", err)
	}
	data, _ := io.ReadAll(sseResp.Body) // 超时关闭后返回已读取的内容
	sseResp.Body.Close()
	body := string(data)
	// 控制平面即时回显的 user_message 应经 SSE 回放可见（agent 事件由 runner 容器上报，E2E 覆盖）。
	if !strings.Contains(body, "event: user_message") {
		t.Fatalf("SSE 回放应含 user_message，实际:\n%s", body)
	}
}

func TestAdminListContainers(t *testing.T) {
	ts, st := setup(t)
	_ = st.Users().Create(context.Background(), &store.User{ID: "admin1", Email: "admin@b.com", Role: store.RoleAdmin, QuotaTokens: 1})
	adminToken, _ := auth.NewTokenManager("test-secret", time.Hour).Issue("admin1", string(store.RoleAdmin), time.Now())
	resp, out := doJSON(t, http.MethodGet, ts.URL+"/api/admin/containers", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理员查看容器应 200, got %d", resp.StatusCode)
	}
	list, _ := out["containers"].([]any)
	if len(list) != 1 {
		t.Fatalf("应列出 1 个受控容器, got %v", out["containers"])
	}
	c0 := list[0].(map[string]any)
	if c0["user"] != "u1" || c0["kind"] != "runner" {
		t.Fatalf("容器信息不符: %v", c0)
	}
}

func TestAdminRBAC(t *testing.T) {
	ts, st := setup(t)
	token := registerUser(t, ts, "user@b.com")

	// 普通用户访问 admin 应 403
	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/api/admin/users", token, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("普通用户访问 admin 应 403, got %d", resp.StatusCode)
	}

	// 提升一个管理员并用其令牌访问
	_ = st.Users().Create(context.Background(), &store.User{ID: "admin1", Email: "admin@b.com", Role: store.RoleAdmin, QuotaTokens: 1})
	adminToken, _ := auth.NewTokenManager("test-secret", time.Hour).Issue("admin1", string(store.RoleAdmin), time.Now())
	resp, out := doJSON(t, http.MethodGet, ts.URL+"/api/admin/users", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理员访问 admin 应 200, got %d %v", resp.StatusCode, out)
	}
}
