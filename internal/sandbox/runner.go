package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ServiceSpec 描述一个"服务型"容器（用镜像 CMD 常驻），用于会话 runner 与浏览器容器。
type ServiceSpec struct {
	Name      string
	Image     string
	Cmd       []string // 留空用镜像自带 CMD
	Port      int      // 服务端口
	Publish   bool     // 是否把 Port 发布到 127.0.0.1（runner=true；浏览器走容器网络=false）
	Hardened  bool     // 是否 drop 所有 capability（runner=true；浏览器需要 caps=false）
	Env       map[string]string
	Mount     MountRef
	Resources ResourceLimits
	Network   string
	Labels    map[string]string
}

// ManagedContainer 是一条受控容器记录（仅含 suki.managed=true 者）。
type ManagedContainer struct {
	ID          string
	Names       []string
	Labels      map[string]string
	State       string // running / exited / ...
	CreatedUnix int64
}

// EnsureNetwork 创建一个用户自定义 bridge 网络（已存在则幂等成功）。
// internal=true 时该网络无任何外联（无 NAT/网关到宿主或互联网）——会话容器放这里，
// 唯一出口是出网代理。runner 与浏览器同网，可按容器名直连。
func (p *DockerProvider) EnsureNetwork(ctx context.Context, name string, internal bool) error {
	resp, err := p.do(ctx, http.MethodPost, "/networks/create", map[string]any{
		"Name": name, "Driver": "bridge", "Internal": internal,
		"Labels": map[string]string{ManagedLabel: "true", "suki.kind": "network"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // 201=创建；403/409=已存在，均视为成功
	return nil
}

// ConnectNetwork 把容器额外接入一个网络（出网代理需同时接 internal 网与有互联网的网）。
func (p *DockerProvider) ConnectNetwork(ctx context.Context, network, container string) error {
	resp, err := p.do(ctx, http.MethodPost, "/networks/"+network+"/connect",
		map[string]any{"Container": container})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // 200=成功；403=已连接，均视为成功
	return nil
}

// RunService 创建并启动一个服务型容器；Publish 时返回分配到的主机端口。
func (p *DockerProvider) RunService(ctx context.Context, spec ServiceSpec) (id string, hostPort int, err error) {
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	var binds []string
	if spec.Mount.Volume != "" {
		binds = []string{spec.Mount.Volume + ":" + mountTargetOr(spec.Mount.Target)}
	} else if spec.Mount.HostPath != "" {
		binds = []string{spec.Mount.HostPath + ":" + mountTargetOr(spec.Mount.Target)}
	}

	body := containerCreateBody{
		Image:  spec.Image,
		Cmd:    spec.Cmd,
		Env:    env,
		Labels: spec.Labels,
		HostConfig: hostConfig{
			Binds:       binds,
			NetworkMode: spec.Network,
			SecurityOpt: []string{"no-new-privileges"},
		},
	}
	if spec.Hardened {
		body.HostConfig.CapDrop = []string{"ALL"}
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
	portKey := strconv.Itoa(spec.Port) + "/tcp"
	if spec.Publish {
		body.ExposedPorts = map[string]struct{}{portKey: {}}
		body.HostConfig.PortBindings = map[string][]portBinding{portKey: {{HostIp: "127.0.0.1", HostPort: ""}}}
	}

	id, err = p.createContainer(ctx, spec.Name, body)
	if err != nil {
		return "", 0, err
	}
	if err = p.startContainer(ctx, id); err != nil {
		_ = p.removeContainer(ctx, id)
		return "", 0, err
	}
	if spec.Publish {
		hostPort, err = p.inspectHostPort(ctx, id, portKey)
		if err != nil {
			return "", 0, err
		}
	}
	return id, hostPort, nil
}

func mountTargetOr(t string) string {
	if t == "" {
		return "/workspace"
	}
	return t
}

func (p *DockerProvider) inspectHostPort(ctx context.Context, id, portKey string) (int, error) {
	resp, err := p.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil)
	if err != nil {
		return 0, err
	}
	var inspect struct {
		NetworkSettings struct {
			Ports map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}
	if err := decodeJSON(resp, &inspect); err != nil {
		return 0, err
	}
	binds := inspect.NetworkSettings.Ports[portKey]
	if len(binds) == 0 || binds[0].HostPort == "" {
		return 0, fmt.Errorf("sandbox: 未取到 %s 的主机端口", portKey)
	}
	return strconv.Atoi(binds[0].HostPort)
}

// ListManaged 列出所有受控容器（suki.managed=true）。基础设施容器不带此标签，不会出现。
func (p *DockerProvider) ListManaged(ctx context.Context) ([]ManagedContainer, error) {
	filters := url.QueryEscape(`{"label":["` + ManagedLabel + `=true"]}`)
	resp, err := p.do(ctx, http.MethodGet, "/containers/json?all=true&filters="+filters, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID      string            `json:"Id"`
		Names   []string          `json:"Names"`
		Labels  map[string]string `json:"Labels"`
		State   string            `json:"State"`
		Created int64             `json:"Created"`
	}
	if err := decodeJSON(resp, &raw); err != nil {
		return nil, err
	}
	out := make([]ManagedContainer, 0, len(raw))
	for _, r := range raw {
		out = append(out, ManagedContainer{ID: r.ID, Names: r.Names, Labels: r.Labels, State: r.State, CreatedUnix: r.Created})
	}
	return out, nil
}

// RemoveContainer 强制删除指定容器（按 ID 或名称）。
func (p *DockerProvider) RemoveContainer(ctx context.Context, idOrName string) error {
	return p.removeContainer(ctx, idOrName)
}

// StopContainer 停止指定容器（按 ID 或名称）。
func (p *DockerProvider) StopContainer(ctx context.Context, idOrName string) error {
	resp, err := p.do(ctx, http.MethodPost, "/containers/"+idOrName+"/stop?t=5", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
