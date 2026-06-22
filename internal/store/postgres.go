package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore 是 Store 的 PostgreSQL 实现。
//
// 注意：PostgreSQL 属于基础设施，由外部独立运行（控制平面只作为客户端连接，
// 绝不创建/管理该容器——它不带 suki.managed 标签）。
type PostgresStore struct {
	pool   *pgxpool.Pool
	events *pgEventStore
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
  id text PRIMARY KEY,
  email text UNIQUE NOT NULL,
  password_hash text NOT NULL,
  role text NOT NULL,
  quota_tokens bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  title text NOT NULL,
  model text NOT NULL,
  status text NOT NULL,
  independent_browser boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  seq bigserial PRIMARY KEY,
  session_id text NOT NULL,
  type text NOT NULL,
  data jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq);
`

// NewPostgresStore 连接 PostgreSQL 并初始化表结构。
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool, events: newPGEventStore(pool)}, nil
}

// Close 关闭连接池。
func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) Users() UserRepo       { return &pgUserRepo{s.pool} }
func (s *PostgresStore) Sessions() SessionRepo { return &pgSessionRepo{s.pool} }
func (s *PostgresStore) Events() EventStore    { return s.events }

func isUniqueViolation(err error) bool {
	return err != nil && (errContains(err, "23505") || errContains(err, "duplicate key"))
}
func errContains(err error, sub string) bool {
	return err != nil && len(sub) > 0 && (stringContains(err.Error(), sub))
}
func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- UserRepo ----

type pgUserRepo struct{ pool *pgxpool.Pool }

func (r *pgUserRepo) Create(ctx context.Context, u *User) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users(id,email,password_hash,role,quota_tokens,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		u.ID, u.Email, u.PasswordHash, string(u.Role), u.QuotaTokens, u.CreatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func scanUser(row pgx.Row) (*User, error) {
	var u User
	var role string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &u.QuotaTokens, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = Role(role)
	return &u, nil
}

func (r *pgUserRepo) GetByID(ctx context.Context, id string) (*User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT id,email,password_hash,role,quota_tokens,created_at FROM users WHERE id=$1`, id))
}

func (r *pgUserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT id,email,password_hash,role,quota_tokens,created_at FROM users WHERE email=$1`, email))
}

func (r *pgUserRepo) List(ctx context.Context) ([]*User, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,email,password_hash,role,quota_tokens,created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *pgUserRepo) AddQuota(ctx context.Context, id string, delta int64) (int64, error) {
	var bal int64
	err := r.pool.QueryRow(ctx,
		`UPDATE users SET quota_tokens = quota_tokens + $2 WHERE id=$1 AND quota_tokens + $2 >= 0 RETURNING quota_tokens`,
		id, delta).Scan(&bal)
	if errors.Is(err, pgx.ErrNoRows) {
		// 要么用户不存在，要么会扣成负数
		var exists bool
		if e := r.pool.QueryRow(ctx, `SELECT true FROM users WHERE id=$1`, id).Scan(&exists); errors.Is(e, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, ErrQuotaExceeded
	}
	return bal, err
}

// ---- SessionRepo ----

type pgSessionRepo struct{ pool *pgxpool.Pool }

func (r *pgSessionRepo) Create(ctx context.Context, s *Session) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO sessions(id,user_id,title,model,status,independent_browser,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.UserID, s.Title, s.Model, string(s.Status), s.IndependentBrowser, s.CreatedAt, s.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func scanSession(row pgx.Row) (*Session, error) {
	var s Session
	var status string
	err := row.Scan(&s.ID, &s.UserID, &s.Title, &s.Model, &status, &s.IndependentBrowser, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Status = SessionStatus(status)
	return &s, nil
}

const sessionCols = `id,user_id,title,model,status,independent_browser,created_at,updated_at`

func (r *pgSessionRepo) GetByID(ctx context.Context, id string) (*Session, error) {
	return scanSession(r.pool.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id=$1`, id))
}

func (r *pgSessionRepo) querySessions(ctx context.Context, sql string, args ...any) ([]*Session, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Session, 0)
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *pgSessionRepo) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	return r.querySessions(ctx, `SELECT `+sessionCols+` FROM sessions WHERE user_id=$1 ORDER BY created_at DESC`, userID)
}

func (r *pgSessionRepo) ListAll(ctx context.Context) ([]*Session, error) {
	return r.querySessions(ctx, `SELECT `+sessionCols+` FROM sessions ORDER BY created_at DESC`)
}

func (r *pgSessionRepo) Update(ctx context.Context, s *Session) error {
	s.UpdatedAt = time.Now()
	ct, err := r.pool.Exec(ctx,
		`UPDATE sessions SET title=$2,model=$3,status=$4,independent_browser=$5,updated_at=$6 WHERE id=$1`,
		s.ID, s.Title, s.Model, string(s.Status), s.IndependentBrowser, s.UpdatedAt)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *pgSessionRepo) Delete(ctx context.Context, id string) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- EventStore ----

type pgEventStore struct {
	pool   *pgxpool.Pool
	mu     sync.Mutex
	subs   map[string]map[int]chan Event
	nextID int
}

func newPGEventStore(pool *pgxpool.Pool) *pgEventStore {
	return &pgEventStore{pool: pool, subs: make(map[string]map[int]chan Event)}
}

func (s *pgEventStore) Append(ctx context.Context, sessionID, typ string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	var ev Event
	err = s.pool.QueryRow(ctx,
		`INSERT INTO events(session_id,type,data) VALUES($1,$2,$3) RETURNING seq,created_at`,
		sessionID, typ, raw).Scan(&ev.Seq, &ev.CreatedAt)
	if err != nil {
		return Event{}, err
	}
	ev.SessionID = sessionID
	ev.Type = typ
	ev.Data = raw

	s.mu.Lock()
	var targets []chan Event
	for _, ch := range s.subs[sessionID] {
		targets = append(targets, ch)
	}
	s.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev, nil
}

func (s *pgEventStore) List(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT seq,session_id,type,data,created_at FROM events WHERE session_id=$1 AND seq>$2 ORDER BY seq`,
		sessionID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0)
	for rows.Next() {
		var ev Event
		if err := rows.Scan(&ev.Seq, &ev.SessionID, &ev.Type, &ev.Data, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Subscribe 进程内实时订阅（单实例 SSE；多实例需 LISTEN/NOTIFY，后续再加）。
func (s *pgEventStore) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	s.mu.Lock()
	if s.subs[sessionID] == nil {
		s.subs[sessionID] = make(map[int]chan Event)
	}
	id := s.nextID
	s.nextID++
	s.subs[sessionID][id] = ch
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if m := s.subs[sessionID]; m != nil {
			if _, ok := m[id]; ok {
				delete(m, id)
				close(ch)
			}
		}
	}
}
