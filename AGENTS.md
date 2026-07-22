# Nginx UIX 项目规范

## 1. 适用范围

本文件适用于整个仓库，是所有开发、测试、评审和文档工作的根级规范。

- `MUST` / “必须”表示不可跳过的要求。
- `SHOULD` / “应当”表示默认要求；偏离时必须说明原因并获得确认。
- 将来某个目录出现更具体的 `AGENTS.md` 时，局部文件只补充该目录规则，不重复或削弱本文件的全局约束。
- 不要为尚未创建的模块提前增加局部 `AGENTS.md`。

## 2. 权威文档与优先级

- `AGENTS.md`：工程、质量、安全和协作规范。
- `PLAN.md`：已确认的产品范围、架构边界、版本顺序和验收条件。
- `DESIGN.md`：前端视觉 token、组件语言、交互和响应式规范。

实施任何功能前必须阅读与任务有关的上述文档。项目文档发生冲突时，不得自行选择方便的一方；应停止相关实现并请求确认。安全、数据完整性和可访问性要求不能被纯视觉要求覆盖。

## 3. 已确定的技术与产品边界

- 后端使用 Go。
- 前端使用 Vue 3 + TypeScript。
- 持久化使用 SQLite；Nginx 配置文件始终是配置真源。
- API 使用版本化 REST；长任务进度使用 SSE。
- 官方首要部署方式是一体化 Docker 镜像，镜像内同时包含 Nginx UIX 和受其管理的 Nginx。
- 不提供 UI 与 Nginx 分离容器的官方 Docker 方案。
- 不通过 Docker Socket 管理宿主机或其他容器中的 Nginx。
- v1.0 只面向单节点 HTTP/HTTPS Nginx 管理；不得顺手扩展集群、Kubernetes、WAF 或 Nginx Plus 功能。
- 所有生产配置修改必须经过工作副本、差异检查、完整校验、备份、原子发布和失败恢复流程。
- 路径测试必须使用隔离 Nginx 实例，不能 reload 生产实例。

## 4. 目标工程骨架

工程骨架遵循 `$golang-patterns` 的 `cmd`、`internal`、小包和依赖注入原则，并按本项目领域拆分：

```text
.
├── cmd/
│   ├── nginx-uix/              # Web/API 入口，只做装配与生命周期管理
│   └── nginx-uix-agent/        # 本地特权 Agent 入口
├── internal/
│   ├── app/                    # 依赖装配和应用生命周期
│   ├── runtime/                # Nginx 状态、validate、reload、restart
│   ├── config/                 # 工作副本、diff、备份、发布事务
│   ├── nginxast/               # tokenizer、AST、source span、局部渲染
│   ├── upstream/               # upstream 领域逻辑
│   ├── location/               # server/location 领域逻辑
│   ├── routelab/               # 静态分析和隔离运行测试
│   ├── certificate/            # ACME 和证书生命周期
│   ├── store/                  # SQLite、迁移和审计
│   └── httpapi/                # REST、SSE、认证和输入边界
├── api/
│   └── v1/                     # OpenAPI 或其他版本化 API 契约
├── web/                        # Vue 3 + TypeScript
├── deploy/
│   └── docker/                 # 一体化镜像、初始化和健康检查
├── tests/
│   └── fixtures/nginx/         # 仓库级 Nginx 固定测试样本
├── PLAN.md
├── DESIGN.md
├── AGENTS.md
└── VERSION
```

规则：

- 默认不创建 `pkg/`。只有确认需要向仓库外提供稳定 Go API 时才使用。
- 不创建含义模糊的 `common`、`utils`、`helpers` 或 `misc` 大杂烩包。
- 每个包只负责一个清晰领域；不能为了复用少量代码制造循环依赖或跨层反向依赖。
- `cmd/*/main.go` 只负责解析启动参数、装配依赖、启动服务和优雅退出，不放业务逻辑。
- 文件应按职责拆分；一起变化的代码放在一起，而不是机械按 controller/service/repository 三层拆散领域逻辑。

