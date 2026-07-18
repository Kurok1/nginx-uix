# Nginx UIX

Nginx UIX v0.2.2 在安全配置工作区之上增加显式发布闭环。管理员可以从当前生产配置创建持久化草稿，完成受管文本文件的编辑、搜索、逻辑分组和差异审阅，再通过具名确认执行完整候选校验、不可变备份、生产发布、reload、运行确认和可安全判定的自动回滚。

最终官方部署形态仍是一体化 Docker 容器，容器内同时运行 Nginx UIX、只监听本地 Unix 套接字的 Agent、s6 进程监督器和由 UIX 管理的 Nginx。`/etc/nginx` 始终是配置真源；SQLite 只保存用户、会话、工作区元数据、任务、审计和恢复索引，不保存可反向覆盖生产文件的配置正文。v0.2.2 不提供任意命令、restart、人工 restore 或备份保留策略；这些能力属于后续版本。

按当前路线图，v0.2.2 只完成源码、原生进程和浏览器验收，未执行 Docker 构建、容器运行或 multiarch。下面的容器命令描述目标部署接口，不构成 v0.2.2 镜像通过证据；完整 Docker 复验统一留到 v0.7.0。

## 快速开始

仓库当前没有声明可拉取的远程镜像地址。先从当前提交构建本地镜像：

```sh
docker build -f deploy/docker/Dockerfile -t nginx-uix:0.2.2 .
```

在宿主机准备一个只包含初始管理员密码的文件。密码必须是 12–128 个 Unicode 字符；文件最多 4 KiB，可以带一个末尾 LF 或 CRLF。将文件放在受限目录中，同时确保容器内 UID `10001` 可以读取它。不要把密码写进镜像、仓库或下面的命令行。

把 `/absolute/private/path/nginx-uix-admin-password` 替换为该文件的绝对路径，然后启动一个容器：

```sh
docker run -d \
  --name nginx-uix \
  -p 80:80 \
  -p 443:443 \
  -p 127.0.0.1:9000:9000 \
  --mount type=volume,src=nginx-uix-nginx,dst=/etc/nginx \
  --mount type=volume,src=nginx-uix-data,dst=/var/lib/nginx-uix \
  --mount type=bind,src=/absolute/private/path/nginx-uix-admin-password,dst=/run/secrets/nginx-uix-admin-password,readonly \
  --env NGINX_UIX_ADMIN_USERNAME=admin \
  --env NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin-password \
  nginx-uix:0.2.2
```

Docker 会为首次使用的两个具名卷创建持久存储。管理界面位于 <http://127.0.0.1:9000/>。管理端口只绑定宿主机回环地址；需要从另一台机器访问时，应通过自行维护的安全访问通道到达该地址。

## 端口和持久化目录

| 容器端口 | 服务 | 快速开始中的宿主机映射 |
| --- | --- | --- |
| `80/tcp` | Nginx HTTP | `80:80` |
| `443/tcp` | Nginx HTTPS | `443:443` |
| `9000/tcp` | Nginx UIX 管理界面 | `127.0.0.1:9000:9000` |

镜像内置的最小 Nginx 配置只监听 `80/tcp`。映射 `443/tcp` 不会自动启用 HTTPS；只有持久化配置明确监听 443 时，该端口才会提供服务。

必须保留并在每次创建容器时重新挂载这两个卷：

| 容器路径 | 快速开始中的卷 | v0.2.2 内容 |
| --- | --- | --- |
| `/etc/nginx` | `nginx-uix-nginx` | Nginx 配置真源 |
| `/var/lib/nginx-uix` | `nginx-uix-data` | SQLite 用户、会话、登录限速、迁移、审计元数据、工作区、发布 journal 和不可变备份 |

`/run/nginx-uix` 是每次启动时重建的运行时目录，不是持久化卷。

## 配置工作区边界

