// Command server 是 suki-chat 控制平面后端入口。
package main

import (
	"context"
	"log"
	"time"

	"github.com/akagiyui/suki-chat/internal/auth"
	"github.com/akagiyui/suki-chat/internal/config"
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

	// 存储：设置 DSN 用 PostgreSQL（基础设施，独立运行；控制平面只作客户端连接），否则内存。
	var st store.Store
	if cfg.DatabaseDSN != "" {
		pg, err := store.NewPostgresStore(context.Background(), cfg.DatabaseDSN)
		if err != nil {
			log.Fatalf("连接 PostgreSQL 失败: %v", err)
		}
		st = pg
		log.Printf("→ 存储: PostgreSQL")
	} else {
		st = store.NewMemoryStore()
		log.Printf("→ 存储: 内存（重启丢失；设置 SUKI_CHAT_DATABASE_DSN 以持久化）")
	}
	bootstrapAdmin(st, cfg)

	// Docker 是会话 runner 容器的运行后端（必需）。
	dp := sandbox.NewDockerProvider("docker/local", cfg.Sandbox.DockerHost)
	if !dp.Healthy(context.Background()) {
		log.Printf("⚠ Docker 不可用（%s）——会话将无法启动。请检查 docker。", cfg.Sandbox.DockerHost)
	}
	// runner 与浏览器容器同处此私有网络，runner 可按容器名直连浏览器。
	if err := dp.EnsureNetwork(context.Background(), cfg.Sandbox.Network); err != nil {
		log.Printf("⚠ 创建网络 %s 失败: %v", cfg.Sandbox.Network, err)
	}
	ws := workspace.NewLocalVolumeStore(dp, cfg.Workspace.VolumePrefix)
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)

	if cfg.DeepSeek.APIKey == "" {
		log.Printf("⚠ 未配置 DeepSeek API Key（SUKI_CHAT_DEEPSEEK_API_KEY / DEEPSEEK_API_KEY），模型调用将失败")
	}

	mgr := session.NewManager(st, dp, ws, tokens, session.Config{
		RunnerImage:  cfg.Sandbox.RunnerImage,
		BrowserImage: cfg.Sandbox.BrowserImage,
		ControlURL:   cfg.ControlURL,
		CPUs:         1.0,
		MemoryMB:     1024,
		PidsLimit:    512,
		Network:      cfg.Sandbox.Network,
		IdleTimeout:  cfg.IdleTimeout,
		ArtifactsDir: cfg.ArtifactsDir,
	})
	mgr.StartReaper(context.Background())
	log.Printf("→ runner 镜像: %s；回连地址: %s；空闲回收: %s", cfg.Sandbox.RunnerImage, cfg.ControlURL, cfg.IdleTimeout)

	srv := server.New(st, tokens, mgr, cfg)
	log.Printf("→ 监听 %s", cfg.ListenAddr)
	if err := srv.Router().Run(cfg.ListenAddr); err != nil {
		log.Fatalf("服务器退出: %v", err)
	}
}

// bootstrapAdmin 在首次启动时创建管理员账号（若不存在）。
func bootstrapAdmin(st store.Store, cfg config.Config) {
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
