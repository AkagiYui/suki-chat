package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
	"github.com/gin-gonic/gin"
)

type fakeClient struct{ n int }

func (c *fakeClient) Chat(_ context.Context, _ model.ChatRequest) (*model.ChatResponse, error) {
	c.n++
	if c.n == 1 {
		return &model.ChatResponse{
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "c1", Type: "function", Function: model.FunctionCall{Name: "run_shell", Arguments: `{"command":"echo hi"}`}},
			}},
			Usage: model.Usage{TotalTokens: 10},
		}, nil
	}
	return &model.ChatResponse{
		Message:      model.Message{Role: model.RoleAssistant, Content: "已完成"},
		FinishReason: "stop",
		Usage:        model.Usage{TotalTokens: 20},
	}, nil
}

func setup(t *testing.T) (*httptest.Server, *store.MemoryStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st := store.NewMemoryStore()
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	mgr := session.NewManager(st, sandbox.NewLocalProvider(), workspace.NewLocalDirStore(t.TempDir()), &fakeClient{}, session.Config{Image: "alpine:3", MaxIters: 6})
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
	for _, want := range []string{"event: user_message", "event: tool_result", "event: done", "已完成"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE 回放缺少 %q，实际:\n%s", want, body)
		}
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
