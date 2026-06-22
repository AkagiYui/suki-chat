package sandbox

import (
	"context"
	"io"
	"net/http"
)

// EnsureVolume 创建一个 Docker named volume（已存在则幂等成功），并打上受控标签。
func (p *DockerProvider) EnsureVolume(ctx context.Context, name string) error {
	resp, err := p.do(ctx, http.MethodPost, "/volumes/create", map[string]any{
		"Name":   name,
		"Labels": map[string]string{ManagedLabel: "true", "suki.kind": "workspace"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil // create 对已存在的卷返回 200/201，均视为成功
}

// RemoveVolume 删除一个 Docker named volume。
func (p *DockerProvider) RemoveVolume(ctx context.Context, name string) error {
	resp, err := p.do(ctx, http.MethodDelete, "/volumes/"+name+"?force=true", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}
