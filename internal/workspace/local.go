package workspace

import (
	"context"
	"os"
	"path/filepath"

	"github.com/akagiyui/suki-chat/internal/sandbox"
)

const mountTarget = "/workspace"

// LocalVolumeStore 是单机模式实现：工作区 = 宿主上的 Docker named volume。
// 休眠不动卷（Snapshot/Release 为 no-op）；对象存储留到加第二台主机时再开。
type LocalVolumeStore struct {
	vols   VolumeManager
	prefix string
}

// NewLocalVolumeStore 创建基于 Docker 卷的工作区存储。
func NewLocalVolumeStore(vols VolumeManager, prefix string) *LocalVolumeStore {
	return &LocalVolumeStore{vols: vols, prefix: prefix}
}

func (s *LocalVolumeStore) volName(sessionID string) string { return s.prefix + sessionID }

func (s *LocalVolumeStore) Provision(ctx context.Context, sessionID string, _ *SnapshotRef) (sandbox.MountRef, error) {
	name := s.volName(sessionID)
	if err := s.vols.EnsureVolume(ctx, name); err != nil { // 幂等：已存在=恢复，不存在=新建
		return sandbox.MountRef{}, err
	}
	return sandbox.MountRef{Volume: name, Target: mountTarget}, nil
}

func (s *LocalVolumeStore) Snapshot(_ context.Context, sessionID string, _ sandbox.MountRef) (SnapshotRef, error) {
	return SnapshotRef{SessionID: sessionID}, nil // no-op：卷留在本机本身就是持久态
}

func (s *LocalVolumeStore) Release(_ context.Context, _ sandbox.MountRef, _ bool) error {
	return nil // no-op：单机永远保留本地卷
}

func (s *LocalVolumeStore) Destroy(ctx context.Context, sessionID string) error {
	return s.vols.RemoveVolume(ctx, s.volName(sessionID))
}

// LocalDirStore 是无 Docker 的实现：工作区 = 宿主上的一个目录。供测试与 sandbox=local 使用。
type LocalDirStore struct {
	baseDir string
}

// NewLocalDirStore 创建基于主机目录的工作区存储。
func NewLocalDirStore(baseDir string) *LocalDirStore {
	return &LocalDirStore{baseDir: baseDir}
}

func (s *LocalDirStore) dir(sessionID string) string { return filepath.Join(s.baseDir, sessionID) }

func (s *LocalDirStore) Provision(_ context.Context, sessionID string, _ *SnapshotRef) (sandbox.MountRef, error) {
	dir := s.dir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return sandbox.MountRef{}, err
	}
	return sandbox.MountRef{HostPath: dir, Target: mountTarget}, nil
}

func (s *LocalDirStore) Snapshot(_ context.Context, sessionID string, _ sandbox.MountRef) (SnapshotRef, error) {
	return SnapshotRef{SessionID: sessionID}, nil
}

func (s *LocalDirStore) Release(_ context.Context, _ sandbox.MountRef, _ bool) error { return nil }

func (s *LocalDirStore) Destroy(_ context.Context, sessionID string) error {
	return os.RemoveAll(s.dir(sessionID))
}
