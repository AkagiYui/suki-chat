package session

import (
	"context"
	"testing"
	"time"

	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
)

type fakeClient struct {
	calls     int
	responses []*model.ChatResponse
}

func (c *fakeClient) Chat(_ context.Context, _ model.ChatRequest) (*model.ChatResponse, error) {
	i := c.calls
	if i >= len(c.responses) {
		i = len(c.responses) - 1
	}
	c.calls++
	return c.responses[i], nil
}

func newTestManager(t *testing.T, client model.Client) (*Manager, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	mgr := NewManager(
		st,
		sandbox.NewLocalProvider(),
		workspace.NewLocalDirStore(t.TempDir()),
		client,
		Config{Image: "alpine:3", MaxIters: 6},
	)
	return mgr, st
}

func waitForEvent(t *testing.T, st *store.MemoryStore, sessionID, typ string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		evs, _ := st.Events().List(context.Background(), sessionID, 0)
		for _, e := range evs {
			if e.Type == typ {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("超时未等到事件 %s", typ)
}

func TestSessionFullRun(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{responses: []*model.ChatResponse{
		{ // 调用 run_shell
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
				{ID: "c1", Type: "function", Function: model.FunctionCall{Name: "run_shell", Arguments: `{"command":"echo hi"}`}},
			}},
			Usage: model.Usage{TotalTokens: 10},
		},
		{ // 最终答复
			Message:      model.Message{Role: model.RoleAssistant, Content: "已完成"},
			FinishReason: "stop",
			Usage:        model.Usage{TotalTokens: 20},
		},
	}}
	mgr, st := newTestManager(t, client)

	_ = st.Users().Create(ctx, &store.User{ID: "u1", Email: "a@b.com", Role: store.RoleUser, QuotaTokens: 1000})
	sess, err := mgr.Create(ctx, "u1", "测试会话", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	if err := mgr.Send(ctx, sess.ID, "u1", "帮我执行 echo"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	// 后台运行：等待 done 事件
	waitForEvent(t, st, sess.ID, "done")

	// 事件应包含工具调用与结果
	evs, _ := st.Events().List(ctx, sess.ID, 0)
	types := map[string]bool{}
	for _, e := range evs {
		types[e.Type] = true
	}
	for _, want := range []string{"user_message", "tool_call", "tool_result", "assistant_message", "done"} {
		if !types[want] {
			t.Fatalf("缺少事件 %s", want)
		}
	}

	// 配额应被扣减 30（10+20）
	u, _ := st.Users().GetByID(ctx, "u1")
	if u.QuotaTokens != 970 {
		t.Fatalf("配额应扣减为 970, got %d", u.QuotaTokens)
	}

	// 状态应回到 idle
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := st.Sessions().GetByID(ctx, sess.ID)
		if s.Status == store.SessionIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	s, _ := st.Sessions().GetByID(ctx, sess.ID)
	if s.Status != store.SessionIdle {
		t.Fatalf("状态应为 idle, got %s", s.Status)
	}
}

func TestSendQuotaExceeded(t *testing.T) {
	ctx := context.Background()
	mgr, st := newTestManager(t, &fakeClient{responses: []*model.ChatResponse{{Message: model.Message{Content: "x"}}}})
	_ = st.Users().Create(ctx, &store.User{ID: "u1", Email: "a@b.com", QuotaTokens: 0})
	sess, _ := mgr.Create(ctx, "u1", "t", "m")
	if err := mgr.Send(ctx, sess.ID, "u1", "hi"); err != ErrQuotaExceeded {
		t.Fatalf("应 ErrQuotaExceeded, got %v", err)
	}
}

func TestSendForbidden(t *testing.T) {
	ctx := context.Background()
	mgr, st := newTestManager(t, &fakeClient{responses: []*model.ChatResponse{{Message: model.Message{Content: "x"}}}})
	_ = st.Users().Create(ctx, &store.User{ID: "u1", Email: "a@b.com", QuotaTokens: 100})
	_ = st.Users().Create(ctx, &store.User{ID: "u2", Email: "c@d.com", QuotaTokens: 100})
	sess, _ := mgr.Create(ctx, "u1", "t", "m")
	if err := mgr.Send(ctx, sess.ID, "u2", "hi"); err != ErrForbidden {
		t.Fatalf("应 ErrForbidden, got %v", err)
	}
}

func TestDeleteSession(t *testing.T) {
	ctx := context.Background()
	mgr, st := newTestManager(t, &fakeClient{responses: []*model.ChatResponse{{Message: model.Message{Content: "x"}}}})
	_ = st.Users().Create(ctx, &store.User{ID: "u1", Email: "a@b.com", QuotaTokens: 100})
	sess, _ := mgr.Create(ctx, "u1", "t", "m")
	if err := mgr.Delete(ctx, sess.ID, "u1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := st.Sessions().GetByID(ctx, sess.ID); err != store.ErrNotFound {
		t.Fatalf("会话应已删除, got %v", err)
	}
}
