package store

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// MemoryStore 是 UserRepo / SessionRepo / EventStore 的内存实现集合。
type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]*User
	sessions map[string]*Session

	events *memoryEventStore
}

// NewMemoryStore 创建内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		users:    make(map[string]*User),
		sessions: make(map[string]*Session),
		events:   newMemoryEventStore(),
	}
}

// Users 返回用户仓储视图。
func (m *MemoryStore) Users() UserRepo { return (*memoryUserRepo)(m) }

// Sessions 返回会话仓储视图。
func (m *MemoryStore) Sessions() SessionRepo { return (*memorySessionRepo)(m) }

// Events 返回事件存储。
func (m *MemoryStore) Events() EventStore { return m.events }

// ---- UserRepo ----

type memoryUserRepo MemoryStore

func (r *memoryUserRepo) Create(_ context.Context, u *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.users {
		if ex.Email == u.Email {
			return ErrConflict
		}
	}
	cp := *u
	r.users[u.ID] = &cp
	return nil
}

func (r *memoryUserRepo) GetByID(_ context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if u, ok := r.users[id]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (r *memoryUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.users {
		if u.Email == email {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (r *memoryUserRepo) List(_ context.Context) ([]*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*User, 0, len(r.users))
	for _, u := range r.users {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (r *memoryUserRepo) AddQuota(_ context.Context, id string, delta int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return 0, ErrNotFound
	}
	next := u.QuotaTokens + delta
	if next < 0 {
		return u.QuotaTokens, ErrQuotaExceeded
	}
	u.QuotaTokens = next
	return next, nil
}

// ---- SessionRepo ----

type memorySessionRepo MemoryStore

func (r *memorySessionRepo) Create(_ context.Context, s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[s.ID]; ok {
		return ErrConflict
	}
	cp := *s
	r.sessions[s.ID] = &cp
	return nil
}

func (r *memorySessionRepo) GetByID(_ context.Context, id string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.sessions[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (r *memorySessionRepo) ListByUser(_ context.Context, userID string) ([]*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0)
	for _, s := range r.sessions {
		if s.UserID == userID {
			cp := *s
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *memorySessionRepo) ListAll(_ context.Context) ([]*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *memorySessionRepo) Update(_ context.Context, s *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[s.ID]; !ok {
		return ErrNotFound
	}
	s.UpdatedAt = time.Now()
	cp := *s
	r.sessions[s.ID] = &cp
	return nil
}

func (r *memorySessionRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[id]; !ok {
		return ErrNotFound
	}
	delete(r.sessions, id)
	return nil
}

// ---- EventStore ----

type memoryEventStore struct {
	mu     sync.RWMutex
	seq    int64
	bySess map[string][]Event
	subs   map[string]map[int]chan Event
	nextID int
}

func newMemoryEventStore() *memoryEventStore {
	return &memoryEventStore{
		bySess: make(map[string][]Event),
		subs:   make(map[string]map[int]chan Event),
	}
}

func (s *memoryEventStore) Append(_ context.Context, sessionID, typ string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	s.seq++
	ev := Event{
		Seq:       s.seq,
		SessionID: sessionID,
		Type:      typ,
		Data:      raw,
		CreatedAt: time.Now(),
	}
	s.bySess[sessionID] = append(s.bySess[sessionID], ev)
	// 复制订阅者快照，锁外推送，避免阻塞写入。
	var targets []chan Event
	for _, ch := range s.subs[sessionID] {
		targets = append(targets, ch)
	}
	s.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
			// 订阅者积压：丢弃实时推送，客户端可在重连时按 afterSeq 回放补齐。
		}
	}
	return ev, nil
}

func (s *memoryEventStore) List(_ context.Context, sessionID string, afterSeq int64) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.bySess[sessionID]
	out := make([]Event, 0, len(all))
	for _, ev := range all {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (s *memoryEventStore) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	s.mu.Lock()
	if s.subs[sessionID] == nil {
		s.subs[sessionID] = make(map[int]chan Event)
	}
	id := s.nextID
	s.nextID++
	s.subs[sessionID][id] = ch
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if m := s.subs[sessionID]; m != nil {
			if _, ok := m[id]; ok {
				delete(m, id)
				close(ch)
			}
		}
	}
	return ch, cancel
}
