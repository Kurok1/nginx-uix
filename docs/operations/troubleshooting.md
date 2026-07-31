# Nginx UIX v1.0.0 故障排查

## 先保存证据

排查时先记录时间、应用版本/构建身份、页面显示的 request ID 或 task ID、当前任务状态和 health 结果。普通日志不得加入密码、Session Cookie、CSRF、Authorization、Cloudflare Token、ACME key authorization/TXT、私钥、请求 Body、配置或 diff 正文。

不要把 `failed`、`rolled_back` 和 `needs_attention` 当成同一种失败：

| 状态 | 语义 | 下一步 |
| --- | --- | --- |
| `failed` | 操作失败，页面必须说明生产是否未变 | 修正可操作原因后重新建立 review/plan |
| `rolled_back` | 原状态已恢复且健康证据已确认 | 保存 rollback 证据，再调查原始失败 |
| `needs_attention` | 文件、元数据或运行事实无法唯一确认 | 停止普通生产变更，只使用 Recovery & History 的受控处置 |
| `cancelled` | 服务端已持久化取消终态 | 检查 cleanup 证据；浏览器断开本身不算取消 |
| `timed_out` | 有界任务超时 | 先刷新持久任务证据，不要直接假定后台停止 |

## Health 快速定位

```sh
curl -i http://127.0.0.1:9000/health/live
curl -i http://127.0.0.1:9000/health/ready
```

| live | ready | 解释 |
| --- | --- | --- |
| 200 | 200 | UI、SQLite、Agent 和真实 Nginx readiness 已确认 |
| 200 | 503 | UI 可访问，但 SQLite、Agent、Nginx 或永久恢复状态至少一项不健康 |
| 无响应 | 无响应 | UI 未监听、启动输入无效、数据根不可用或端口冲突 |

readiness 不是进程存活别名。不要降低探针标准或只检查 PID 1。

## 容器 / PID 1

- PID 1 必须是镜像自带的 s6 init。正常停止使用 `docker stop`/`SIGTERM`，等待其转发信号并有界退出；不要把 `docker kill` 当成日常停止。
- 容器使用 `--cap-drop ALL`，只添加 `CHOWN`、`DAC_OVERRIDE`、`FOWNER`、`KILL`、`SETGID`、`SETUID`。不要用 `--privileged`、host PID/network、Docker Socket 或额外设备绕过启动错误。
- UI 进程、Agent、Nginx 与 PID 1 是不同健康对象。容器仍为 running 不代表 readiness；先检查 `/health/live`、`/health/ready`、Docker health 和脱敏后的 `/api/v1/system/status`。
- 重建必须重新挂载同一对 `/etc/nginx` 与 `/var/lib/nginx-uix` volume。缺少任一卷时停止操作，不要从 SQLite 反向生成配置或用默认配置覆盖非空目录。
- 当前 exact v1.0.0 候选只在原生 arm64 完成完整运行验收；amd64 诊断必须在真实 amd64 runner 复核，不能把模拟器限制写成产品成功或失败。

## UI / 登录

- 首次启动失败：检查管理员用户名、密码文件绝对路径、权限、12–128 字符限制和 4 KiB 上限。只要设置了密码文件，读取失败不会回退到明文变量。
- 登录一直失败：确认浏览器使用的 origin 与 `NGINX_UIX_PUBLIC_URL` 精确一致；不要依赖 `X-Forwarded-*` 修正。
- 429：同一用户名+直接来源为 5 次/5 分钟，同一来源跨用户名为 20 次/5 分钟，均阻断 15 分钟。不要删除 throttle 行绕过；等待 `Retry-After` 或调查异常来源。
- 已登录后 401：会话可能达到 8 小时 idle 或 24 小时 absolute；重新认证。Session 只保存 digest，不能从数据库恢复浏览器 token。
- 页面错误：使用可见 request ID 对应结构化日志。UI 不应显示后端内部 message、路径或 stack。

## Agent / Unix Socket

固定 Socket 为 `/run/nginx-uix/agent.sock`。在 Linux 上检查：

