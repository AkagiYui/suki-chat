package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DockerProvider 通过 Docker Engine API 在某个 daemon 上创建会话容器。
// 直接用 net/http 走 unix socket 或 tcp，避免引入庞大的 docker SDK 依赖树。
//
// 安全提示：远程 tcp:// daemon 等于远程 root，生产必须 mTLS/SSH；MVP 默认本地 socket。
type DockerProvider struct {
	name    string
	baseURL string
	http    *http.Client
}

// NewDockerProvider 根据 host（unix:///var/run/docker.sock 或 tcp://ip:port）创建 Provider。
func NewDockerProvider(name, host string) *DockerProvider {
	client, base := dockerHTTP(host)
	return &DockerProvider{name: name, baseURL: base, http: client}
}

func dockerHTTP(host string) (*http.Client, string) {
	switch {
	case strings.HasPrefix(host, "unix://"):
		sock := strings.TrimPrefix(host, "unix://")
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		}
		return &http.Client{Transport: tr}, "http://localhost"
	case strings.HasPrefix(host, "tcp://"):
		return &http.Client{}, "http://" + strings.TrimPrefix(host, "tcp://")
	default:
		// 兜底当作 unix socket 路径
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", host)
			},
		}
		return &http.Client{Transport: tr}, "http://localhost"
	}
}

func (p *DockerProvider) Name() string { return p.name }

func (p *DockerProvider) Healthy(ctx context.Context) bool {
	resp, err := p.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// do 发起一次 Docker API 请求，body 会被 JSON 序列化（nil 表示无 body）。
func (p *DockerProvider) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return p.http.Do(req)
}

type containerCreateBody struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd,omitempty"` // 留空则用镜像自带 CMD（runner 用）
	Tty          bool                `json:"Tty"`
	Env          []string            `json:"Env,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig   hostConfig          `json:"HostConfig"`
}

type hostConfig struct {
	Binds        []string                 `json:"Binds,omitempty"`
	NanoCPUs     int64                    `json:"NanoCpus,omitempty"`
	Memory       int64                    `json:"Memory,omitempty"`
	PidsLimit    int64                    `json:"PidsLimit,omitempty"`
	NetworkMode  string                   `json:"NetworkMode,omitempty"`
	CapDrop      []string                 `json:"CapDrop,omitempty"`
	SecurityOpt  []string                 `json:"SecurityOpt,omitempty"`
	PortBindings map[string][]portBinding `json:"PortBindings,omitempty"`
}

type portBinding struct {
	HostIp   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// ManagedLabel 标记由控制平面创建的资源；所有 list/stop/reap 只作用于带此标签者，
// 基础设施（如 Postgres）不带标签，控制平面绝不触碰。
const ManagedLabel = "suki.managed"

// Create 创建并启动一个会话容器。
func (p *DockerProvider) Create(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	target := spec.Mount.Target
	if target == "" {
		target = "/workspace"
	}
	var binds []string
	switch {
	case spec.Mount.Volume != "":
		binds = []string{spec.Mount.Volume + ":" + target}
	case spec.Mount.HostPath != "":
		binds = []string{spec.Mount.HostPath + ":" + target}
	}

	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	body := containerCreateBody{
		Image:      spec.Image,
		Cmd:        []string{"sleep", "infinity"}, // 容器常驻，工具命令通过 exec 注入
		Tty:        false,
		Env:        env,
		WorkingDir: target,
		Labels:     map[string]string{ManagedLabel: "true", "suki.session": spec.SessionID, "suki.kind": "shell"},
		HostConfig: hostConfig{
			Binds:       binds,
			NetworkMode: spec.Network,
			// 加固：丢弃所有 capability、禁止提权、限制资源。
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
		},
	}
	if spec.Resources.CPUs > 0 {
		body.HostConfig.NanoCPUs = int64(spec.Resources.CPUs * 1e9)
	}
	if spec.Resources.MemoryMB > 0 {
		body.HostConfig.Memory = spec.Resources.MemoryMB * 1024 * 1024
	}
	if spec.Resources.PidsLimit > 0 {
		body.HostConfig.PidsLimit = spec.Resources.PidsLimit
	}

	name := "suki-" + spec.SessionID
	id, err := p.createContainer(ctx, name, body)
	if err != nil {
		return nil, err
	}
	if err := p.startContainer(ctx, id); err != nil {
		_ = p.removeContainer(ctx, id)
		return nil, err
	}
	return &dockerSandbox{provider: p, id: id}, nil
}

func (p *DockerProvider) createContainer(ctx context.Context, name string, body containerCreateBody) (string, error) {
	create := func() (*http.Response, error) {
		return p.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), body)
	}
	resp, err := create()
	if err != nil {
		return "", err
	}
	// 名称冲突：强制删除旧容器后重试（容器视为可抛，状态都在卷里）。
	if resp.StatusCode == http.StatusConflict {
		resp.Body.Close()
		_ = p.forceRemoveByName(ctx, name)
		resp, err = create()
		if err != nil {
			return "", err
		}
	}
	// 镜像不存在：拉取后重试。
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		if err := p.pullImage(ctx, body.Image); err != nil {
			return "", err
		}
		resp, err = create()
		if err != nil {
			return "", err
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sandbox: 创建容器失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (p *DockerProvider) startContainer(ctx context.Context, id string) error {
	resp, err := p.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sandbox: 启动容器失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (p *DockerProvider) pullImage(ctx context.Context, image string) error {
	resp, err := p.do(ctx, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(image), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sandbox: 拉取镜像失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	_, _ = io.Copy(io.Discard, resp.Body) // 必须读完拉取流，否则镜像未就绪
	return nil
}

func (p *DockerProvider) removeContainer(ctx context.Context, id string) error {
	resp, err := p.do(ctx, http.MethodDelete, "/containers/"+id+"?force=true", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (p *DockerProvider) forceRemoveByName(ctx context.Context, name string) error {
	return p.removeContainer(ctx, name)
}

// dockerSandbox 是 DockerProvider 创建出的单个会话容器句柄。
type dockerSandbox struct {
	provider *DockerProvider
	id       string
}

func (s *dockerSandbox) ID() string { return s.id }

type execCreateBody struct {
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Cmd          []string `json:"Cmd"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
}