## 5. 通用工作方式

- 开始编码前确认当前任务属于 `PLAN.md` 的哪个版本和交付项。
- 多步骤功能先形成设计与实施计划；不要边写代码边决定核心边界。
- 一次变更只解决一个可验证的问题，不夹带无关重构。
- 新行为和缺陷修复应先写失败测试，确认失败原因正确后再实现最小修改。
- 不覆盖或清理用户已有的未提交改动。
- 不直接修改生成文件、构建产物、依赖目录或锁文件中的手工内容；应修改源文件并通过标准命令重新生成。
- 不通过降低断言、跳过测试、吞掉错误或扩大超时来“修复”失败。
- 完成声明必须附带本次实际运行过的验证命令和结果；不得根据代码外观推测通过。
- 只有具备测试或可复现验证证据的 `PLAN.md` 项目才能勾选完成。

## 6. Go 工程规范

### 6.1 基本原则

- 代码应简单、直接、可预测；清晰优先于技巧。
- 尽可能让类型的零值可用。
- 接受接口、返回具体类型；接口定义在消费方。
- 接口应小而聚焦，通常为 1–3 个方法；不要提前为每个结构体创建接口。
- 使用显式依赖注入，禁止可变的包级全局状态和隐式 Service Locator。
- 避免带副作用的 `init()`；初始化失败必须由入口显式返回和处理。
- 仅在存在多个真正可选且稳定的构造参数时使用 Functional Options，不要把它当默认构造模式。
- 同一类型的 receiver 选择保持一致；包含锁、较大结构或需要修改状态时使用指针 receiver。
- 包名使用简短、小写、无下划线名称，避免 `service`、`manager` 等无信息后缀。
- 文件名使用小写 snake_case；测试文件使用 `_test.go`。
- 优先标准库；增加第三方依赖前说明用途、维护状态、许可证、镜像体积和替代方案。
- 除非有明确且记录过的必要性，避免 CGO，以保持一体化镜像的多架构构建能力。

### 6.2 Context 与并发

- 可能阻塞、访问磁盘、调用进程、数据库或网络的公共方法，首个参数必须是 `context.Context`。
- 不把 `context.Context` 存进结构体，不传 `nil` Context。
- 所有外部进程、网络请求、SSE 任务和沙箱操作必须有取消与超时。
- 每个 goroutine 必须有清晰的所有者、退出条件和回收路径。
- 禁止无界 goroutine、无界队列和无法取消的 channel send。
- 并发任务优先使用 `errgroup` 或等价的结构化并发方式传播错误和取消。
- 共享状态优先封装在拥有明确不变量的类型中；不要依赖散落的锁。
- 服务停止时必须先停止接收新任务，再取消后台任务、等待清理并在超时后返回明确错误。

### 6.3 错误处理

- 错误是值，必须返回、分类或明确处理；禁止使用 `panic` 处理用户输入、I/O 或运行时错误。
- 使用 `%w` 包装底层错误并增加动作和对象上下文，例如 `validate nginx config: %w`。
- Go 错误文本使用小写开头且不加句号。
- 需要调用方分支处理时使用稳定的 sentinel error 或自定义错误类型，并通过 `errors.Is` / `errors.As` 判断。
- 不通过字符串包含关系判断错误类型。
- 禁止无说明地使用空白标识符忽略错误。best-effort cleanup 也应记录或注释忽略原因。
- 错误只在有处理责任的边界记录一次，避免每层重复日志。
- API 层把领域错误转换为稳定错误码；不得把内部路径、命令行、堆栈或密钥直接返回给浏览器。

### 6.4 文件系统与外部进程

