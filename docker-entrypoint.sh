#!/bin/sh
# Unified entrypoint: automatically starts embedded PostgreSQL if
# SUKI_CHAT_DATABASE_DSN is not provided, otherwise uses the external one.
set -e

# ---------------------------------------------------------------
# 1. Optionally initialize & start embedded PostgreSQL
# ---------------------------------------------------------------
if [ -z "${SUKI_CHAT_DATABASE_DSN:-}" ]; then
    echo "→ SUKI_CHAT_DATABASE_DSN not set, starting embedded PostgreSQL..."

    POSTGRES_USER="${POSTGRES_USER:-suki}"
    POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-suki}"
    POSTGRES_DB="${POSTGRES_DB:-suki}"
    PGDATA="${PGDATA:-/var/lib/postgresql/data}"

    # 自动检测已安装的 PostgreSQL 版本（Containerfile 控制安装哪个版本）
    PG_VER=""
    PG_BIN=""
    for ver in 18 17 16 15 14; do
        if [ -x "/usr/libexec/postgresql${ver}/initdb" ]; then
            PG_VER="$ver"
            PG_BIN="/usr/libexec/postgresql${ver}"
            break
        fi
    done
    if [ -z "$PG_BIN" ]; then
        echo "ERROR: PostgreSQL binaries not found"
        exit 1
    fi
    echo "→ PostgreSQL 版本: $PG_VER"

    INITDB="$PG_BIN/initdb"
    PG_CTL="$PG_BIN/pg_ctl"

    # 检查数据目录版本，不匹配则报错退出
    if [ -f "$PGDATA/PG_VERSION" ]; then
        DATA_VER=$(cat "$PGDATA/PG_VERSION")
        if [ "$DATA_VER" != "$PG_VER" ]; then
            echo "ERROR: 数据目录是 PG $DATA_VER，当前镜像是 PG $PG_VER，版本不兼容！"
            echo "→ 请先使用 PG $DATA_VER 的旧镜像导出数据，再导入到 PG $PG_VER。"
            echo "→ 或者删除旧数据卷重新初始化: docker volume rm <volume_name>"
            exit 1
        fi
    fi

    if [ ! -f "$PGDATA/PG_VERSION" ]; then
        echo "→ Initializing PostgreSQL data directory..."
        mkdir -p "$PGDATA"
        chown -R postgres:postgres "$PGDATA"
        su-exec postgres "$INITDB" -D "$PGDATA" --auth-host=md5 --auth-local=trust
        echo "listen_addresses = '*'" >> "$PGDATA/postgresql.conf"

        # 通过单用户模式创建用户和数据库（无需 psql 客户端）
        echo "→ Creating user and database..."
        su-exec postgres "$PG_BIN/postgres" --single -D "$PGDATA" postgres <<SQL
CREATE USER $POSTGRES_USER WITH PASSWORD '$POSTGRES_PASSWORD';
CREATE DATABASE $POSTGRES_DB OWNER $POSTGRES_USER;
SQL
    fi

    mkdir -p /run/postgresql
    chown postgres:postgres /run/postgresql

    echo "→ Starting PostgreSQL..."
    su-exec postgres "$PG_CTL" -D "$PGDATA" -w start

    export SUKI_CHAT_DATABASE_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@localhost:5432/${POSTGRES_DB}?sslmode=disable"
else
    echo "→ Using external database: ${SUKI_CHAT_DATABASE_DSN}"
fi

# ---------------------------------------------------------------
# 2. Start Go backend in background
# ---------------------------------------------------------------
echo "→ Starting Go backend..."
/usr/local/bin/server &

# ---------------------------------------------------------------
# 3. Start Caddy in foreground (logs to stdout)
# ---------------------------------------------------------------
echo "→ Starting Caddy..."
exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