配置工作区保存的是从 `/etc/nginx` 稳定快照创建的草稿。只有 `ready` 工作区能够写入；每次写操作都需要当前强 ETag。保存文件、搜索、分组、查看 diff、导航或关闭浏览器都只作用于草稿，不会隐式修改生产或 reload。

生产变更只能从完整 diff 进入独立的“检查候选配置”流程。检查会绑定工作区 revision、生产/基础/草稿/候选摘要、manifest/policy 版本和 Nginx build；通过后仍须输入可见工作区名称确认。Agent 随后再次校验摘要，创建并验证完整不可变备份，逐文件原子替换并同步生产配置，执行完整 `nginx -t`、reload、master/worker 与回环 HTTP 健康确认。安全可判定的失败自动恢复备份；无法确认生产或运行状态时进入阻断性的 `needs_attention`，绝不报告成功。

v0.2.2 只管理符合正向策略的常规 UTF-8 文本文件。单个受管文件最多 2 MiB，生产根最多 4096 个条目、32 MiB 受管文本；最多保留 8 个工作区、合计 512 MiB 工作区数据。搜索最多返回 500 项，完整 diff 响应最多 4 MiB，达到响应上限时会明确标记不完整而不会伪装成完整结果。

私钥、证书、口令文件和被敏感 Nginx 指令引用的材料只显示安全分类，不复制或返回正文。symlink 不会被跟随或物化为活动链接：根内目标也只读，越界、断裂或成环目标显示为 external/unavailable。特殊文件、无效 UTF-8、含 NUL、越界路径和无法安全判定的条目均 fail closed。

`stale` 表示生产摘要已变化：旧草稿仍可查看、搜索和比较，但不能继续写入，也不会自动 rebase 或覆盖外部修改；应从当前生产配置创建新工作区。`needs_attention` 表示系统无法确认文件系统、journal 与 SQLite 元数据一致：该工作区只读，只允许查看或命名删除，不能通过普通编辑清除状态。

发布成功后工作区进入 `published`，仍可查看文件、diff、release ID 和阶段证据，但不能再次编辑或发布。发布任务由应用生命周期拥有；SSE 或浏览器断开不会取消已经排队的生产事务，刷新后会从持久化阶段恢复显示。

## 首次管理员和环境变量

应用只读取下面五个环境变量。Nginx 可执行文件、入口配置和 Agent 套接字路径固定在镜像内，不能通过环境变量改写。

| 变量 | 行为 |
| --- | --- |
| `NGINX_UIX_LISTEN_ADDR` | 可选；默认 `0.0.0.0:9000`。快速开始保持此默认值。 |
| `NGINX_UIX_PUBLIC_URL` | 可选；管理界面的可信公开 origin，例如 `https://admin.example.com`。 |
| `NGINX_UIX_ADMIN_USERNAME` | 用户表为空时必填；3–64 个可打印 ASCII 字符，首尾不能是空白。 |
| `NGINX_UIX_ADMIN_PASSWORD_FILE` | 用户表为空时首选密码来源；非空时必须成功读取该文件。 |
| `NGINX_UIX_ADMIN_PASSWORD` | 仅在密码文件变量为空时使用的明文回退。 |

密码文件优先级是 fail closed：只要设置了 `NGINX_UIX_ADMIN_PASSWORD_FILE`，文件不存在、不可读或内容无效都会阻止 UI 绑定 9000；程序不会改用 `NGINX_UIX_ADMIN_PASSWORD`。Docker 故障验收同时传入了文件密码和不同的明文回退，并验证只有文件密码可以登录。

不要在生产部署中使用明文回退。明文环境变量会成为容器配置的一部分，可能被有宿主机管理权限的工具读取。它只适合受控的本地兼容场景；正式部署应使用只读挂载的 Secret 文件。

引导变量只在 `/var/lib/nginx-uix` 的用户表为空时读取。管理员一旦创建，后续容器创建时即使改变这些变量，也不会创建第二个管理员或重置原凭据。保留数据卷是保留管理员访问权的必要条件。

