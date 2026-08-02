# Nginx UIX

Nginx UIX 是面向单节点 HTTP/HTTPS Nginx 的 Web 管理界面，后端使用 Go，前端使用 Vue 3 + TypeScript，持久化使用 SQLite。官方部署方式是一体化 Docker 镜像，镜像内同时包含 Nginx UIX 和受管 Nginx。

当前版本为 `1.0.0`，已经正式发布：

- [GitHub Release v1.0.0](https://github.com/Kurok1/nginx-uix/releases/tag/v1.0.0)
- `ghcr.io/kurok1/nginx-uix:1.0.0`

## 当前能力

- 查看 Nginx 运行状态和生效配置。
- 管理配置工作区、diff、校验、发布、备份与恢复。
- 结构化管理 upstream、server 和 location。
- 使用隔离 Nginx 实例执行 Route Lab。
- 管理 Let's Encrypt HTTP-01 与 Cloudflare DNS-01 证书。
- 通过同一个 Web UI 查看任务、历史和审计记录。

v1.0 只面向单节点 Nginx，不包含集群、Kubernetes、WAF、Nginx Plus、Docker Socket 管理或 UI/Nginx 分离容器方案。

## v1.0 发布门禁

v1.0 只要求：

- Go 与前端单元测试、静态类型检查和正常构建通过。
- native amd64 一体化镜像通过 Docker basic smoke。
- 成功生成 linux/amd64 与 linux/arm64 的 `nginx-uix`、`nginx-uix-agent` 二进制。
- 成功生成 linux/amd64 与 linux/arm64 的 OCI 镜像包。

当前发布流水线不运行 Playwright、权限或安全套件、漏洞扫描、SBOM、故障注入、完整升级恢复和长时间稳定性测试。这些问题后续按独立缺陷处理，不阻断 v1.0。

## 源码验证

固定工具链为 Go `1.26.5`、Node.js `24.17.0`、npm `11.13.0`。

```sh
go mod verify
go test ./...

export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
. "$NVM_DIR/nvm.sh"
nvm use 24.17.0
test "$(node --version)" = "v24.17.0"
test "$(npm --version)" = "11.13.0"
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Docker basic smoke：

```sh
IMAGE=nginx-uix:1.0.0-test \
PLATFORM=linux/amd64 \
BUILD_IMAGE=1 \
SMOKE_PROFILE=basic \
tests/docker/smoke.sh
```

多平台二进制和 OCI 镜像包默认输出到 `.tmp/multiarch/`：

```sh
tests/docker/multiarch.sh
```

GitHub Actions 会把这六个文件保存为可下载的构建 artifact。

## 正式发布

v1.0.0 已由 [release.yml](.github/workflows/release.yml) 发布。可直接拉取正式多架构镜像：

```sh
docker pull ghcr.io/kurok1/nginx-uix:1.0.0
```

后续正式发布仍只在推送与 `VERSION` 完全一致的 `vX.Y.Z` tag 时触发。发布工作流会重复最小门禁，然后：

- 创建 GitHub Release，附带 amd64/arm64 二进制压缩包、OCI 镜像包和 `SHA256SUMS`。
- 推送 `ghcr.io/kurok1/nginx-uix:1.0.0`、`:v1.0.0` 和 `:latest` 多架构镜像。

普通 `main` 分支 push 不会创建 Release；只有显式 tag 才会发布。

## 运行边界

目标镜像暴露 Nginx `80/443` 和 UI `9000`，并持久化：

| 路径 | 内容 |
| --- | --- |
| `/etc/nginx` | Nginx 配置真源 |
| `/var/lib/nginx-uix` | SQLite、工作区、备份、证书材料和任务记录 |

`/run/nginx-uix` 只保存运行期 Socket/PID，不是持久卷。

## 文档

- [Apache License 2.0](LICENSE)
- [版本范围与路线图](PLAN.md)
- [v1.0.0 验收记录](docs/release/v1.0.0-verification.md)
- [v1.1.0 发布说明](docs/release/v1.1.0-release-notes.md)
- [v1.1.0 验收记录](docs/release/v1.1.0-verification.md)
- [安装与验收](docs/operations/installation.md)
- [用户手册](docs/operations/user-guide.md)
- [语言选择与本地化](docs/operations/language-and-localization.md)
- [管理员手册](docs/operations/administrator-guide.md)
- [升级与回滚](docs/operations/upgrade-and-rollback.md)
- [故障恢复](docs/operations/failure-recovery-guide.md)
- [REST API 契约](api/v1/openapi.yaml)
