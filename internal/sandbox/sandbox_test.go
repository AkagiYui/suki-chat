package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestLocalSandboxExec(t *testing.T) {
	ctx := context.Background()
	p := NewLocalProvider()
	sb, err := p.Create(ctx, SandboxSpec{SessionID: "t1"})
	if err != nil {
		t.Fatalf("创建本地沙箱失败: %v", err)
	}
	defer sb.Remove(ctx)

	res, err := sb.Exec(ctx, ExecSpec{Cmd: []string{"sh", "-c", "echo hello"}})
	if err != nil {
		t.Fatalf("exec 失败: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" || res.ExitCode != 0 {
		t.Fatalf("结果不符: %+v", res)
	}

	// 非零退出码应被捕获而非报错
	res, err = sb.Exec(ctx, ExecSpec{Cmd: []string{"sh", "-c", "exit 3"}})
	if err != nil {
		t.Fatalf("exec 失败: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("退出码应为 3, got %d", res.ExitCode)
	}
}

// Docker 集成测试：仅在本机 Docker 可用时运行（CI 的 ubuntu runner 自带 docker）。
func TestDockerSandboxIntegration(t *testing.T) {
	ctx := context.Background()
	p := NewDockerProvider("docker/local", "unix:///var/run/docker.sock")
	if !p.Healthy(ctx) {
		t.Skip("本机无可用 Docker，跳过 Docker 集成测试")
	}

	sb, err := p.Create(ctx, SandboxSpec{
		SessionID: "itest",
		Image:     "alpine:3",
		Network:   "none",
		Resources: ResourceLimits{MemoryMB: 128, PidsLimit: 128},
	})
	if err != nil {
		t.Fatalf("创建 Docker 沙箱失败: %v", err)
	}
	defer sb.Remove(ctx)

	res, err := sb.Exec(ctx, ExecSpec{Cmd: []string{"sh", "-c", "echo docker-ok && echo err 1>&2"}})
	if err != nil {
		t.Fatalf("exec 失败: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "docker-ok" {
		t.Fatalf("stdout 不符: %q", res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "err" {
		t.Fatalf("stderr 不符: %q", res.Stderr)
	}

	st, err := sb.Status(ctx)
	if err != nil || !st.Running {
		t.Fatalf("容器应在运行: %+v err=%v", st, err)
	}
}
