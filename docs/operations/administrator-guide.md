# Nginx UIX v1.0.0 管理员手册

## 部署模型

官方部署是一体化 Linux 容器，包含 UI、特权 Agent、s6 监督器和受管理 Nginx。不得拆成 UI/Nginx 官方双容器，不挂载 Docker Socket，不使用 `--privileged`。

固定接口：

| 接口 | 约束 |
| --- | --- |
| Nginx | 容器 `80/443` |
| UI | 容器 `9000`；宿主默认只映射 `127.0.0.1` |
| Agent | `/run/nginx-uix/agent.sock`，不暴露 TCP |
| 配置真源 | `/etc/nginx` 持久根 |
| UIX 数据 | `/var/lib/nginx-uix` 持久根 |
| UI 身份 | `10001:10001` |
| Agent Socket | `0:10002`、`0660` |

安装命令、Secret 与 capability 见[安装与验收](installation.md)。

## 最小权限

容器使用 `--cap-drop ALL`，只添加：

```text
CHOWN,DAC_OVERRIDE,FOWNER,KILL,SETGID,SETUID
```

当前 native 验收不需要 `NET_BIND_SERVICE`。不要增加 host PID/network、设备或 Docker Socket。UI 以非 root 运行；Agent 为类型化特权边界，不接受命令、可执行路径、任意文件路径、signal 或 URL。

证书私钥、数据库和管理员 Secret 不高于 `0600`，敏感目录不高于 `0700`。启动时权限无法确认会 fail closed。

## 网络与可信 Origin

UI 默认只映射宿主回环地址。需要远程管理时，在受控 HTTPS 入口后设置精确 `NGINX_UIX_PUBLIC_URL`。值必须是单一 `http://` 或 `https://` origin，不能包含凭据、query、fragment 或业务路径。

不要依赖 `X-Forwarded-For` 作为登录限流身份，也不要用转发 Header 动态改变可信 origin。管理入口应限制来源网络并保留 TLS 终止层自己的访问审计。

## 启动与停止

启动后同时检查：

```sh
curl --fail --show-error http://127.0.0.1:9000/health/live
curl --fail --show-error http://127.0.0.1:9000/health/ready
docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' nginx-uix
```

正常停止使用 `docker stop --time 15 nginx-uix`。PID 1 必须转发信号并等待 UI、Agent、Nginx、证书调度器和持久任务 owner 退出。日常维护不使用 `docker kill`。

重建时必须同时重新挂载原 `/etc/nginx` 与 `/var/lib/nginx-uix`。只有 `/etc/nginx` 完全为空时镜像才写默认配置；不要从 SQLite 反向生成 Nginx 配置。

## 任务与一致性

release、restore、restart、retention、Route Lab 和 certificate task 都有持久状态。维护窗口前：

1. 停止接收新变更；
2. 等待任务终态或通过产品入口取消；
3. 处理或记录所有开放 `needs_attention`；
4. 确认 production lease 没有活动 owner；
5. 再优雅停止容器。

浏览器断开不等于任务停止。不要直接修改 SQLite task state、lease、stage 或 journal。

## 配置与 Nginx

所有生产修改必须经工作副本、diff、完整候选验证、不可变备份、原子发布、reload 和运行确认。校验失败不得 reload。

`nginx -t` 只证明语法与依赖可读，不证明路径匹配。使用 Route Lab 的隔离实例验证 server/location 行为；禁止在生产 Nginx 上运行未发布配置实验。

若外部配置管理仍会写 `/etc/nginx`，必须建立明确变更窗口。UIX 会检测生产摘要变化并把旧工作区置为 `stale`，但不能替外部工具提供跨系统事务。

## 备份、升级与恢复

- 升级/回滚：[v1.0.0 升级与回滚](upgrade-and-rollback.md)
- 故障决策：[故障恢复手册](failure-recovery-guide.md)
- 双根冷备/灾难恢复：[冷备与灾难恢复](backup-and-disaster-recovery.md)
- 故障诊断：[故障排查](troubleshooting.md)

冷备必须同时包含 `/etc/nginx` 和 `/var/lib/nginx-uix`，并保留 owner、group、mode、symlink 和文件摘要。恢复只写空目标，不与现有树逐文件合并。

## 证书运维

- 生产签发前先在 staging 完成匹配预检；
- Cloudflare Token 仅授予目标 Zone Read 与 DNS Write；
- ACME account key、Token 和私钥不进入普通日志、API 错误或前端缓存；
- 自动续期计划从 SQLite 恢复，失败遵守一小时起的有界退避；
- challenge 或 binding 清理不确定时保持 `needs_attention`，不要删除证据或覆盖旧证书。

证书绑定同样走配置发布事务；不得直接写生产配置绕过 diff、备份和 rollback。

## 审计与日志

结构化日志记录动作、对象、结果、耗时、request ID 或 task ID。不得记录密码、Cookie、Authorization、Cloudflare Token、ACME key、私钥、请求 Body、配置正文或 diff 正文。

API 用户错误与内部日志分离。排障共享脱敏 ID 和状态，不共享完整响应/数据库/配置。审计事件、任务历史和不可变备份索引位于 SQLite；Nginx 配置正文始终只以文件为真源。

## 维护节奏

- 每日：health、开放 attention、失败/超时任务、证书续期状态；
- 每次配置发布：diff/check、backup ID、reload/运行证据；
- 每次升级前：完整双根冷备和空目标恢复演练；
- 定期：备份保留 dry-run、磁盘容量、权限、管理员访问路径；
- 发布变更后：核对版本、source fingerprint、本地 image ID、正式 manifest digest 和平台证据。

正式 v1.0.0 digest 与最终平台限制以 `docs/release/v1.0.0-verification.md` 为准；该文件不存在或仍有发布阻断项时，不得把本地候选当成正式产物。