- 禁止拼接命令字符串或使用 `sh -c` 执行 Nginx 操作。
- 必须使用 `exec.CommandContext` 和独立参数调用受白名单约束的可执行文件。
- 命令路径、参数、超时、输出大小和允许的退出码必须显式定义。
- 所有来自用户或配置的路径都要 canonicalize，并验证仍位于允许的根目录内。
- 必须考虑符号链接、路径穿越、TOCTOU、跨文件系统 rename 和文件权限。
- 配置写入使用同目录临时文件、正确权限、`fsync` 和原子 rename；不能直接截断生产文件。
- 生产发布前后都执行完整 `nginx -t`；失败不得 reload。
- `nginx -t` 只代表语法及依赖可读取，不得当作路径匹配成功的证据。
- 沙箱必须使用独立 prefix、PID、日志、临时目录和随机回环端口，并在成功、错误、取消和进程崩溃后清理。
- 特权 Agent 只暴露类型化操作，不接受任意命令、任意路径或透传 shell 参数。

### 6.5 日志与敏感信息

- 使用结构化日志，字段名保持稳定；日志必须包含 request ID 或 task ID 等关联标识。
- 日志记录动作、对象、结果和耗时，不记录完整私钥、密码、Cookie、Authorization、ACME account key 或请求 Body。
- 只在进程入口决定日志级别和输出；领域包不读取全局环境变量决定行为。
- 用户可见错误与内部诊断分离；详细诊断写入受保护日志，UI 显示可操作且脱敏的信息。

### 6.6 Go 测试与工具链

- 测试使用标准 `testing` 包作为基础，表驱动测试用于规则组合，不强制引入测试框架。
- 包专属 fixture 放在包内 `testdata/`；跨模块 Nginx 样本放在 `tests/fixtures/nginx/`。
- 测试应确定、可并行时才调用 `t.Parallel()`，不能共享固定端口或可变全局状态。
- 文件测试使用 `t.TempDir()`；环境变量使用 `t.Setenv()`；清理使用 `t.Cleanup()`。
- 测试 Nginx 行为时使用真实 Nginx 二进制和固定 fixture，不用 mock 伪装匹配结果。
- 外部依赖通过小接口隔离，接口定义在被测包中。
- v0.1 工程骨架必须提供下列质量命令：

```bash
go test ./...
go test -race ./...
go vet ./...
golangci-lint run
go mod tidy
go mod verify
```

- 提交前对变更的 Go 文件运行 `gofmt` 和 `goimports`。
- `go.mod`、构建镜像和 CI 使用一致且明确固定的 Go 版本；禁止浮动到未验证版本。

## 7. API 与数据规范

- HTTP 路径以 `/api/v1` 开头，使用资源名词和标准方法语义。
- 请求和响应必须有显式 DTO；不要把数据库模型或 Nginx AST 直接暴露为 API。
- JSON 字段使用 `snake_case`，时间使用带时区的 RFC 3339，时长使用明确单位。
- API 错误使用稳定结构：错误码、用户可读消息、可选字段级详情和 request ID。
- 列表接口从首次实现起定义稳定排序；需要分页的数据不得返回无界结果。
- 会改变配置、证书或进程的操作必须记录审计事件。
- 长任务使用可查询状态机和 SSE；客户端断开不等于任务自动成功或失败。
- SQLite 只保存用户、会话、逻辑分组、任务、审计、证书元数据和备份索引。
- 数据库迁移必须单调、可重复验证；已发布迁移不得原地修改。
- 多文件配置发布和相关元数据更新必须设计明确的一致性与恢复顺序，不能假设文件系统与 SQLite 共享事务。

## 8. 前端工程与设计规范

### 8.1 Vue 与 TypeScript