func (s *dockerSandbox) Exec(ctx context.Context, spec ExecSpec) (ExecResult, error) {
	if spec.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSec)*time.Second)
		defer cancel()
	}
	p := s.provider
	// 1) 创建 exec
	resp, err := p.do(ctx, http.MethodPost, "/containers/"+s.id+"/exec", execCreateBody{
		AttachStdout: true, AttachStderr: true, Cmd: spec.Cmd, WorkingDir: spec.WorkingDir,
	})
	if err != nil {
		return ExecResult{}, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	err = decodeJSON(resp, &created)
	if err != nil {
		return ExecResult{}, err
	}

	// 2) 启动 exec 并读取多路复用流
	startResp, err := p.do(ctx, http.MethodPost, "/exec/"+created.ID+"/start",
		map[string]bool{"Detach": false, "Tty": false})
	if err != nil {
		return ExecResult{}, err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(startResp.Body)
		return ExecResult{}, fmt.Errorf("sandbox: exec 启动失败 %d: %s", startResp.StatusCode, strings.TrimSpace(string(raw)))
	}
	stdout, stderr, err := demuxDockerStream(startResp.Body)
	if err != nil {
		return ExecResult{}, err
	}

	// 3) 查询退出码
	insResp, err := p.do(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil)
	if err != nil {
		return ExecResult{}, err
	}
	var inspect struct {
		ExitCode int `json:"ExitCode"`
	}
	if err := decodeJSON(insResp, &inspect); err != nil {
		return ExecResult{}, err
	}
	return ExecResult{ExitCode: inspect.ExitCode, Stdout: string(stdout), Stderr: string(stderr)}, nil
}

func (s *dockerSandbox) Stop(ctx context.Context) error {
	resp, err := s.provider.do(ctx, http.MethodPost, "/containers/"+s.id+"/stop?t=5", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (s *dockerSandbox) Remove(ctx context.Context) error {
	return s.provider.removeContainer(ctx, s.id)
}

func (s *dockerSandbox) Status(ctx context.Context) (Status, error) {
	resp, err := s.provider.do(ctx, http.MethodGet, "/containers/"+s.id+"/json", nil)
	if err != nil {
		return Status{}, err
	}
	var inspect struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := decodeJSON(resp, &inspect); err != nil {
		return Status{}, err
	}
	return Status{ID: s.id, Running: inspect.State.Running}, nil
}

// demuxDockerStream 解析 Docker 多路复用流（Tty=false）：
// 每帧 8 字节头 [STREAM_TYPE,0,0,0, SIZE(4 字节大端)]，随后 SIZE 字节负载。
func demuxDockerStream(r io.Reader) (stdout, stderr []byte, err error) {
	header := make([]byte, 8)
	for {
		if _, e := io.ReadFull(r, header); e != nil {
			if e == io.EOF || e == io.ErrUnexpectedEOF {
				return stdout, stderr, nil
			}
			return stdout, stderr, e
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		payload := make([]byte, size)
		if _, e := io.ReadFull(r, payload); e != nil {
			return stdout, stderr, e
		}
		switch header[0] {
		case 1:
			stdout = append(stdout, payload...)
		case 2:
			stderr = append(stderr, payload...)
		}
	}
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sandbox: docker api %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
