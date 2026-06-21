// Command server 是 suki-chat 控制平面后端入口。
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
	"github.com/akagiyui/suki-chat/internal/model"
	"github.com/akagiyui/suki-chat/internal/sandbox"
	"github.com/akagiyui/suki-chat/internal/server"
	"github.com/akagiyui/suki-chat/internal/session"
	"github.com/akagiyui/suki-chat/internal/store"
	"github.com/akagiyui/suki-chat/internal/workspace"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	gin.SetMode(gin.ReleaseMode)

	st := store.NewMemoryStore()
	bootstrapAdmin(st, cfg)

	provider, ws := buildSandboxLayer(cfg)
	log.Printf("→ 沙箱后端: %s (mode=%s)", provider.Name(), cfg.Sandbox.Mode)

	client := model.NewDeepSeekClient(cfg.DeepSeek.APIKey, cfg.DeepSeek.BaseURL)
	if cfg.DeepSeek.APIKey == "" {
		log.Printf("⚠ 未配置 DeepSeek API Key（SUKI_CHAT_DEEPSEEK_API_KEY / DEEPSEEK_API_KEY），agent 调用将失败")
	}

	mgr := session.NewManager(st, provider, ws, client, session.Config{
		Image:     cfg.Sandbox.Image,
		CPUs:      1.0,
		MemoryMB:  512,
		PidsLimit: 256,
		Network:   cfg.Sandbox.Network,
		MaxIters:  8,
	})

	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)
	srv := server.New(st, tokens, mgr, cfg)

	log.Printf("→ 监听 %s", cfg.ListenAddr)
	if err := srv.Router().Run(cfg.ListenAddr); err != nil {
		log.Fatalf("服务器退出: %v", err)
	}
}

// buildSandboxLayer 根据配置组装沙箱 Provider 与工作区存储（组装入口/composition root）。
// 这里是"先用 Docker 跑 MVP、日后可换后端/存储"的唯一拼装点。
func buildSandboxLayer(cfg config.Config) (sandbox.Provider, workspace.Store) {
	if cfg.Sandbox.Mode == "docker" {
		dp := sandbox.NewDockerProvider("docker/local", cfg.Sandbox.DockerHost)
		if !dp.Healthy(context.Background()) {
			log.Printf("⚠ Docker 不可用（%s），回退到 local 沙箱（仅供开发，无隔离）", cfg.Sandbox.DockerHost)
			return sandbox.NewLocalProvider(), workspace.NewLocalDirStore(localWorkspaceDir())
		}
		return dp, workspace.NewLocalVolumeStore(dp, cfg.Workspace.VolumePrefix)
	}
	return sandbox.NewLocalProvider(), workspace.NewLocalDirStore(localWorkspaceDir())
}

func localWorkspaceDir() string {
	dir := filepath.Join(os.TempDir(), "suki-chat-workspaces")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// bootstrapAdmin 在首次启动时创建管理员账号（若不存在）。
func bootstrapAdmin(st *store.MemoryStore, cfg config.Config) {
	ctx := context.Background()
	if _, err := st.Users().GetByEmail(ctx, cfg.AdminEmail); err == nil {
		return
	}
	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}
	admin := &store.User{
		ID:           "admin",
		Email:        cfg.AdminEmail,
		PasswordHash: hash,
		Role:         store.RoleAdmin,
		QuotaTokens:  cfg.DefaultQuotaTokens * 100, // 管理员给足配额
		CreatedAt:    time.Now(),
	}
	if err := st.Users().Create(ctx, admin); err != nil {
		log.Printf("⚠ 创建管理员失败: %v", err)
		return
	}
	log.Printf("→ 已创建管理员账号: %s", cfg.AdminEmail)
}