- 使用 Vue 3 Composition API 和 `<script setup lang="ts">`。
- TypeScript 开启 strict；禁止无说明的 `any`、`@ts-ignore` 和非空断言滥用。
- 组件 Props、Emits、API DTO、路由参数和表单模型必须有显式类型。
- 状态默认放在最接近使用位置；只有跨路由或跨模块共享的状态才进入全局 store。
- 组件应围绕一个交互职责拆分；页面组件负责编排，不承载通用领域逻辑。
- API 调用集中在类型化 client 层；组件不得散落拼接 URL 或解释后端内部错误。
- 长任务 UI 必须支持进行中、成功、失败、取消、超时和需要人工处理状态。
- 前端标准包管理器使用 npm，仓库只保留 `package-lock.json`，不得混用 pnpm/yarn 锁文件。
- 本地开发和构建必须使用项目级 `web/node_modules`；禁止依赖全局安装的前端工具或包。
- Node 和 npm 版本必须在仓库及构建镜像中明确固定；可复现安装使用 `npm ci`。

#### Node 与 npm 本地环境

- 本仓库的本地开发、测试和构建必须使用 nvm 管理的 Node.js 24 环境，不得直接使用系统 Node.js 或系统 npm。
- 每个新终端或非交互 Shell 在执行任何 `npm`、`npx` 或前端质量命令前，必须先显式加载 nvm，并切换到 `web/package.json` 的 `engines.node` 所声明版本；当前命令为：

```bash
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
. "$NVM_DIR/nvm.sh"
nvm use 24.17.0
```

- 切换后必须运行 `node --version` 和 `npm --version`，确认分别符合 `web/package.json` 的 `engines.node` 与 `engines.npm`；当前应为 Node.js `v24.17.0`、npm `11.13.0`。
- 出现 `EBADENGINE` 或版本不匹配时必须先修正 nvm 环境，不得忽略警告继续执行或据此声明前端门禁通过。

### 8.2 `DESIGN.md` 是强制设计基线

- 实现任何页面或组件前，必须读取 `DESIGN.md` 对应 token、组件、Do/Don't 和响应式章节。
- 颜色、字体、间距、圆角、阴影和断点应映射为集中设计 token；禁止在业务组件中散落硬编码十六进制颜色。
- 交互主色只有 Action Blue。不得为品牌或装饰引入第二强调色。
- 卡片、按钮和文本不使用装饰阴影；设计文档定义的 product shadow 不应用于后台 UI chrome。
- 不使用装饰渐变。
- 使用系统字体栈；不得未经许可分发 SF Pro 字体文件。非 Apple 平台需要替代字体时遵循 `DESIGN.md` 的建议。
- 触控目标不得小于 44×44px；键盘焦点必须清晰可见。
- 响应式实现遵循 `DESIGN.md` 的断点与折叠策略，不只适配桌面宽度。
- 运维后台必需但 `DESIGN.md` 未覆盖的表格、树、编辑器、diff、Modal、Toast、状态徽标和错误态，必须先补充设计 token/组件规范再实现。
- 成功、警告、错误等语义状态不能只依赖颜色；如确需新增语义色，必须先更新 `DESIGN.md`，并保持其只用于状态而非品牌强调。
- 可访问性目标为 WCAG 2.2 AA；语义 HTML、键盘操作、焦点顺序、标签和对比度优先于视觉模仿。

### 8.3 前端质量门禁

