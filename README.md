# anvilkit-auth-template

Open-source style starter template for a multi-tenant auth platform built from day one with two microservices:

- `auth-api` (user auth, token lifecycle)
- `admin-api` (tenant-scoped admin RBAC APIs)

Tech stack: **Go 1.22**, **Gin**, **PostgreSQL**, **Redis**.

## Quick start

```bash
cp .env.example .env
make init
make smoke
```

## Services

- auth-api: `http://localhost:8080`
- admin-api: `http://localhost:8081`

## Highlights

- Unified JSON envelope response with request ID.
- Middleware-driven centralized error handling.
- JWT access + refresh rotation with hashed refresh token persistence.
- Casbin RBAC for admin APIs.
- Docker Compose one-command bootstrap.

See `docs/` for architecture and API details.

## 部署指南（Docker Compose + GitHub Actions）

本仓库新增了 `.github/workflows/deploy.yml`，用于将 `auth-api` 与 `admin-api` 镜像构建并推送到 GHCR，再通过 SSH 在目标 Linux 服务器上执行容器部署。

### 1) 服务器准备

1. 安装 Docker 与 Docker Compose Plugin（`docker compose version` 可用）。
2. 创建部署目录（示例）：`/opt/anvilkit-auth-template`。
3. 在部署目录准备生产环境变量文件：`/opt/anvilkit-auth-template/.env`，至少包含：
   - `DB_DSN`
   - `REDIS_ADDR`
   - `JWT_SECRET`
   - `CORS_ALLOW_ORIGINS`
   - `CORS_ALLOW_CREDENTIALS`
4. 如果 DB/Redis 走外部服务，`DB_DSN/REDIS_ADDR` 指向外部地址，并在 GitHub Environment Variables 中设置 `USE_INTERNAL_DEPS=false`。
5. 确保服务器可拉取 GHCR 镜像（镜像私有时，请预先 `docker login ghcr.io`）。

> 说明：生产部署使用 `deploy/docker-compose.prod.yml`，其中包含 `migrate` 一次性迁移服务；部署时会先执行迁移，再启动 API 容器。

### 2) GitHub Environments 与 Secrets 配置

建议在 GitHub 仓库中创建 Environment（至少 `production`，可选 `staging`），并在每个 Environment 下配置：

必需 Secrets：
- `DEPLOY_SSH_KEY`：部署机私钥（建议专用 deploy key）
- `DEPLOY_HOST`：服务器地址
- `DEPLOY_USER`：SSH 用户
- `DEPLOY_PATH`：远程部署目录（如 `/opt/anvilkit-auth-template`）
- `DEPLOY_PORT`：可选，默认 22

可选 Variables：
- `USE_INTERNAL_DEPS`：`true`（默认，启动 compose 内 pg/redis）或 `false`（使用外部 DB/Redis）

### 3) 触发部署

支持两种触发方式：
- `workflow_dispatch` 手动触发（默认，支持选择 `production/staging`）
- 推送版本 tag（`v*`）触发自动部署（默认走 `production` environment）

部署流程：
1. 在 CI 构建并推送镜像：
   - `ghcr.io/<owner>/anvilkit-auth-template-auth-api:<tag>`
   - `ghcr.io/<owner>/anvilkit-auth-template-admin-api:<tag>`
2. 上传 `docker-compose.prod.yml`、`remote_deploy.sh`、迁移 SQL 到服务器。
3. 远程执行：
   - `docker compose run --rm migrate`
   - `docker compose pull && docker compose up -d`
4. 健康检查：
   - `curl -fsS http://127.0.0.1:8080/healthz`
   - `curl -fsS http://127.0.0.1:8081/healthz`
5. 健康检查失败自动回滚到上一版本 tag。

### 4) 回滚机制

远程脚本会在 `${DEPLOY_PATH}/.deploy_state` 维护：
- `current_tag`
- `prev_tag`

当部署失败或健康检查失败时，脚本自动尝试回滚到 `prev_tag/current_tag` 并重新拉起服务。

### 5) 可观测性与排障

常用命令（在服务器部署目录执行）：

```bash
docker compose -f deploy/docker-compose.prod.yml --env-file .env ps
docker compose -f deploy/docker-compose.prod.yml --env-file .env logs -f auth-api
docker compose -f deploy/docker-compose.prod.yml --env-file .env logs -f admin-api
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8081/healthz
```

常见问题：
- **拉取镜像失败（401/403）**：检查服务器是否已登录 GHCR 或镜像是否公开。
- **迁移失败**：检查 `.env` 中 `DB_DSN` 连通性与权限；确认目标库可执行 SQL。
- **健康检查失败**：查看 `auth-api/admin-api` 日志，确认依赖（DB/Redis、JWT_SECRET、CORS）配置完整。