## Public URL、Origin 和 Cookie

直接使用快速开始中的回环 HTTP 地址时，可以不设置 `NGINX_UIX_PUBLIC_URL`。服务会以实际请求的 scheme 和 `Host` 作为可信 origin。

如果浏览器通过固定的 HTTPS 地址访问管理界面，请在 `docker run` 中增加该 origin；非默认端口也必须包含在值中。例如：

```text
--env NGINX_UIX_PUBLIC_URL=https://admin.example.com
```

该值必须是绝对的 `http://` 或 `https://` origin：不能包含用户名、密码、查询、片段或除空路径、`/` 之外的路径。它只定义浏览器安全边界，不改变监听地址或端口。

登录请求必须携带与可信 origin 完全匹配的 `Origin`。应用不使用转发请求头来推断公开 scheme、Host 或登录限速来源，因此 HTTPS 入口必须显式配置正确的 `NGINX_UIX_PUBLIC_URL`。

成功登录后，Cookie 名为 `nginx_uix_session`，并始终设置 `HttpOnly`、`SameSite=Strict` 和 `Path=/`。只有显式配置了有效的 HTTPS `NGINX_UIX_PUBLIC_URL` 时才设置 `Secure`。Cookie 本身不设置持久化 `Expires` 或 `Max-Age`；服务端会话采用 8 小时空闲期限和 24 小时绝对期限。认证响应、状态响应和配置响应均禁止缓存。

## 健康状态

两个无需登录的端点只返回通用状态，不泄露版本、PID、路径或诊断内容：

| 端点 | 成功 | 语义 |
| --- | --- | --- |
| `GET /health/live` | `200 {"status":"ok"}` | 只证明 UI HTTP 进程能够响应；不检查 SQLite、Agent 或 Nginx。 |
| `GET /health/ready` | `200 {"status":"ready"}` | SQLite 可访问、Agent 可访问，且真实 Nginx 正在运行并未进入永久失败。任何一项失败都返回 `503 {"status":"not_ready"}`。 |

Docker 镜像的 `HEALTHCHECK` 使用 readiness，参数为 10 秒间隔、3 秒超时、20 秒启动期和 3 次重试。因此 Nginx 配置无效、Nginx 停止或 Agent 不可访问时，Docker 会把容器标为 `unhealthy`；这不等于 UI 一定不可访问。只要 liveness 仍为 200，就可以登录 UI 查看脱敏诊断。

```sh
curl -i http://127.0.0.1:9000/health/live
curl -i http://127.0.0.1:9000/health/ready
docker inspect --format '{{.State.Health.Status}}' nginx-uix
```

## 容器重建和版本升级

容器本身不是持久层。替换镜像或重新创建容器时，必须继续使用同一个 `nginx-uix-nginx` 和 `nginx-uix-data` 卷。不要删除这两个卷，也不要在删除容器时要求 Docker 连带删除卷。

```sh
docker stop --time 15 nginx-uix
docker rm nginx-uix
```

随后用目标镜像重复“快速开始”的 `docker run`，保持两个卷名不变。从 v0.2.1 升级到 v0.2.2 时，应用会向前执行 SQLite 迁移；不要把已经由 v0.2.2 打开的数据目录重新交给旧版本。该迁移和持久化重开已通过原生测试，容器卷升级统一在 v0.7.0 验证。保留两个卷会保留：

- Nginx 文件的内容和元数据；
- 初始管理员及原密码；
- 已有浏览器会话；
- 登录限速状态；
- SQLite WAL 完整性；
- 工作区、逻辑组和恢复状态；
- 发布检查、release 阶段、备份索引和受保护备份。

改变管理员引导变量不会修改已持久化的管理员。升级前应先备份两个卷，并以目标版本的发布验证记录确认迁移和平台限制。删除卷后的恢复不属于数据保留。

## Docker 验证边界

