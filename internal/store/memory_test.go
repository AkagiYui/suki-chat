package store

import (
	"context"
	"testing"
	"time"
)

func TestUserRepoCreateAndConflict(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	u := &User{ID: "u1", Email: "a@b.com", Role: RoleUser, QuotaTokens: 100, CreatedAt: time.Now()}
	if err := m.Users().Create(ctx, u); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// 同邮箱冲突
	if err := m.Users().Create(ctx, &User{ID: "u2", Email: "a@b.com"}); err != ErrConflict {
		t.Fatalf("应返回 ErrConflict, got %v", err)
	}
	got, err := m.Users().GetByEmail(ctx, "a@b.com")
	if err != nil || got.ID != "u1" {
		t.Fatalf("按邮箱查询失败: %v %+v", err, got)
	}
	// 返回的是副本，修改不应影响存储
	got.PasswordHash = "tampered"
	again, _ := m.Users().GetByID(ctx, "u1")
	if again.PasswordHash == "tampered" {
		t.Fatal("仓储应返回副本，避免外部篡改")
	}
}

func TestUserRepoAddQuota(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	_ = m.Users().Create(ctx, &User{ID: "u1", Email: "a@b.com", QuotaTokens: 100})

	bal, err := m.Users().AddQuota(ctx, "u1", -30)
	if err != nil || bal != 70 {
		t.Fatalf("扣减失败: %v bal=%d", err, bal)
	}
	// 扣超应失败且余额不变
	if _, err := m.Users().AddQuota(ctx, "u1", -1000); err != ErrQuotaExceeded {
		t.Fatalf("应 ErrQuotaExceeded, got %v", err)
	}
	cur, _ := m.Users().GetByID(ctx, "u1")
	if cur.QuotaTokens != 70 {
		t.Fatalf("失败扣减不应改变余额, got %d", cur.QuotaTokens)
	}
}

func TestSessionRepo(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	s := &Session{ID: "s1", UserID: "u1", Title: "t", Status: SessionCreated, CreatedAt: time.Now()}
	if err := m.Sessions().Create(ctx, s); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	s.Status = SessionRunning
	if err := m.Sessions().Update(ctx, s); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, _ := m.Sessions().GetByID(ctx, "s1")
	if got.Status != SessionRunning {
		t.Fatalf("状态未更新: %s", got.Status)
	}
	byUser, _ := m.Sessions().ListByUser(ctx, "u1")
	if len(byUser) != 1 {
		t.Fatalf("应有 1 个会话, got %d", len(byUser))
	}
	if err := m.Sessions().Delete(ctx, "s1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := m.Sessions().GetByID(ctx, "s1"); err != ErrNotFound {
		t.Fatalf("应 ErrNotFound, got %v", err)
	}
}

func TestEventStoreAppendListReplay(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryStore().Events()

	for i := 0; i < 3; i++ {
		if _, err := es.Append(ctx, "s1", "msg", map[string]int{"i": i}); err != nil {
			t.Fatalf("追加失败: %v", err)
		}
	}
	// 另一个会话的事件不应混入
	_, _ = es.Append(ctx, "s2", "msg", nil)

	all, _ := es.List(ctx, "s1", 0)
	if len(all) != 3 {
		t.Fatalf("应有 3 条, got %d", len(all))
	}
	if all[0].Seq >= all[1].Seq {
		t.Fatal("Seq 应单调递增")
	}
	// 回放：afterSeq 之后的部分
	rest, _ := es.List(ctx, "s1", all[0].Seq)
	if len(rest) != 2 {
		t.Fatalf("回放应有 2 条, got %d", len(rest))
	}
}

func TestEventStoreSubscribe(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryStore().Events()

	ch, cancel := es.Subscribe("s1")
	defer cancel()

	go func() {
		_, _ = es.Append(ctx, "s1", "tick", map[string]string{"v": "hello"})
	}()

	select {
	case ev := <-ch:
		if ev.Type != "tick" {
			t.Fatalf("事件类型不符: %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅未收到实时事件")
	}

	// 取消后再追加不应 panic（通道已关闭，不再推送给该订阅者）
	cancel()
	if _, err := es.Append(ctx, "s1", "tick2", nil); err != nil {
		t.Fatalf("取消后追加失败: %v", err)
	}
}