```sh
stat -c '%F %u:%g %a %n' /run/nginx-uix/agent.sock
```

期望 Unix Socket、owner/group `0:10002`、mode `660`。以下任一情况都会 fail closed：旧路径是普通文件/symlink、GID 不是 10002、`chown`/`chmod` 后复核不一致、peer UID 不是 10001、凭据不可读。

Agent API 没有 `/exec`、`/bin/sh`、任意路径、任意 signal 或 query 参数入口。遇到未知路由 404/405 时不要添加 shell 转发作为修复，应确认 UI 与 Agent 版本一致。

## Nginx / 配置发布

- Dashboard 显示 invalid：保留脱敏诊断，在隔离环境运行完整 `nginx -t`；不要 reload。
- 工作区 `stale`：生产摘要已变化。旧草稿只读，从当前生产重新创建工作区，不做自动 rebase。
- publish `failed`：生产通常未改变或已安全回滚，按阶段和 error code 判断；重新发布必须重新 review 和 check。
- publish `rolled_back`：保存 release/backup ID 和运行健康证据，再处理候选错误。
- publish `needs_attention`：普通编辑和发布保持阻断。不要手工改状态；使用 verified restore、fixed restart 或 current-state verification。
- 配置路径/符号链接错误：确认 canonical path 仍在允许根。不要扩大到 `/` 或通过 symlink 绕过。

`nginx -t` 只证明语法与依赖可读，不能证明 location 匹配。路径行为必须使用 Route Lab 的隔离实例。

## SQLite / 持久数据

先停 UI/Agent，再在副本或只读 URI 上检查：

```sh
sqlite3 'file:/absolute/path/to/nginx-uix.db?mode=ro' \
  'PRAGMA integrity_check; PRAGMA foreign_key_check; SELECT version FROM schema_migrations ORDER BY version;'
```

v1.0.0 期望 migration `1..7`。不要删除 `-wal`、手改 migration checksum、导出后重建 schema 或让两个版本同时写一个数据库。出现只读目录、磁盘满或 integrity 失败时，停止写入并按灾难恢复流程处理。

## Route Lab

- 静态分析是 prediction，不是运行证据；页面将二者分区显示。
- 运行测试只连接随机回环沙箱，Host/SNI 不改变连接目标；生产 Nginx 不 reload。
- POST/Body/敏感 Header 需要精确二次确认。历史不会保存 Body 或敏感 Header 值。
- SSE 断开只表示 UI 重连，任务仍由服务端拥有。刷新 task 状态，不要把断开当取消。
- `ROUTE_CLEANUP_FAILED` 或 cleanup 证据不完整时停止重试，确认 master、端口和 stage 目录已清理。

## 证书 / ACME / Cloudflare

- wildcard 必须使用 Cloudflare DNS-01；HTTP-01 不接受 wildcard。
- Cloudflare 只支持受限 API Token，权限至少为目标 Zone 的 Zone Read 与 DNS Write；不支持 Global API Key。
- Token/private key 提交后不会返回浏览器。不要把它们粘到日志或工单。
- production 签发需要匹配的 staging 证据或精确风险短语；plan 过期后重新 review。
- rate limit/propagation timeout：遵守 backoff，先检查权威 DNS，不要消耗 production 配额反复试探。
- `needs_attention`/challenge cleanup failure：旧证书可能仍在服务，但清理无法确认。保留 task/certificate/request ID，停止同一资源的新任务。
- 证书仍被 Nginx 引用时删除必须失败；先经过可见 binding diff、完整发布和引用复核。

## 何时恢复而不是修补

出现以下任一情况应隔离当前状态并使用已验证冷备恢复：

- SQLite integrity 或 foreign key 失败；
- migration checksum 不匹配；
- 两个持久根缺少其一或来自不同时间点；
- 私钥/数据目录权限无法确认；
- Nginx 配置摘要与备份 manifest 不一致；
- rollback/reconciliation 无法唯一证明生产与运行状态。

恢复步骤见[冷备与灾难恢复](backup-and-disaster-recovery.md)。不确定状态始终保持 fail closed。