v0.1.0 和 v0.2.1 的历史 Docker 记录继续保留，但不替代最终完整产品的复验。v0.1.0 至 v0.6.0 不执行 Docker 脚本、镜像构建、容器/卷故障注入、容器浏览器测试或 multiarch；v0.7.0 将以届时完整源码一次性覆盖镜像、进程生命周期、数据升级、故障恢复、安全边界和 `linux/amd64`/`linux/arm64`。因此当前 README 不把任何 v0.2.2 Docker 命令或产物标记为通过。

## 空卷和非空 Nginx 卷

容器启动时会枚举 `/etc/nginx` 的所有直接条目，包括隐藏项。

- 目录完全为空时，镜像一次性复制最小默认树：`nginx.conf`、`conf.d/default.conf` 和 `html/index.html`。默认 Nginx 在 80 端口提供欢迎页。
- 只要存在任意文件、目录、隐藏项或符号链接，初始化就不会复制、合并、修复、改权限或重命名其中任何内容。
- 非空目录缺少 `/etc/nginx/nginx.conf`，或者入口配置不可读、无效时，目录仍保持原样；UI 和 Agent 继续运行，Nginx 保持停止，readiness 返回 503。

Docker 卷验收对普通文件、隐藏文件、嵌套目录和符号链接分别记录了启动前、启动后、容器重建后的内容与元数据摘要，三次结果完全一致。

## Nginx 无效时的诊断

当 Docker 状态为 `unhealthy` 时，先分别检查 liveness 和 readiness。典型的无效配置状态是：

- `/health/live` 返回 200；
- `/health/ready` 返回 503；
- 管理界面仍可登录；
- Dashboard 显示 UI 和 Agent 可用、Nginx 已停止，并展示最近一次启动校验的脱敏诊断；
- 容器内没有 Nginx master 进程。

可用以下已在故障验收中使用过的日志命令查看 UI、Agent 和监督流程的有界日志尾部：

```sh
docker logs --tail 120 nginx-uix
```

普通工作区编辑不会修改生产配置。若一次发布明确显示 `rolled_back`，系统已经恢复旧配置并重新确认运行健康；若显示 `needs_attention`，不要继续编辑、发布或把状态手工改成成功。v0.2.2 只提供证据查看，不提供人工 restore/restart；应先保存 release ID、backup ID 和脱敏阶段证据，再按受控运维流程检查生产配置与 Nginx。任何非空配置目录都不会被默认文件自动修补。

如果 liveness 也不可访问，应优先检查管理员引导输入、`/var/lib/nginx-uix` 的可写性、9000 监听冲突和容器日志。故障验收已验证：UI 数据只读、存储空间不足或 9000 被占用时，UI 不绑定管理端口，但有效的 Nginx 服务保持独立运行。

## 架构边界

发布 Dockerfile 的目标边界仍是 `linux/amd64` 和 `linux/arm64`。v0.2.2 没有构建或运行任一架构镜像，也不复用 v0.2.1 的 OCI 结果冒充当前版本证据；两种架构的最终行为统一在 v0.7.0 验证。

## 契约与验证来源

- [v0.1.0 验收证据](docs/release/v0.1.0-verification.md)
- [v0.2.1 验收证据](docs/release/v0.2.1-verification.md)
- [v0.2.2 验收证据](docs/release/v0.2.2-verification.md)
- [产品版本范围](PLAN.md)
- [v0.1.0 设计边界](docs/superpowers/specs/2026-07-14-v0.1-observability-design.md)
- [v0.2.1 工作区设计](docs/superpowers/specs/2026-07-15-v0.2.1-config-workspace-design.md)
- [v0.2.2 安全发布设计](docs/superpowers/specs/2026-07-18-v0.2.2-safe-publish-design.md)
- [REST API 契约](api/v1/openapi.yaml)
- [发布镜像定义](deploy/docker/Dockerfile)
- Docker、容器故障和 multiarch 脚本仅作为 v0.7.0 的未来验收入口，不属于 v0.2.2 已执行证据。
