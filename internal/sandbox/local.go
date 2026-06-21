package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
	"time"
)

// LocalProvider 在宿主进程内直接执行命令，工作区是主机上的一个目录。
// 用于无 Docker 的环境（CI 单元测试、轻量本地开发）。不提供隔离，仅供测试。
type LocalProvider struct{}

// NewLocalProvider 创建本地 Provider。
func NewLocalProvider() *LocalProvider { return &LocalProvider{} }

func (p *LocalProvider) Name() string { return "local" }

func (p *LocalProvider) Healthy(_ context.Context) bool { return true }

func (p *LocalProvider) Create(_ context.Context, spec SandboxSpec) (Sandbox, error) {
	dir := spec.Mount.HostPath
	if dir == "" {
		d, err := os.MkdirTemp("", "suki-local-"+spec.SessionID+"-")
		if err != nil {
			return nil, err
		}
		dir = d
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &localSandbox{id: "local-" + spec.SessionID, workdir: dir, env: spec.Env}, nil
}

type localSandbox struct {
	id      string
	workdir string
	env     map[string]string
	mu      sync.Mutex
	stopped bool
}

func (s *localSandbox) ID() string { return s.id }

func (s *localSandbox) Exec(ctx context.Context, spec ExecSpec) (ExecResult, error) {
	if len(spec.Cmd) == 0 {
		return ExecResult{}, nil
	}
	if spec.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSec)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, spec.Cmd[0], spec.Cmd[1:]...)
	cmd.Dir = s.workdir
	if spec.WorkingDir != "" {
		cmd.Dir = spec.WorkingDir
	}
	cmd.Env = os.Environ()
	for k, v := range s.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

func (s *localSandbox) Stop(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return nil
}

func (s *localSandbox) Remove(_ context.Context) error {
	// 注意：本地模式不在 Remove 时删工作区目录，交由 WorkspaceStore 统一管理。
	return s.Stop(context.Background())
}

func (s *localSandbox) Status(_ context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{ID: s.id, Running: !s.stopped}, nil
}
