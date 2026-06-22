# suki-chat · 云端 AI Agent SaaS（MVP）

一个开箱即用的云端 AI Agent 平台：用户注册登录后创建会话，每个会话在**隔离容器**中运行，
Agent 可自主使用工具（联网抓取、Shell 执行）完成任务。**关闭页面也不会中断**——运行在服务端，
通过 SSE 事件流随时回看进度、断线重连回放。多个会话可并行运行，互不影响。

> 当前为 MVP（第一版，供迭代）。已端到端跑通：注册 → 建会话 → 发任务 → Agent 调用 DeepSeek
> → 在真实 Docker 容器内执行工具 → SSE 流式回传 → token 计量扣减。

## 架构

```
SolidJS 前端 ── Caddy ── Go 控制平面 ──(Sandbox 接口)── 会话容器(隔离, 每会话一个)
                              │                              └ web_fetch / run_shell 工具
                              ├ 认证(JWT/argon2) · 会话 API · SSE 流式网关
                              ├ 模型网关(DeepSeek, 计量/配额) · Admin
                              └ 事件存储(append-only, 回放)
```

| 层 | 技术 | 说明 |
|---|---|---|
| 前端 | SolidJS + TanStack Router + Tailwind 4 | Hero/登录/注册/会话/Admin，移动端响应式 |
| 控制平面 | Go 1.26 + Gin | 认证、会话生命周期、SSE、计费、Admin |
| 沙箱 | Docker Engine API（unix socket） | 每会话一个隔离容器，cap-drop/no-new-privileges/资源上限 |
| 模型网关 | DeepSeek（OpenAI 兼容） | 统一调用、token 计量、内部配额 |
| 存储 | 内存（MVP） | 接口化，后续可换 PostgreSQL |

### 关键设计接缝（为日后升级预留）

- **`sandbox.Provider` / `sandbox.Sandbox`**：会话运行环境抽象。MVP 是本地 Docker；
  加远程主机 = 多注册一个 Provider；规模化可换 microVM / K8s / 托管沙箱，业务零改动。
- **`workspace.Store`**：工作区存储抽象。MVP 是本地 Docker 卷（`LocalVolumeStore`，
  休眠 Snapshot/Release 为 no-op）；多机时换对象存储快照实现即可，生命周期代码不变。
- **`agent` 事件协议**：Agent 每步写入 append-only 事件日志，前端 SSE 订阅 + 按 `last_seq` 回放。
  这是"关页面不丢 / 多端查看 / 审计"的统一机制。

### 与目标架构的差异（MVP 取舍，已在代码注释标注）

- **Agent 运行时跑在每会话容器内（pi）**：控制平面只编排——按会话起一个 `suki-runner`（Node+pi）
  容器，转发用户消息、收集容器上报的事件。模型经控制平面计量代理调用（容器不持密钥、不直连上游）。
  bash / web_fetch / 截图工具都在容器内执行。
- **浏览器**：默认每用户共享一个 CloakBrowser 容器；创建会话时勾选「独立浏览器」则该会话单独一个。
  runner 与浏览器同处私有网络 `suki-net`，按容器名直连。
- **容器治理**：控制平面只管理自己创建的容器（`suki.managed` 标签）；空闲自动回收；管理员可查看
  各用户活跃容器。基础设施（如 Postgres）不带标签，绝不被触碰。
- **持久化**：设置 `SUKI_CHAT_DATABASE_DSN` 用 PostgreSQL（用户/会话/事件落库，重启不丢）；
  留空则内存存储。PostgreSQL 属基础设施，独立运行，控制平面只作客户端连接、**绝不**纳入容器治理。

## 本地运行

需要本机可用的 Docker。先准备两个会话容器镜像：

```bash
docker build -t suki-runner:dev ./runner        # 会话 runner（pi 运行时）
docker pull cloakhq/cloakbrowser:latest         # 浏览器（截图用，约 2.4GB）
```

### 后端

```bash
export DEEPSEEK_API_KEY=sk-xxxx          # DeepSeek 密钥（仅本地，勿提交）
go run ./cmd/server                       # 监听 :8182；启动时自动创建 suki-net 网络
```

默认会创建管理员账号 `admin@example.com` / `admin12345`（可用环境变量覆盖）。

### 前端

```bash
cd frontend
corepack enable && pnpm install
pnpm dev                                  # :5173，已配置 /api 代理到 :8182
```

### 容器（一体化镜像）

```bash
docker build --target all-in-one -t suki-chat:all-in-one .
docker run -p 80:80 -e DEEPSEEK_API_KEY=sk-xxxx \
  -v /var/run/docker.sock:/var/run/docker.sock suki-chat:all-in-one
```

## 配置（环境变量，前缀 `SUKI_CHAT_`）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SUKI_CHAT_LISTEN_ADDR` | `:8182` | 后端监听地址 |
| `SUKI_CHAT_JWT_SECRET` | dev 值 | JWT 密钥，**生产必须设置** |
| `SUKI_CHAT_DEEPSEEK_API_KEY` / `DEEPSEEK_API_KEY` | 空 | DeepSeek 密钥 |
| `SUKI_CHAT_DEEPSEEK_FAST_MODEL` | `deepseek-v4-flash` | 轻量模型 |
| `SUKI_CHAT_DEEPSEEK_PRO_MODEL` | `deepseek-v4-pro` | 强力模型 |
| `SUKI_CHAT_RUNNER_IMAGE` | `suki-runner:dev` | 会话 runner 镜像（pi 运行时） |
| `SUKI_CHAT_BROWSER_IMAGE` | `cloakhq/cloakbrowser:latest` | 浏览器镜像（截图） |
| `SUKI_CHAT_SANDBOX_NETWORK` | `suki-net` | runner 与浏览器同处的私有网络 |
| `SUKI_CHAT_CONTROL_URL` | `http://host.docker.internal:8182` | 容器回连控制平面地址 |
| `SUKI_CHAT_IDLE_TIMEOUT` | `15m` | 空闲多久回收容器 |
| `SUKI_CHAT_DEFAULT_QUOTA_TOKENS` | `1000000` | 新用户默认 token 配额 |
| `SUKI_CHAT_ADMIN_EMAIL` / `_PASSWORD` | 见上 | 引导管理员账号 |

## API 概览

- `POST /api/auth/register` · `POST /api/auth/login` · `GET /api/me`
- `GET/POST /api/sessions` · `GET/DELETE /api/sessions/:id`
- `POST /api/sessions/:id/messages` — 发任务（异步，立即返回）
- `GET /api/sessions/:id/events?last_seq=N` — SSE 事件流（回放 + 实时）
- `POST /api/sessions/:id/hibernate` — 休眠
- `GET /api/admin/users` · `GET /api/admin/sessions` —（管理员）

## 测试

```bash
go test ./...                                    # 全部后端测试（含 Docker 沙箱集成测试）
DEEPSEEK_API_KEY=sk-xxxx go test ./internal/model/ -run Integration   # DeepSeek 真实调用
cd frontend && pnpm exec tsc -b && pnpm build    # 前端类型检查 + 构建
```

CI（`.github/workflows/build.yml`）：先跑 `test`（Go 测试 + 前端 lint/类型/构建），
通过后再 `build` 并发布镜像。