v0.1 工程骨架必须在 `web/package.json` 提供以下脚本对应的能力：

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
npm --prefix web run test:e2e
```

- 纯逻辑使用单元测试，组件交互使用组件测试，关键管理闭环使用浏览器端到端测试。
- 不用脆弱的大型快照代替行为断言。
- 关键页面至少验证桌面、平板和手机布局以及键盘操作。
- 设计验收应检查 token 使用、溢出、对齐、响应式和可访问性，不只比较单张截图。

## 9. Docker 与运行时规范

- 官方镜像必须一体化包含 UI、特权 Agent、进程监督器和 Nginx。
- PID 1 必须正确转发信号、回收子进程并支持优雅停止。
- UI/Web 进程以非 root 用户运行；特权 Agent 使用最小权限并仅监听 Unix Socket。
- Nginx master 与 worker 权限按其职责分离；证书私钥默认权限不得高于 `0600`。
- 基础镜像和工具链版本必须固定；发布镜像不得使用未经验证的浮动 `latest`。
- 采用多阶段构建；最终镜像不包含编译器、源码缓存、测试密钥和包管理缓存。
- 至少构建 linux/amd64 与 linux/arm64。
- 默认端口为 Nginx `80/443` 和 UI `9000`。
- `/etc/nginx` 与 `/var/lib/nginx-uix` 必须可持久化。
- 只有 `/etc/nginx` 完全为空时才能写入默认配置；非空目录不得被镜像默认值覆盖。
- 健康检查必须分别验证 UI、Agent 和真实 Nginx 进程，不能只验证 PID 1。
- 容器停止、升级和重建必须保留配置、数据库、备份和证书。
- 禁止要求 `--privileged` 或 Docker Socket；新增 Linux capability 必须逐项说明原因。

## 10. 安全规范

- v0.1 起即要求认证、安全 Cookie、CSRF 防护、会话过期和登录速率限制。
- 所有输入在系统边界校验；不要依赖前端校验保护后端。
- 对文件名、路径、域名、URI、Header、Nginx 参数和命令参数分别进行上下文相关验证。
- 不允许通过 API 执行任意 Shell、读取任意宿主机文件或访问任意目标 URL。
- 路径实验室的请求目标固定为沙箱回环地址；Host/SNI 只是请求语义，不改变连接目标。
- 非幂等路径测试必须二次确认并记录审计。
- 密码使用适合口令的自适应哈希算法；不得自行设计密码学协议。
- 证书和 ACME 密钥不得进入普通日志、API 错误、配置 diff 或前端状态缓存。
- 数据库、备份和证书目录必须在启动时检查权限。
- 安全相关默认值采用 fail closed；无法确认发布或回滚状态时必须显示“需要人工处理”，不能报告成功。

## 11. 综合测试与完成定义

测试层次必须与风险匹配：

- Go 单元测试：领域规则、错误映射、状态机、路径约束。
- Golden tests：Nginx AST 解析和局部渲染。
- 真实 Nginx 集成测试：`nginx -t`、reload、沙箱和 location 匹配。
- SQLite 集成测试：迁移、事务和恢复。
- ACME 测试：仅使用本地 ACME 测试服务或 staging，不消耗 production 配额。
- 前端测试：逻辑、组件、响应式和浏览器端到端。
- Docker smoke tests：首次启动、已有卷、信号、健康检查、重建和升级。
- 故障注入：磁盘满、只读目录、端口冲突、Nginx 失败、超时、取消和进程崩溃。

一个任务只有在以下条件全部满足时才完成：

- 对应需求和验收条件有明确证据。
- 相关测试先失败后通过，或对纯文档/机械变更给出适当验证。
- 格式化、静态检查、单元测试和相关集成测试通过。
- 错误路径、取消路径和清理路径已验证。
- 安全、日志、迁移、文档和 UI 状态已按变更范围同步更新。
- 没有通过跳过测试、忽略错误或降低标准掩盖问题。

## 12. 文件头注释规则

### 12.1 强制要求

每次创建任何新代码文件时，必须在文件顶部添加包含 `@author` 和 `@since` 的文件头注释。非代码文件和生成文件不添加。

创建本次会话的第一个代码文件前，必须实际执行：

```bash
NAME=$(git config user.name)
EMAIL=$(git config user.email)
```

- author 格式为 `姓名 <邮箱>`。
- 未配置 email 时只写姓名。
- name 也未配置时使用 `whoami`。
- 同一项目目录、同一会话可复用结果；切换项目后必须重新获取。
- 不得硬编码 author 或从示例复制。

`@since` 按以下优先级实际读取：

| 项目类型 | 版本来源 |
| --- | --- |
| Maven | `pom.xml` 的项目自身 `<version>` |
| Gradle | `build.gradle` / `build.gradle.kts` 的 `version` |
| Node.js / TypeScript | `package.json` 的 `version` |
| Python | `pyproject.toml`、`setup.py`、`__version__` |
| Rust | `Cargo.toml` 的 `[package] version` |
| Go | `go.mod` 附近的 `VERSION`，否则最近 git tag |
| Ruby | `*.gemspec` 或 `Gemfile.lock` |
| PHP | `composer.json` 的 `version` |
| .NET | `*.csproj` 的 `<Version>` |
| 其他 | 最近 git tag |
| 全部不存在 | `date +%Y-%m-%d` |

版本号原样使用，不自行增加或删除 `v` 前缀。

### 12.2 注释格式

Java、Kotlin、Scala 在 `package` 之后、`import` 之前使用：

```java
/**
 * @author <a href="mailto:zhangsan@example.com">张三</a>
 * @since 1.2.3
 */
