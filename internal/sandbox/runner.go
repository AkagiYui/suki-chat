package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// ServiceSpec 描述一个"服务型"容器（用镜像自带 CMD 常驻、对外发布一个端口），
// 用于会话 runner 与浏览器容器。
type ServiceSpec struct {
	Name      string
	Image     string
	Port      int // 容器内服务端口（会被发布到 127.0.0.1 的随机主机端口）
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

// RunService 创建并启动一个服务型容器，返回容器 ID 与分配到的主机端口。
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
	portKey := strconv.Itoa(spec.Port) + "/tcp"

	body := containerCreateBody{
		Image:        spec.Image,
		Env:          env,
		Labels:       spec.Labels,
		ExposedPorts: map[string]struct{}{portKey: {}},
		HostConfig: hostConfig{
			Binds:        binds,
			NetworkMode:  spec.Network,
			CapDrop:      []string{"ALL"},
			SecurityOpt:  []string{"no-new-privileges"},
			PortBindings: map[string][]portBinding{portKey: {{HostIp: "127.0.0.1", HostPort: ""}}},
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

	id, err = p.createContainer(ctx, spec.Name, body)
	if err != nil {
		return "", 0, err
	}
	if err = p.startContainer(ctx, id); err != nil {
		_ = p.removeContainer(ctx, id)
		return "", 0, err
	}
	hostPort, err = p.inspectHostPort(ctx, id, portKey)
	if err != nil {
		return "", 0, err
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
