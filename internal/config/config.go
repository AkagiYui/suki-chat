// Package config 负责从环境变量加载运行时配置。
// 所有环境变量统一使用 SUKI_CHAT_ 前缀（与容器入口脚本保持一致）。
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 是整个服务的运行时配置。
type Config struct {
	ListenAddr string // 后端监听地址，默认 :8182

	JWTSecret string        // JWT 签名密钥，生产环境必须显式设置
	JWTTTL    time.Duration // access token 有效期

	DeepSeek  DeepSeekConfig  // 上游模型（DeepSeek，OpenAI 兼容）
	Sandbox   SandboxConfig   // 会话沙箱（容器）配置
	Workspace WorkspaceConfig // 工作区存储配置
	Browser   BrowserConfig   // 隐身浏览器服务（CloakBrowser，CDP）

	ArtifactsDir string // 会话工件（截图等）落盘目录

	ControlURL  string        // 会话容器回连控制平面的地址
	IdleTimeout time.Duration // 空闲多久回收 runner 容器

	DefaultQuotaTokens int64 // 新用户默认 token 配额（内部配额制，不接真实支付）

	// 首个管理员账号引导：服务启动时若不存在则自动创建
	AdminEmail    string
	AdminPassword string
}

// DeepSeekConfig 描述上游模型网关配置。
type DeepSeekConfig struct {
	APIKey    string
	BaseURL   string // 默认 https://api.deepseek.com
	FastModel string // 轻量模型，默认 deepseek-v4-flash
	ProModel  string // 强模型，默认 deepseek-v4-pro
}

// SandboxConfig 描述会话运行环境。
type SandboxConfig struct {
	Mode        string // docker | local
	DockerHost  string // 默认 unix:///var/run/docker.sock
	RunnerImage string // 会话 runner 镜像（pi 运行时），默认 suki-runner:dev
	Network     string // 容器网络，默认 bridge
}

// WorkspaceConfig 描述工作区存储模式。
type WorkspaceConfig struct {
	Mode         string // local | snapshot（MVP 仅实现 local）
	VolumePrefix string // Docker 卷名前缀
}

// BrowserConfig 描述隐身浏览器服务（CloakBrowser，CDP 端点）。
type BrowserConfig struct {
	// CDP 端点，形如 http://127.0.0.1:9222。为空则禁用浏览器/截图工具。
	CDPURL string
}

// Load 从环境变量读取配置，未设置项使用合理默认值。
func Load() Config {
	return Config{
		ListenAddr: env("SUKI_CHAT_LISTEN_ADDR", ":8182"),
		JWTSecret:  env("SUKI_CHAT_JWT_SECRET", "dev-insecure-secret-change-me"),
		JWTTTL:     envDuration("SUKI_CHAT_JWT_TTL", 24*time.Hour),
		DeepSeek: DeepSeekConfig{
			// 兼容裸 DEEPSEEK_API_KEY，方便本地测试
			APIKey:    firstNonEmpty(os.Getenv("SUKI_CHAT_DEEPSEEK_API_KEY"), os.Getenv("DEEPSEEK_API_KEY")),
			BaseURL:   env("SUKI_CHAT_DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
			FastModel: env("SUKI_CHAT_DEEPSEEK_FAST_MODEL", "deepseek-v4-flash"),
			ProModel:  env("SUKI_CHAT_DEEPSEEK_PRO_MODEL", "deepseek-v4-pro"),
		},
		Sandbox: SandboxConfig{
			Mode:        env("SUKI_CHAT_SANDBOX_MODE", "docker"),
			DockerHost:  env("SUKI_CHAT_DOCKER_HOST", "unix:///var/run/docker.sock"),
			RunnerImage: env("SUKI_CHAT_RUNNER_IMAGE", "suki-runner:dev"),
			Network:     env("SUKI_CHAT_SANDBOX_NETWORK", "bridge"),
		},
		Workspace: WorkspaceConfig{
			Mode:         env("SUKI_CHAT_WORKSPACE_MODE", "local"),
			VolumePrefix: env("SUKI_CHAT_WORKSPACE_PREFIX", "suki-ws-"),
		},
		Browser: BrowserConfig{
			// 默认指向本机 9222；需运行 CloakBrowser 容器后才可用，否则截图工具会优雅报错
			CDPURL: env("SUKI_CHAT_BROWSER_CDP", "http://127.0.0.1:9222"),
		},
		ArtifactsDir:       env("SUKI_CHAT_ARTIFACTS_DIR", filepath.Join(os.TempDir(), "suki-chat-artifacts")),
		ControlURL:         env("SUKI_CHAT_CONTROL_URL", "http://host.docker.internal:8182"),
		IdleTimeout:        envDuration("SUKI_CHAT_IDLE_TIMEOUT", 15*time.Minute),
		DefaultQuotaTokens: envInt64("SUKI_CHAT_DEFAULT_QUOTA_TOKENS", 1_000_000),
		AdminEmail:         env("SUKI_CHAT_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword:      env("SUKI_CHAT_ADMIN_PASSWORD", "admin12345"),
	}
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