```

没有 email 时 `@author` 只写姓名。

TypeScript、JavaScript、TSX、JSX、C、C++、头文件、C#、Go、Rust、Swift、PHP、CSS、SCSS 使用：

```text
/**
 * @author 张三 <zhangsan@example.com>
 * @since 1.2.3
 */
```

Python 在 shebang 和编码声明之后、import 之前使用：

```python
"""
@author: 张三 <zhangsan@example.com>
@since: 1.2.3
"""
```

Ruby、Shell、YAML、TOML、R 使用 `#`：

```text
# @author 张三 <zhangsan@example.com>
# @since 1.2.3
```

Shell 注释放在 shebang 之后。

Lua、SQL 使用 `--`：

```text
-- @author 张三 <zhangsan@example.com>
-- @since 1.2.3
```

HTML、XML、Vue、Svelte 使用：

```html
<!--
  @author 张三 <zhangsan@example.com>
  @since 1.2.3
-->
```

未知扩展名使用该语言最常见的块注释或行注释。存在 shebang 或编码声明时，文件头放在其后。

### 12.3 禁止事项

- 不为 `.md`、`.txt`、`.json`、lockfile、`.gitignore` 等非代码文件添加文件头。
- 不为 `dist/`、`build/`、`node_modules/` 等生成内容添加文件头。
- 不修改已有文件头，除非用户明确要求。
- 不因 lint 或格式化工具不识别注释而删除文件头；应配置工具保留它。

## 13. 文档和版本维护

- `VERSION` 是 Go 项目版本和新代码文件 `@since` 的首要来源。
- Go 构建元数据、`web/package.json`、镜像标签和 `VERSION` 的应用发布版本必须一致；`go.mod` 只固定模块路径、依赖和 Go 工具链版本。
- 产品范围、版本顺序或验收标准改变时更新 `PLAN.md`。
- 新增或改变视觉 token、组件和交互规则时更新 `DESIGN.md`。
- API 行为改变时同步更新 `api/v1` 契约和客户端类型。
- 数据格式改变时同步更新迁移、备份恢复文档和升级测试。
- 文档示例必须可执行且不包含真实域名密钥、私钥或生产凭据。

## 14. 明确禁止的做法

- 绕过工作副本直接写生产 Nginx 配置。
- 配置校验失败后继续 reload。
- 用 `nginx -t` 冒充路径匹配测试。
- 在生产 Nginx 上做未发布配置的路径实验。
- 在 Go 中拼接 Shell 命令或把用户输入透传给进程。
- 在 SQLite 中保存一份可反向覆盖 Nginx 文件的配置副本。
- 让前端直接依赖数据库模型或 Nginx AST 内部结构。
- 在业务组件中硬编码与 `DESIGN.md` 冲突的颜色、阴影、圆角和断点。
- 为了“以后可能需要”提前建立抽象、接口、插件系统或分布式组件。
- 未验证就更新 `PLAN.md` 状态或宣称功能完成。
