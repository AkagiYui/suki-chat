package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// 集成测试：需一个真实 PostgreSQL，设置 SUKI_CHAT_TEST_DSN 后运行。
func pgTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("SUKI_CHAT_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 SUKI_CHAT_TEST_DSN，跳过 Postgres 集成测试")
	}
	s, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(), "TRUNCATE users, sessions, events"); err != nil {
		t.Fatalf("清表失败: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPostgresUsers(t *testing.T) {
	ctx := context.Background()
	s := pgTestStore(t)
	u := &User{ID: "u1", Email: "a@b.com", PasswordHash: "h", Role: RoleUser, QuotaTokens: 100, CreatedAt: time.Now()}
	if err := s.Users().Create(ctx, u); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := s.Users().Create(ctx, &User{ID: "u2", Email: "a@b.com"}); err != ErrConflict {
		t.Fatalf("同邮箱应 ErrConflict, got %v", err)
	}
	got, err := s.Users().GetByEmail(ctx, "a@b.com")
	if err != nil || got.ID != "u1" || got.QuotaTokens != 100 {
		t.Fatalf("按邮箱查询不符: %v %+v", err, got)
	}
	// 原子扣减
	bal, err := s.Users().AddQuota(ctx, "u1", -30)
	if err != nil || bal != 70 {
		t.Fatalf("扣减失败: %v bal=%d", err, bal)
	}
	if _, err := s.Users().AddQuota(ctx, "u1", -1000); err != ErrQuotaExceeded {
		t.Fatalf("扣超应 ErrQuotaExceeded, got %v", err)
	}
	cur, _ := s.Users().GetByID(ctx, "u1")
	if cur.QuotaTokens != 70 {
		t.Fatalf("失败扣减不应改余额, got %d", cur.QuotaTokens)
	}
	if _, err := s.Users().GetByID(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("应 ErrNotFound, got %v", err)
	}
}

func TestPostgresSessions(t *testing.T) {
	ctx := context.Background()
	s := pgTestStore(t)
	now := time.Now()
	sess := &Session{ID: "s1", UserID: "u1", Title: "t", Model: "m", Status: SessionCreated, IndependentBrowser: true, CreatedAt: now, UpdatedAt: now}
	if err := s.Sessions().Create(ctx, sess); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	sess.Status = SessionRunning
	if err := s.Sessions().Update(ctx, sess); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, _ := s.Sessions().GetByID(ctx, "s1")
	if got.Status != SessionRunning || !got.IndependentBrowser {
		t.Fatalf("字段不符: %+v", got)
	}
	list, _ := s.Sessions().ListByUser(ctx, "u1")
	if len(list) != 1 {
		t.Fatalf("应有 1 个会话, got %d", len(list))
	}
	if err := s.Sessions().Delete(ctx, "s1"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := s.Sessions().GetByID(ctx, "s1"); err != ErrNotFound {
		t.Fatalf("应 ErrNotFound, got %v", err)
	}
}

func TestPostgresEvents(t *testing.T) {
	ctx := context.Background()
	s := pgTestStore(t)
	es := s.Events()

	ch, cancel := es.Subscribe("s1")
	defer cancel()

	for i := 0; i < 3; i++ {
		if _, err := es.Append(ctx, "s1", "msg", map[string]int{"i": i}); err != nil {
			t.Fatalf("追加失败: %v", err)
		}
	}
	all, _ := es.List(ctx, "s1", 0)
	if len(all) != 3 {
		t.Fatalf("应有 3 条, got %d", len(all))
	}
	if all[0].Seq >= all[1].Seq {
		t.Fatal("seq 应单调递增")
	}
	rest, _ := es.List(ctx, "s1", all[0].Seq)
	if len(rest) != 2 {
		t.Fatalf("回放应有 2 条, got %d", len(rest))
	}
	// 实时订阅应至少收到一条
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("订阅未收到实时事件")
	}
}
