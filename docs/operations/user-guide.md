# Nginx UIX v1.0.0 用户手册

## 适用范围

本手册面向通过浏览器管理单节点 HTTP/HTTPS Nginx 的管理员。v1.0.0 不管理集群、Kubernetes、WAF、Nginx Plus、宿主机 Docker 或其他容器中的 Nginx，也不提供任意 Shell。

部署、升级和权限由[管理员手册](administrator-guide.md)负责；发生数据损坏或状态无法确认时转到[故障恢复手册](failure-recovery-guide.md)。

## 登录与 Dashboard

使用管理员提供的 HTTPS 管理地址登录。Cookie 为 `HttpOnly`、`SameSite=Strict`，不会把 Session token 保存到浏览器 Web Storage。会话空闲 8 小时或创建 24 小时后需要重新登录。

Dashboard 同时显示 UI、Agent 与真实 Nginx 状态：

- UI healthy 只表示管理服务可响应；
- Agent unavailable 时不能安全执行配置、进程或 Route Lab 操作；
- Nginx degraded/stopped/unknown 时先查看启动校验与恢复证据；
- readiness 为 503 时不能通过刷新、忽略错误或扩大超时把它当成健康。

页面错误会显示 request ID 或 task ID。记录 ID 和时间，不要把密码、Cookie、Token、私钥、配置正文或 diff 正文复制到工单。

## 配置工作区

正常配置变更遵循固定闭环：

1. 从当前生产配置创建工作区。
2. 在文件视图或结构化 Upstream、Server/Location 页面编辑草稿。
3. 查看完整 diff；确认未识别的 Nginx 语法仍被保留。
4. 运行完整候选校验。校验会构造隔离候选并执行 `nginx -t`，不会 reload 生产实例。
5. 使用工作区名称确认发布。
6. 等待 release 达到持久终态，再检查 backup ID、reload 和运行确认。

`stale` 表示生产配置在工作区创建后发生变化。旧工作区会只读；从当前生产重新创建工作区，不做自动 rebase。`needs_attention` 表示系统无法唯一证明文件、元数据或运行事实，普通编辑与发布会保持阻断。

不要直接在浏览器外修改工作区内部的 `base`、`draft` 或 `control`。如果其他受控工具直接修改 `/etc/nginx`，UIX 会通过生产摘要使旧工作区失效。

## 发布终态

| 状态 | 含义 | 用户动作 |
| --- | --- | --- |
| `succeeded` | 候选已校验、备份、原子发布、reload 并确认运行 | 保存 release/backup ID |
| `failed` | 发布未成功；详情必须说明生产是否未变 | 修正原因后重新 review/check |
| `rolled_back` | 发布失败后已恢复原配置并确认健康 | 保存 rollback 证据并调查 |
| `needs_attention` | 无法安全确认最终状态 | 停止普通变更，进入 Recovery & History |
| `cancelled` | 服务端已持久化取消 | 检查 cleanup 与生产摘要 |

浏览器关闭或 SSE 断开不会让后台任务自动成功、失败或取消。重新打开对应历史记录读取持久状态。

## Route Lab

Route Lab 包含两类证据：

- 静态分析预测 Nginx 的 server/location 选择；
- 运行测试在随机回环端口启动独立 Nginx，连接目标始终是沙箱回环地址。

Host/SNI 只改变请求语义，不改变连接目标。运行测试不会 reload 生产 Nginx。POST、Body 或可能联系 upstream 的请求需要精确二次确认；历史不会保存 Body 或敏感 Header 值。

`ROUTE_CLEANUP_FAILED` 时不要反复重试。记录 run ID，让管理员确认沙箱 master、端口和 stage 已清理。

## Recovery & History

每次成功发布都会创建不可变配置备份。Recovery & History 可执行：

- 按 backup ID 人工恢复，并在写生产前创建 safety backup；
- 固定类型化 restart；
- 备份保留 dry-run 与确认执行；
- `needs_attention` 的当前状态验证或受控处置；
- release、restore、restart、Route Lab、证书任务与审计历史查看。

产品内发布备份只覆盖配置事务，不能替代 `/etc/nginx` 与 `/var/lib/nginx-uix` 的整机冷备。

## 证书

证书页面支持：

- ACME 账户注册与停用；
- HTTP-01 和受限 Cloudflare API Token DNS-01；
- 签发 review、执行、取消与持久任务历史；
- Nginx server 绑定 diff、发布和清理；
- 手工/自动续期、旧版本保留和失败退避。

wildcard 必须使用 DNS-01。Cloudflare 只接受目标 Zone 的受限 API Token，不接受 Global API Key。Token、ACME account key 和私钥提交后不会返回浏览器。

production 签发必须有匹配的 staging 预检证据或精确风险确认。遇到 rate limit 或传播超时应遵守 backoff，不要用 production 配额试探。

## 日常检查清单

- Dashboard 没有 component issue，readiness 正常；
- 没有长期处于 queued/running/cancelling 的任务；
- 没有未解释的 `needs_attention`；
- 发布前 diff、check 和确认对象一致；
- 发布后保存 release、backup 和 request ID；
- 证书到期、续期重试和 binding 状态可解释；
- 定期冷备和恢复演练由管理员完成。

故障状态与处理入口见[故障排查](troubleshooting.md)。
