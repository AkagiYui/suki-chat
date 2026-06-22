package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
	"github.com/gin-gonic/gin"
)

// setupInternal 构建一个把上游指向 fakeUpstream 的服务器，用于测试 runner 内部接口。
func setupInternal(t *testing.T, fakeUpstream string) (*httptest.Server, *store.MemoryStore, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st := store.NewMemoryStore()
	_ = st.Users().Create(context.Background(), &store.User{ID: "u1", Email: "a@b.com", QuotaTokens: 1000})
	_ = st.Sessions().Create(context.Background(), &store.Session{ID: "s1", UserID: "u1"})

	tokens := auth.NewTokenManager("test-secret", time.Hour)
	cfg := config.Config{DeepSeek: config.DeepSeekConfig{BaseURL: fakeUpstream, APIKey: "real-key", FastModel: "deepseek-v4-flash", ProModel: "deepseek-v4-pro"}}
	mgr := session.NewManager(st, stubRunner{}, workspace.NewLocalDirStore(t.TempDir()), tokens, session.Config{IdleTimeout: time.Hour})
	ts := httptest.NewServer(New(st, tokens, mgr, cfg).Router())
	t.Cleanup(ts.Close)
	return ts, st, tokens
}

func TestInternalChatProxyMetersQuota(t *testing.T) {
	var sawKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawKey = r.Header.Get("Authorization") // 应是服务端真实 key，非 runner 令牌
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"total_tokens":42}}`)
	}))
	defer upstream.Close()

	ts, st, tokens := setupInternal(t, upstream.URL)
	runnerTok, _ := tokens.IssueRunner("u1", "s1", time.Now())

	resp, out := doJSON(t, http.MethodPost, ts.URL+"/api/internal/v1/chat/completions", runnerTok,
		map[string]any{"model": "deepseek-v4-flash", "messages": []any{map[string]string{"role": "user", "content": "hi"}}, "stream": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("代理应 200, got %d", resp.StatusCode)
	}
	if sawKey != "Bearer real-key" {
		t.Fatalf("上游应收到服务端密钥, got %q", sawKey)
	}
	if out["choices"] == nil {
		t.Fatalf("应透传上游响应, got %v", out)
	}
	// 计量：从 usage 扣减配额 42
	u, _ := st.Users().GetByID(context.Background(), "u1")
	if u.QuotaTokens != 958 {
		t.Fatalf("配额应扣减为 958, got %d", u.QuotaTokens)
	}
}

// 集成：通过代理真正调用 DeepSeek，仅在设置 DEEPSEEK_API_KEY 时运行。
func TestInternalChatProxyIntegration(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("未设置 DEEPSEEK_API_KEY，跳过")
	}
	gin.SetMode(gin.TestMode)
	st := store.NewMemoryStore()
	_ = st.Users().Create(context.Background(), &store.User{ID: "u1", QuotaTokens: 100000})
	_ = st.Sessions().Create(context.Background(), &store.Session{ID: "s1", UserID: "u1"})
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	cfg := config.Config{DeepSeek: config.DeepSeekConfig{BaseURL: "https://api.deepseek.com", APIKey: key, FastModel: "deepseek-v4-flash"}}
	mgr := session.NewManager(st, stubRunner{}, workspace.NewLocalDirStore(t.TempDir()), tokens, session.Config{IdleTimeout: time.Hour})
	ts := httptest.NewServer(New(st, tokens, mgr, cfg).Router())
	defer ts.Close()

	runnerTok, _ := tokens.IssueRunner("u1", "s1", time.Now())
	resp, out := doJSON(t, http.MethodPost, ts.URL+"/api/internal/v1/chat/completions", runnerTok,
		map[string]any{"model": "deepseek-v4-flash", "messages": []any{map[string]string{"role": "user", "content": "用一个字回答：你好"}}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("真实代理调用失败: %d %v", resp.StatusCode, out)
	}
	u, _ := st.Users().GetByID(context.Background(), "u1")
	if u.QuotaTokens >= 100000 {
		t.Fatalf("真实调用应扣减配额, 仍为 %d", u.QuotaTokens)
	}
	t.Logf("通过代理的真实调用 OK，剩余配额 %d", u.QuotaTokens)
}

func TestInternalChatRejectsNonRunnerToken(t *testing.T) {
	ts, _, tokens := setupInternal(t, "http://unused")
	userTok, _ := tokens.Issue("u1", "user", time.Now()) // 普通用户令牌
	resp, _ := doJSON(t, http.MethodPost, ts.URL+"/api/internal/v1/chat/completions", userTok, map[string]any{})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("普通用户令牌应被拒, got %d", resp.StatusCode)
	}
}

func TestInternalEventIngestion(t *testing.T) {
	ts, st, tokens := setupInternal(t, "http://unused")
	runnerTok, _ := tokens.IssueRunner("u1", "s1", time.Now())

	resp, _ := doJSON(t, http.MethodPost, ts.URL+"/api/internal/sessions/s1/events", runnerTok,
		map[string]any{"type": "assistant_message", "data": map[string]string{"content": "来自容器"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("上报事件应 200, got %d", resp.StatusCode)
	}
	evs, _ := st.Events().List(context.Background(), "s1", 0)
	if len(evs) != 1 || evs[0].Type != "assistant_message" {
		t.Fatalf("事件应被写入, got %+v", evs)
	}

	// 令牌的 sessionID 与路径不符应 403
	otherTok, _ := tokens.IssueRunner("u1", "s2", time.Now())
	resp, _ = doJSON(t, http.MethodPost, ts.URL+"/api/internal/sessions/s1/events", otherTok, map[string]any{"type": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("会话不匹配应 403, got %d", resp.StatusCode)
	}
}
