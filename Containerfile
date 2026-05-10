# =============================================================================
# suki-tab - Unified Containerfile
#
# 构建目标（通过 --target 选择）：
#   all-in-one  — 内嵌 PostgreSQL，适合单机部署
#   external-db — 使用外部数据库，适合生产环境
#
# 默认构建目标为 external-db（最后一个阶段）。
# 构建示例：
#   docker build --target all-in-one -t suki-chat:all-in-one .
#   docker build --target external-db -t suki-chat:external-db .
# =============================================================================

# ---- Stage 1: 构建前端 ----
FROM node:lts-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY frontend/ .
RUN corepack enable && pnpm build

# ---- Stage 2: 构建 Go 后端 ----
FROM golang:alpine AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 go build -o /app/server ./cmd/server

# ---- Stage 3a: All-in-One（内嵌 PostgreSQL）----
FROM alpine:3 AS all-in-one

# 安装 PostgreSQL 18、Caddy 及基础工具
RUN apk add --no-cache \
    postgresql18 \
    caddy \
    ca-certificates \
    tzdata \
    su-exec \
    bash

# 复制构建产物
COPY --from=frontend-builder /app/frontend/dist /usr/share/caddy
COPY --from=backend-builder /app/server /usr/local/bin/server
COPY Caddyfile /etc/caddy/Caddyfile
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 80

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]

# ---- Stage 3b: External DB（使用外部数据库）----
FROM alpine:3 AS external-db

# 仅安装 Caddy 及基础工具
RUN apk add --no-cache \
    caddy \
    ca-certificates \
    tzdata \
    bash

# 复制构建产物
COPY --from=frontend-builder /app/frontend/dist /usr/share/caddy
COPY --from=backend-builder /app/server /usr/local/bin/server
COPY Caddyfile /etc/caddy/Caddyfile
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 80

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
