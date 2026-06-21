// Package workspace 管理会话工作区的持久化。
//
// 接口按"多机/对象存储"那个更复杂的模式设计；MVP 的本地实现把 Snapshot/Release
// 实现成 no-op。这样切换存储模式只需改配置 + 换实现，业务（生命周期）代码零分支。
package workspace

import (
	"context"

	"github.com/akagiyui/suki-chat/internal/sandbox"
)

// SnapshotRef 是一次工作区快照的引用。MVP 本地模式为空值（占位）。
type SnapshotRef struct {
	SessionID string
	Location  string // 对象存储中的对象键；本地模式为空
}

// Store 是工作区存储接口。
type Store interface {
	// Provision 为会话准备一个可挂载的工作区：新建，或从快照恢复。
	Provision(ctx context.Context, sessionID string, from *SnapshotRef) (sandbox.MountRef, error)
	// Snapshot 休眠时调用：把当前工作区持久化为快照。
	Snapshot(ctx context.Context, sessionID string, m sandbox.MountRef) (SnapshotRef, error)
	// Release 释放本地挂载；keepLocal=false 时可删本地副本（快照已在远端）。
	Release(ctx context.Context, m sandbox.MountRef, keepLocal bool) error
	// Destroy 会话终止，彻底删除工作区。
	Destroy(ctx context.Context, sessionID string) error
}

// VolumeManager 是 LocalVolumeStore 依赖的最小卷管理能力，由 sandbox.DockerProvider 实现。
type VolumeManager interface {
	EnsureVolume(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
}
