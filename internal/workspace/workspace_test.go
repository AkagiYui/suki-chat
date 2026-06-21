package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeVolumeManager 记录卷操作，用于单元测试 LocalVolumeStore。
type fakeVolumeManager struct {
	ensured map[string]bool
	removed map[string]bool
}

func newFakeVM() *fakeVolumeManager {
	return &fakeVolumeManager{ensured: map[string]bool{}, removed: map[string]bool{}}
}
func (f *fakeVolumeManager) EnsureVolume(_ context.Context, name string) error {
	f.ensured[name] = true
	return nil
}
func (f *fakeVolumeManager) RemoveVolume(_ context.Context, name string) error {
	f.removed[name] = true
	return nil
}

func TestLocalVolumeStore(t *testing.T) {
	ctx := context.Background()
	vm := newFakeVM()
	s := NewLocalVolumeStore(vm, "suki-ws-")

	mount, err := s.Provision(ctx, "sess1", nil)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	if mount.Volume != "suki-ws-sess1" || mount.Target != "/workspace" {
		t.Fatalf("挂载引用不符: %+v", mount)
	}
	if !vm.ensured["suki-ws-sess1"] {
		t.Fatal("应已创建卷")
	}

	// Snapshot/Release 在本地模式为 no-op，不应报错
	if _, err := s.Snapshot(ctx, "sess1", mount); err != nil {
		t.Fatalf("Snapshot 应 no-op: %v", err)
	}
	if err := s.Release(ctx, mount, false); err != nil {
		t.Fatalf("Release 应 no-op: %v", err)
	}

	if err := s.Destroy(ctx, "sess1"); err != nil {
		t.Fatalf("Destroy 失败: %v", err)
	}
	if !vm.removed["suki-ws-sess1"] {
		t.Fatal("应已删除卷")
	}
}

func TestLocalDirStore(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	s := NewLocalDirStore(base)

	mount, err := s.Provision(ctx, "sess1", nil)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	if mount.HostPath != filepath.Join(base, "sess1") {
		t.Fatalf("HostPath 不符: %s", mount.HostPath)
	}
	if _, err := os.Stat(mount.HostPath); err != nil {
		t.Fatalf("工作区目录应存在: %v", err)
	}

	if err := s.Destroy(ctx, "sess1"); err != nil {
		t.Fatalf("Destroy 失败: %v", err)
	}
	if _, err := os.Stat(mount.HostPath); !os.IsNotExist(err) {
		t.Fatal("工作区目录应已删除")
	}
}
