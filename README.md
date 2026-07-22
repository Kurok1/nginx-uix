# Nginx UIX

Nginx UIX 是面向单节点 HTTP/HTTPS Nginx 的安全管理界面。当前版本为 **v0.6.0 发布候选版**：v0.1–v0.5 的功能闭环已经完成，v0.6 不新增产品功能，只处理安全审计、升级与灾备、Nginx 兼容回归、跨模块浏览器验收、错误文案一致性和发布文档。

## 当前能力

- 运行状态、真实 Nginx build/进程与启动校验证据；
- Nginx 配置工作区、文件/分组、diff、完整候选校验、安全发布与自动回滚；
- upstream、server/location 的结构化管理，未知语法保持无损；
- release、不可变备份、restore、restart、retention、审计与 `needs_attention` 处置；
- Route Lab 静态预测与隔离 Nginx 路径实验，不 reload 生产实例；
- Let's Encrypt HTTP-01 与受限 Cloudflare API Token DNS-01，包含签发、绑定、续期、部署、取消、清理和历史。

v1.0 范围固定为单节点 Nginx。项目不提供集群、Kubernetes、WAF、Nginx Plus、任意 Shell、Docker Socket 或 UI/受管 Nginx 分离容器方案。

## 核心安全边界

- `/etc/nginx` 始终是配置真源；SQLite 不保存可反向覆盖生产文件的配置副本。
- 所有生产配置变更必须经过工作副本、完整 diff、`nginx -t`、不可变备份、原子发布、reload 健康确认和失败恢复。
- Agent 只监听固定 Unix Socket，只接受 UID `10001` peer 和精确 path+method 的类型化白名单；不接受命令、可执行路径、任意文件路径、PID、signal 或 URL。
- Session 只持久化 digest；业务 mutation 同时要求 HttpOnly Cookie、可信 Origin 和当前 CSRF。
- 登录同时限制“用户名 + 直接来源”和同一来源的跨用户名喷洒，不信任 `X-Forwarded-For`。
- 密码、Cookie、Token、私钥、请求 Body、配置正文和 diff 正文不会进入普通日志或公共错误。
- 无法确认发布、回滚、证书清理或运行状态时进入 `needs_attention`，不会报告成功。

## 发布与 Docker 边界

v0.6.0 已覆盖源码、SQLite、原生进程、浏览器和真实隔离 Nginx 验收。**本版本没有执行 Docker 构建、容器运行、volume 升级、容器故障注入或 multiarch 验证。** `deploy/docker/Dockerfile` 描述最终一体化镜像接口，但只有 v0.7.0 完整镜像验收后才构成生产安装证据。

目标镜像固定暴露 Nginx `80/443` 和 UI `9000`，并持久化：

| 路径 | 内容 |
| --- | --- |
| `/etc/nginx` | Nginx 配置真源 |
| `/var/lib/nginx-uix` | SQLite、工作区、备份、证书材料和任务证据 |

`/run/nginx-uix` 只保存运行期 Socket/PID，不是持久卷。

## 源码验收

固定工具链为 Go `1.26.4`、Node.js `24.17.0`、npm `11.13.0`。前端只使用项目级 `web/node_modules`。

```sh
go mod verify
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
npm --prefix web run test:e2e
```

真实 Nginx 兼容测试使用随机回环端口和临时 prefix：

```sh
NGINX_UIX_INTEGRATION=1 NGINX_BIN=/absolute/path/to/nginx \
  go test ./tests/integration -count=1 -v
```

这些测试不得指向生产配置或 production reload 包装脚本。

## 健康接口

| 端点 | 语义 |
| --- | --- |
| `GET /health/live` | UI HTTP 进程可响应，不代表 Nginx 健康 |
| `GET /health/ready` | SQLite、Agent、真实 Nginx 和永久恢复状态均可确认 |

readiness 失败返回 503。只要 liveness 仍可用，管理员可登录查看脱敏、可操作的状态证据。

## 文档入口

- [v0.6.0 发布候选设计](docs/superpowers/specs/2026-07-22-v0.6-release-candidate-design.md)
- [安装与验收](docs/operations/installation.md)
- [升级与回滚](docs/operations/upgrade-and-rollback.md)
- [冷备与灾难恢复](docs/operations/backup-and-disaster-recovery.md)
- [故障排查](docs/operations/troubleshooting.md)
- [v0.6.0 验收记录](docs/release/v0.6.0-verification.md)
- [版本范围与路线图](PLAN.md)
- [视觉与交互规范](DESIGN.md)
- [REST API 契约](api/v1/openapi.yaml)

历史版本的设计与验收记录继续保留在 `docs/superpowers/specs/` 和 `docs/release/`，但不能替代 v0.6.0 当前工作树的复验。
