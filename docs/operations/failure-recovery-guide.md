# Nginx UIX v1.0.0 故障恢复手册

## 适用范围

本手册用于在 UI、Agent、Nginx、SQLite、配置发布、Route Lab 或证书任务异常时选择安全恢复路径。详细诊断命令见[故障排查](troubleshooting.md)，升级兼容与程序回退见[升级与回滚](upgrade-and-rollback.md)，完整数据恢复见[冷备与灾难恢复](backup-and-disaster-recovery.md)。

恢复目标不是尽快把页面改成绿色，而是保留证据并唯一证明：

- `/etc/nginx` 的生产字节与记录的摘要一致；
- SQLite、备份索引、任务与审计可读且相互一致；
- Agent 执行的是固定类型化操作；
- Nginx 已通过完整校验，运行事实可确认；
- cleanup、rollback 或 restore 已达到持久终态。

无法证明任一项时保持 `needs_attention`，不要报告成功。

## 先停止扩大影响

1. 停止接收新的配置、恢复、restart、Route Lab 和证书操作。
2. 记录 UTC 时间、应用版本、source/build identity、request ID、task ID 和当前持久状态。
3. 保存 live、ready、Dashboard component 状态和脱敏日志；不要复制密码、Cookie、Token、私钥、请求 Body、配置正文或 diff 正文。
4. 不执行任意 Shell、手改 SQLite、删除 WAL/journal/stage、直接写 `/etc/nginx`、强制 reload 或反复重启。
5. 若数据库、权限或两个持久根可能损坏，优雅停止 UI 与 Agent，保留故障现场并进入冷备恢复。

浏览器关闭或 SSE 断开只影响显示，不改变服务端任务。先回读持久任务状态，再决定是否取消或恢复。

## 决策表

| 观察结果 | 首选路径 | 禁止动作 |
| --- | --- | --- |
| live 无响应，Nginx 仍服务 | 检查容器启动输入、数据根、UI 日志；数据健康时用同一 digest 和同一双卷重建 | 用默认空卷覆盖原数据 |
| live 200、ready 503 | 按 Dashboard 区分 SQLite、Agent、Nginx 或永久 recovery 状态 | 降低 readiness 或只检查 PID 1 |
| Agent unavailable | 停止会改变配置或进程的操作，检查固定 Socket、身份和权限后受控重建 | 增加 `--privileged`、Docker Socket 或任意命令入口 |
| Nginx invalid/stopped/unknown | 保留启动/运行诊断；先完整 `nginx -t`，再选择已验证配置 restore 或固定 restart | 校验失败后 reload |
| release `failed` | 确认生产未变或已有明确失败边界，重新创建 review/check | 复用过期 check 或跳过 diff |
| release/restore `rolled_back` | 保存原任务、backup 和 rollback 健康证据，调查触发原因 | 把 rolled back 记成普通成功 |
| 任一 `needs_attention` | 只使用 Recovery & History 的 current-state verification、指定 backup restore 或固定 restart | 手改任务状态、生产摘要或 journal |
| Route Lab cleanup 失败 | 停止同类测试，确认独立 master、随机端口和 stage 已清理 | reload 生产 Nginx 或扩大连接目标 |
| 证书 challenge/binding cleanup 不确定 | 保留旧证书和任务证据，阻断同一资源的新任务 | 删除仍被引用的材料或在 production 反复签发 |
| SQLite integrity/FK/migration 失败，或双根来自不同时间点 | 隔离当前状态，恢复已验证的完整双根冷备 | 导出重建 schema、删除 WAL 或混合两个快照 |

## 恢复层级

按影响从小到大选择第一个满足前提的层级：

1. **任务回读或受控取消**：持久数据健康，只是页面断开、任务超时或仍在运行。
2. **配置级恢复**：SQLite 和运行边界健康，存在已验证的 immutable backup；通过 Recovery & History 按 backup ID 恢复，系统先创建 safety backup。
3. **同版本容器重建**：镜像进程或容器运行层异常，但双持久根、Secret 和权限完整；使用相同 digest 和原挂载重建。
4. **程序回滚**：v1.0.0 引入运行问题，schema 仍为 `1..7`、没有无法由 v0.7.0 解释的活动任务，并且 v0.7.0 镜像 digest 已验证。
5. **完整数据恢复**：数据库、权限、摘要或双根一致性无法确认；停止所有写入，从同一恢复点恢复 `/etc/nginx` 与 `/var/lib/nginx-uix` 到空目标。

不得把不同层级拼接成“部分恢复”。尤其不能用旧 `/etc/nginx` 搭配新 `/var/lib/nginx-uix`，也不能逐文件合并恢复树。

## 恢复完成条件

只有以下证据全部成立，才结束事故状态：

- SQLite `integrity_check` 为 `ok`、`foreign_key_check` 无行、migration 精确为 `1..7`；
- 两个持久根的摘要、owner、group、mode、symlink 和证书材料符合恢复点；
- 没有无法解释的 active task、production lease、candidate/validation/Route Lab stage；
- 完整 `nginx -t` 通过，且没有在失败后绕过校验 reload；
- UI、Agent、真实 Nginx 和 Docker health 可确认，live/ready 符合预期；
- 配置 restore/restart/rollback 已有持久终态、backup ID、request/task ID 和审计事件；
- 重建后通过 API 回读管理员、工作区、历史、证书和备份索引。

事故记录应只保存脱敏标识、时间、版本、digest、状态转换和修复结论。若仍有一项不确定，保持隔离并标记“需要人工处理”。
