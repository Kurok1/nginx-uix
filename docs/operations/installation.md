# Nginx UIX v0.7.0 安装与验收

## 当前发布边界

v0.7.0 是功能冻结后的 Docker 统一验证版。它已经在原生 arm64 daemon 上完成一体化镜像、容器生命周期、volume 升级、故障注入、Docker 浏览器、安全边界和供应链验收，同时完成 linux/amd64 与 linux/arm64 OCI/SBOM 静态验证。

当前仍有两项发布边界：

- 本版本只形成经过验证的本地候选 `nginx-uix:0.7.0-acceptance`，没有远端 pull 地址、不可变 tag 或正式发布承诺。
- 当前主机没有真实 amd64 runner；amd64 OCI/SBOM/静态内容通过，原生运行留给 v1.0.0 发布环境。

完整环境、image ID、manifest/SBOM digest、命令与限制见 [v0.7.0 验收证据](../release/v0.7.0-verification.md)。

## 固定工具链

| 工具 | 仓库固定版本 |
| --- | --- |
| Go | `1.26.5`（`go.mod` toolchain） |
| Node.js | `24.17.0` |
| npm | `11.13.0` |
| Nginx | 发布镜像目标为 `1.30.3`；原生兼容测试记录实际二进制版本 |

前端依赖只安装到 `web/node_modules`，使用 npm 和 `package-lock.json`。不要使用全局 Vite、Vue、Playwright 或混入 pnpm/yarn lockfile。

## 从干净源码验收

在仓库根目录执行：

```sh
test "$(tr -d '\n' < VERSION)" = "0.7.0"
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

真实 Nginx 兼容测试只使用临时目录、随机回环端口、独立 PID 和日志：

```sh
NGINX_UIX_INTEGRATION=1 NGINX_BIN=/absolute/path/to/nginx \
  go test ./tests/integration -count=1 -v
```

该命令不得指向包装了 production reload 行为的脚本。测试不会读取、写入或 reload `/etc/nginx` 的生产实例。

## 已验证运行布局

官方部署是一体化 Linux 镜像，同时包含 UI、特权 Agent、进程监督器和受管理的 Nginx。不会提供 UI 与 Nginx 分离容器，也不会使用 Docker Socket 或 `--privileged` 管理宿主机 Nginx。

| 项目 | 固定接口 |
| --- | --- |
| UI | TCP `9000`，默认监听 `0.0.0.0:9000` |
| Nginx | TCP `80` / `443` |
| Agent | `/run/nginx-uix/agent.sock`，Unix Socket，不对网络暴露 |
| Nginx 配置真源 | `/etc/nginx` |
| UIX 持久数据 | `/var/lib/nginx-uix` |
| Nginx 二进制 | `/usr/sbin/nginx` |
| UI 用户 | UID/GID `10001:10001` |
| Agent Socket 组 | GID `10002`；Socket 必须为 `0:10002`、`0660` |

`/etc/nginx` 和 `/var/lib/nginx-uix` 必须是两个独立、持久、可一起备份的挂载。`/run/nginx-uix` 是运行期状态，不能当成持久卷。

## 启动本地候选

以下命令只引用本机已经验收的候选标签，不会从远端下载镜像。先创建权限为 `0600`、内容为初始管理员密码的 Secret 文件：

```sh
test -f ./admin-password
test "$(stat -f '%Lp' ./admin-password 2>/dev/null || stat -c '%a' ./admin-password)" = "600"
docker image inspect nginx-uix:0.7.0-acceptance >/dev/null
```

然后使用两个命名 volume 启动：

```sh
docker run --detach \
  --name nginx-uix \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add DAC_OVERRIDE \
  --cap-add FOWNER \
  --cap-add KILL \
  --cap-add SETGID \
  --cap-add SETUID \
  --publish 80:80 \
  --publish 443:443 \
  --publish 127.0.0.1:9000:9000 \
  --mount type=volume,src=nginx-uix-nginx,dst=/etc/nginx \
  --mount type=volume,src=nginx-uix-data,dst=/var/lib/nginx-uix \
  --mount type=bind,src="$PWD/admin-password",dst=/run/secrets/nginx-uix-admin,readonly \
  --env NGINX_UIX_ADMIN_USERNAME=admin \
  --env NGINX_UIX_ADMIN_PASSWORD_FILE=/run/secrets/nginx-uix-admin \
  nginx-uix:0.7.0-acceptance
```

原生验收使用 `--cap-drop ALL` 逐项证明了上述六项 capability；不要增加 `--privileged`、host PID/network、Docker Socket 或额外设备。本机 daemon 上不需要 `NET_BIND_SERVICE`。UI 只映射到宿主回环地址；若需要远程管理，应通过受控 HTTPS 入口访问并设置精确的 `NGINX_UIX_PUBLIC_URL`。

## 首次管理员

首次启动时用户表为空，推荐通过只读 Secret 文件提供密码：

| 变量 | 语义 |
| --- | --- |
| `NGINX_UIX_ADMIN_USERNAME` | 3–64 个可打印 ASCII 字符，首尾无空白 |
| `NGINX_UIX_ADMIN_PASSWORD_FILE` | 首选密码来源；设置后读取失败会 fail closed |
| `NGINX_UIX_ADMIN_PASSWORD` | 仅在未设置密码文件时使用的本地兼容回退 |
| `NGINX_UIX_LISTEN_ADDR` | 可选，默认 `0.0.0.0:9000` |
| `NGINX_UIX_PUBLIC_URL` | 可选，可信管理 origin，例如 `https://admin.example.test` |
| `NGINX_UIX_EFFECTIVE_CONFIG_ROOTS` | Agent 额外只读配置根，Linux path-list 格式 |

密码必须为 12–128 个 Unicode 字符。密码文件最多 4 KiB，可带一个末尾 LF/CRLF，权限应为 `0600`，且只允许目标 UI 身份读取。管理员一旦创建，引导变量不会重置密码或创建第二个管理员。

## 公开地址与 Cookie

通过固定 HTTPS 地址访问时必须设置精确 origin 的 `NGINX_UIX_PUBLIC_URL`。值只能是 `http://` 或 `https://` origin，不能含凭据、查询、fragment 或业务路径。应用不信任 `X-Forwarded-For` 推导登录来源，也不使用转发 Header 推导可信 origin。

Session Cookie 名为 `nginx_uix_session`，始终使用 `HttpOnly`、`SameSite=Strict`、`Path=/`；显式 HTTPS public URL 时再加 `Secure`。会话为 8 小时 idle、24 小时 absolute，浏览器 Web Storage 不保存 token。

## 启动后检查

```sh
curl --fail --show-error http://127.0.0.1:9000/health/live
curl --fail --show-error http://127.0.0.1:9000/health/ready
```

- liveness 只证明 UI HTTP 进程响应。
- readiness 同时要求 SQLite、Agent 和真实 Nginx 状态可确认；任一不满足返回 503。
- readiness 失败不能通过修改探针或扩大超时伪装为健康，应按[故障排查](troubleshooting.md)保留 request ID 与安全诊断。

正式使用前仍必须完成[升级与回滚](upgrade-and-rollback.md)和[冷备与灾难恢复](backup-and-disaster-recovery.md)演练。v0.7.0 已证明本地 arm64 候选的安装接口；远端不可变 multiarch 镜像、真实 amd64 runtime 和正式发布承诺属于 v1.0.0。
