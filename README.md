# Nginx UIX

Nginx UIX v0.2.1 在只读 Nginx 可观测能力之上增加安全配置工作区。管理员可以从当前生产配置创建持久化草稿，完成受管文本文件的编辑、搜索、逻辑分组和差异审阅。

v0.2.1 唯一受支持的部署形态仍是一个一体化 Docker 容器。容器内同时运行 Nginx UIX、只监听本地 Unix 套接字的 Agent、s6 进程监督器和由 UIX 观察的 Nginx。工作区只是 `/var/lib/nginx-uix` 内的草稿；`/etc/nginx` 始终是生产配置真源。此版本不会从工作区运行候选配置校验，也不提供发布、备份、reload 或 restart。

## 快速开始

仓库当前没有声明可拉取的远程镜像地址。先从当前提交构建本地镜像：

```sh
docker build -f deploy/docker/Dockerfile -t nginx-uix:0.2.1 .
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
  nginx-uix:0.2.1
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

| 容器路径 | 快速开始中的卷 | v0.2.1 内容 |
| --- | --- | --- |
| `/etc/nginx` | `nginx-uix-nginx` | Nginx 配置真源 |
| `/var/lib/nginx-uix` | `nginx-uix-data` | SQLite 用户、会话、登录限速、迁移、审计元数据和持久化工作区 |

`/run/nginx-uix` 是每次启动时重建的运行时目录，不是持久化卷。

## 配置工作区边界

配置工作区保存的是从 `/etc/nginx` 稳定快照创建的草稿。只有 `ready` 工作区能够写入；每次写操作都需要当前强 ETag。所有工作区操作都局限在 `/var/lib/nginx-uix/workspaces`，不会修改、删除、重命名、chmod 或 chown `/etc/nginx` 中的条目，也不会触发候选配置 `nginx -t`、发布、reload 或 restart。

v0.2.1 只管理符合正向策略的常规 UTF-8 文本文件。单个受管文件最多 2 MiB，生产根最多 4096 个条目、32 MiB 受管文本；最多保留 8 个工作区、合计 512 MiB 工作区数据。搜索最多返回 500 项，完整 diff 响应最多 4 MiB，达到响应上限时会明确标记不完整而不会伪装成完整结果。

私钥、证书、口令文件和被敏感 Nginx 指令引用的材料只显示安全分类，不复制或返回正文。symlink 不会被跟随或物化为活动链接：根内目标也只读，越界、断裂或成环目标显示为 external/unavailable。特殊文件、无效 UTF-8、含 NUL、越界路径和无法安全判定的条目均 fail closed。

`stale` 表示生产摘要已变化：旧草稿仍可查看、搜索和比较，但不能继续写入，也不会自动 rebase 或覆盖外部修改；应从当前生产配置创建新工作区。`needs_attention` 表示系统无法确认文件系统、journal 与 SQLite 元数据一致：该工作区只读，只允许查看或命名删除，不能通过普通编辑清除状态。

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

随后用目标镜像重复“快速开始”的 `docker run`，保持两个卷名不变。从 v0.1.0 升级到 v0.2.1 时，应用会向前执行 SQLite 迁移；不要把已经由 v0.2.1 打开的数据卷重新挂载到旧版本。保留两个卷会保留：

- Nginx 文件的内容和元数据；
- 初始管理员及原密码；
- 已有浏览器会话；
- 登录限速状态；
- SQLite WAL 完整性；
- v0.2.1 工作区、逻辑组和恢复状态。

改变管理员引导变量不会修改已持久化的管理员。升级前应先备份两个卷，并以目标版本的发布验证记录确认迁移和平台限制。删除卷后的恢复不属于数据保留。

## 本地验收镜像与构建缓存

Docker 验收会根据 Buildx driver 选择缓存后端。`docker` driver 使用 Docker daemon 原生缓存并报告 `cache=daemon`，不读写 `.tmp/buildx-cache`；`docker-container` driver 则以事务化替换的 `.tmp/buildx-cache` 作为当前 worktree 唯一可写本地缓存。双架构构建使用专用 `docker-container` builder。首次运行可用 `BUILD_IMAGE=auto`：脚本只在本地镜像的 source fingerprint 或平台 build identity 不匹配时构建；后续套件使用相同 `IMAGE` 和 `BUILD_IMAGE=0`，身份不一致会直接失败而不会暗中重建。再次使用 `BUILD_IMAGE=auto` 应复用完全匹配的镜像并报告 `build=no`。

```sh
export IMAGE=nginx-uix:0.2.1-acceptance
BUILD_IMAGE=auto tests/docker/smoke.sh
BUILD_IMAGE=0 tests/docker/faults.sh
BUILD_IMAGE=0 tests/docker/workspace.sh
```

在 `docker-container` driver 下可以设置 `BUILDX_CACHE_SEED_DIR` 导入另一个已验证缓存；seed 只作为只读 `cache-from`，不会被复制、轮换、删除或作为 `cache-to`。`.tmp/buildx-cache` 只在该 driver 下作为可写缓存。常规验收不使用 `--no-cache`，也不删除或清空现有缓存。

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

v0.2.1 的工作区不会修改持久化生产配置。请在持久化 `/etc/nginx` 中恢复有效的 `/etc/nginx/nginx.conf`，再停止并重新启动容器。任何非空卷都不会被默认文件自动修补。

如果 liveness 也不可访问，应优先检查管理员引导输入、`/var/lib/nginx-uix` 的可写性、9000 监听冲突和容器日志。故障验收已验证：UI 数据只读、存储空间不足或 9000 被占用时，UI 不绑定管理端口，但有效的 Nginx 服务保持独立运行。

## 架构边界

发布 Dockerfile 只接受 `linux/amd64` 和 `linux/arm64`；其他 `TARGETARCH` 会使构建失败。多架构验收已从同一源码候选和锁文件生成并检查两个 OCI manifest。

当前候选的完整 native smoke、工作区和故障证据来自原生 `linux/arm64` 环境。最终源码候选已成功生成 `linux/amd64` 和 `linux/arm64` 两个 OCI 产物；按 2026-07-18 的用户决定，两个平台构建成功即作为本版本的多架构接受边界，后续 multiarch native runtime 未运行且不声称通过。部署时应选择与 Linux 主机原生架构匹配的镜像，并把未覆盖的原生平台运行视为未验证。

## 契约与验证来源

- [v0.1.0 验收证据](docs/release/v0.1.0-verification.md)
- [v0.2.1 验收证据](docs/release/v0.2.1-verification.md)
- [产品版本范围](PLAN.md)
- [v0.1.0 设计边界](docs/superpowers/specs/2026-07-14-v0.1-observability-design.md)
- [v0.2.1 工作区设计](docs/superpowers/specs/2026-07-15-v0.2.1-config-workspace-design.md)
- [REST API 契约](api/v1/openapi.yaml)
- [发布镜像定义](deploy/docker/Dockerfile)
- [Docker 主线验收](tests/docker/smoke.sh)
- [Docker 故障验收](tests/docker/faults.sh)
- [双架构验收](tests/docker/multiarch.sh)
