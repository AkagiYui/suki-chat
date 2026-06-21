// Package sandbox 抽象"会话运行环境"。
//
// 业务层只依赖 Provider/Sandbox 接口；底层可换 Docker（本地/远程）、microVM、
// K8s Pod 或托管沙箱，业务零改动。这正是"先用 Docker 跑 MVP，日后升级隔离"
// 的关键接缝。
package sandbox

import "context"

// ResourceLimits 是单个会话沙箱的资源上限（"扩容单个会话"在此调整）。
type ResourceLimits struct {
	CPUs      float64 // CPU 核数，如 0.5
	MemoryMB  int64   // 内存上限（MiB）
	PidsLimit int64   // 进程数上限
}

// MountRef 描述工作区挂载来源：Docker 卷名或主机目录，二选一。
type MountRef struct {
	Volume   string // Docker named volume 名
	HostPath string // 主机目录（local 模式/测试用）
	Target   string // 容器内挂载点，如 /workspace
}

// SandboxSpec 描述要创建的会话沙箱。
type SandboxSpec struct {
	SessionID string
	Image     string
	Resources ResourceLimits
	Mount     MountRef
	Env       map[string]string
	Network   string
}

// ExecSpec 是一次命令执行请求。
type ExecSpec struct {
	Cmd        []string
	WorkingDir string
	TimeoutSec int
}

// ExecResult 是命令执行结果。
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Status 是沙箱当前状态。
type Status struct {
	ID      string
	Running bool
}

// Sandbox 是一个运行中（或可恢复）的会话运行环境实例。
type Sandbox interface {
	ID() string
	Exec(ctx context.Context, spec ExecSpec) (ExecResult, error)
	Stop(ctx context.Context) error   // 停止运行（休眠），保留工作区
	Remove(ctx context.Context) error // 彻底删除容器
	Status(ctx context.Context) (Status, error)
}

// Provider 是"能在某后端创建会话沙箱"的工厂。
// 一个 Docker daemon（本地或远程）= 一个 Provider 实例；加主机 = 加 Provider。
type Provider interface {
	Name() string
	Create(ctx context.Context, spec SandboxSpec) (Sandbox, error)
	Healthy(ctx context.Context) bool
}
