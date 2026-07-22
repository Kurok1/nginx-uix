# Nginx UIX v0.6.0 冷备与灾难恢复

## 支持的备份类型

v0.6.0 只支持维护窗口内的冷备份。备份单元必须同时包含：

- `/etc/nginx`：Nginx 配置真源及其相对引用材料；
- `/var/lib/nginx-uix`：SQLite、WAL（若存在）、工作区、不可变备份、证书密文/私钥和任务证据。

不支持在线复制单个 `nginx-uix.db`、备份下载 API、浏览器文件导出或让 SQLite 反向生成 Nginx 配置。产品内的不可变发布备份用于配置事务恢复，也不能替代整机灾备。

## 安全前提

1. 选择新的绝对备份目标，例如 `/srv/nginx-uix-recovery/2026-07-22T140000Z`；不能使用 `/`、用户主目录或已有数据根。
2. 确认目标位于受保护、空间充足、支持 Unix owner/mode/symlink 的文件系统。
3. 停止接收新任务，等待 release、restore、restart、Route Lab、certificate scheduler/task owner 退出。
4. 停止 UI 和 Agent，并确认没有进程持有 SQLite 写连接。若还有其他自动化会修改 `/etc/nginx`，也必须停止。
5. 记录应用版本、构建身份、Nginx `-V` 和维护窗口。

生产 Nginx 可以继续服务已加载的配置，但维护窗口内不得有任何配置写入或 reload。需要最清晰的全系统边界时一并优雅停止 Nginx。

## 创建冷备

下面是 Linux 示例。先把占位路径替换为一个新的、明确的绝对目录，并人工确认它不是现有生产根：

```sh
RECOVERY_SNAPSHOT=/srv/nginx-uix-recovery/2026-07-22T140000Z
test "${RECOVERY_SNAPSHOT}" != "/"
test ! -e "${RECOVERY_SNAPSHOT}"
install -d -m 0700 "${RECOVERY_SNAPSHOT}"
cp -a -- /etc/nginx "${RECOVERY_SNAPSHOT}/etc-nginx"
cp -a -- /var/lib/nginx-uix "${RECOVERY_SNAPSHOT}/var-lib-nginx-uix"
```

不要把变量设为 `$HOME`、`~` 或含通配符的路径。`cp -a` 的目的在于保留 owner、group、mode、symlink 和时间；目标介质不支持这些元数据时，备份不合格。

生成不包含正文的文件清单和 SHA-256 清单：

```sh
(
  cd -- "${RECOVERY_SNAPSHOT}"
  find . -xdev ! -name manifest.metadata ! -name manifest.sha256 \
    -printf '%P\t%y\t%m\t%U\t%G\t%s\n' \
    | LC_ALL=C sort > manifest.metadata
  find . -xdev -type f ! -name manifest.metadata ! -name manifest.sha256 -print0 \
    | LC_ALL=C sort -z \
    | xargs -0 sha256sum > manifest.sha256
  chmod 0600 manifest.metadata manifest.sha256
)
```

清单可以包含相对路径、类型、权限、owner、size 和摘要，但不能复制密码、Token、私钥、配置正文、diff 或请求 Body 到工单/普通日志。

## 验证恢复点

1. 以只读方式打开备份中的数据库：

```sh
sqlite3 "file:${RECOVERY_SNAPSHOT}/var-lib-nginx-uix/nginx-uix.db?mode=ro" \
  'PRAGMA integrity_check; PRAGMA foreign_key_check; SELECT version FROM schema_migrations ORDER BY version;'
```

期望 `integrity_check` 仅输出 `ok`，`foreign_key_check` 无行，v0.6.0 migration 精确为 `1` 到 `7`。

2. 复核 `/var/lib/nginx-uix`、备份和证书等敏感数据目录不高于 `0700`，数据库和私钥不高于 `0600`；Nginx 配置目录保留部署所需的原 mode（镜像默认根为 `0755`）。确认两个根的 owner/group 与部署身份一致。
3. 复核 manifest 中没有越界 symlink、Socket、device 或无法恢复的特殊文件。
4. 在临时 mount namespace、chroot 或等价隔离环境中把备份副本映射到原绝对路径，再运行完整 `/usr/sbin/nginx -t -c /etc/nginx/nginx.conf`。仅更改 `-p` 不能安全处理绝对 include/证书路径，禁止因此误读生产文件。
5. 将恢复点标为只读并记录验证时间。任何一步失败都把目标隔离为“不完整”，不能当成恢复点。

## 空目标恢复

恢复前先保留故障现场快照。下面的检查只接受空目标，不负责删除任何现有内容：

```sh
test -z "$(find /etc/nginx -mindepth 1 -maxdepth 1 -print -quit)"
test -z "$(find /var/lib/nginx-uix -mindepth 1 -maxdepth 1 -print -quit)"
```

若目标非空，停止并把现有目录完整移到明确的隔离路径；不要用递归删除清场，也不要合并恢复树。

在 UI、Agent 和所有配置写入者停止的情况下恢复：

```sh
cp -a -- "${RECOVERY_SNAPSHOT}/etc-nginx/." /etc/nginx/
cp -a -- "${RECOVERY_SNAPSHOT}/var-lib-nginx-uix/." /var/lib/nginx-uix/
```

随后按顺序执行：

1. 对照两份 manifest 复核摘要和元数据。
2. 复核 `/var/lib/nginx-uix` 目录、数据库、证书密钥和 Token 密文权限。
3. 只读执行 SQLite `integrity_check`、`foreign_key_check` 和 migration 查询。
4. 在不 reload 生产的情况下执行完整 `nginx -t`；绝对路径必须指向恢复树而不是旧生产树。
5. 启动 Agent，确认固定 Socket 为真实 Unix Socket、`0:10002`、`0660`。
6. 启动 UI，等待 startup reconciliation，检查 liveness/readiness 和 Dashboard。
7. 验证管理员、工作区、备份/历史、Route Lab 与证书任务可读，再恢复管理入口和业务流量。

任何摘要、权限、数据库、证书、配置或运行状态不确定，都保持服务隔离并标记“需要人工处理”。不得通过手改 SQLite、删除 journal、跳过 `nginx -t` 或强制 reload 报告成功。

## 定期演练

至少在每次版本升级前和备份介质/权限策略变化后执行一次临时目录恢复演练。演练必须记录：

- 恢复点版本和摘要；
- 两个根是否同时恢复；
- SQLite integrity/foreign key/migration；
- Nginx 隔离验证；
- 证书与 Secret 权限；
- UI/Agent/Nginx health；
- 未运行的 Docker volume 项（v0.7.0 前必须明确标为未验证）。
