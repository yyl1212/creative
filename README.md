# Creative

从真实需求出发的工具平台。当前已实现迭代 1 的代码（浏览器完整验收待补齐）：中英双语用户空站、独立 Go 管理入口、私有管理员初始化与持久化会话。产品清单为 `[]`，需求库初始为空。未接入模型、研究接口、广告、支付、收入或访问统计。

迭代 0 已完成主线/环境准备和定时平台隔离试验；本机定时任务的 HTTP 网络权限未通过，跨次恢复和串行验证仍阻塞，不能启用真实搜寻。详见 [定时任务运行记录](docs/research-task-operations.md)。

## 本地运行

需要 Node.js 24（本次 24.17.0）、npm、Go 1.27.3；Go 可通过标准工具链自动下载所需版本。公开站与后台使用不同端口，生产时应使用独立主机与 HTTPS。

```sh
npm ci
ASTRO_TELEMETRY_DISABLED=1 npm run build
npm run preview
```

打开 `http://localhost:4321/` 选择中文或英文。开发时用 `npm run dev`。Astro 7 的 preview 命令返回后服务仍运行，使用 `npx astro preview stop` 停止。

后台先创建私有账户，没有默认密码或公开注册。密码交互输入不回显，12–128 字节；账号 3–64 位字母、数字、下划线或连字符（保存为小写）。

```sh
CGO_ENABLED=0 go run ./cmd/creative-admin create-admin --username owner
export CREATIVE_CSRF_KEY="$(openssl rand -hex 32)"
CGO_ENABLED=0 go run ./cmd/creative
```

打开 `http://localhost:8080/admin/login`，使用刚创建的账号登录。生产重启应从权限受控的环境文件或密钥管理器加载同一 CSRF 密钥；不要在仓库保存真实值。程序不自动加载 `.env`。模型密钥不属于本轮运行条件。

```sh
# 私有恢复：重设密码、重新启用账号、撤销该账号所有现有会话，并清除账号失败计数。
CGO_ENABLED=0 go run ./cmd/creative-admin reset-password --username owner
# 禁用后现有会话立即失去权限。
CGO_ENABLED=0 go run ./cmd/creative-admin disable-admin --username owner
# 只初始化/校验迁移，不创建账号。
CGO_ENABLED=0 go run ./cmd/creative-admin migrate
```

自动化初始化可显式使用 `--password-stdin`，从私有文件描述符传入密码；不要把密码写进命令行、Git、日志或公开配置。登录按账号和来源各 5 次/15 分钟临时限制；恢复密码不清除整个来源的限制，来源限制自然到期后恢复。

## 配置与边界

`.env.example` 只含占位配置。复制后填写的文件应保持私有；所有 `.env*`（除示例）均被 Git 忽略。

| 配置 | 默认与用途 |
| --- | --- |
| `SITE_URL` | `http://localhost:4321`；公开站源地址，正式域名必须 HTTPS、无路径/凭据/查询参数 |
| `ADMIN_URL` | 未配置时隐藏公开站管理链接；可配置 `http://localhost:8080/admin/login` |
| `FEEDBACK_EMAIL` | 未配置时说明反馈尚未开放，不显示表单或虚构邮箱 |
| `CREATIVE_DB` | `data/creative.db`；私有 SQLite，文件权限 0600，自动创建的目录 0700 |
| `CREATIVE_ADMIN_ORIGIN` | `http://localhost:8080`；严格校验管理 Host 与写请求 Origin；非本机只允许 HTTPS |
| `CREATIVE_LISTEN` | `127.0.0.1:8080`；默认仅本机监听 |
| `CREATIVE_CSRF_KEY` | 必填，32 字节密钥的 64 位十六进制表示 |
| `CREATIVE_PRODUCTS` | `src/data/products.json`；启动时校验并读取，清单改变后重启以刷新总览；缺失/损坏拒绝启动 |
| `CREATIVE_TRUSTED_PROXY` | 默认空，忽略 X-Real-IP；填写明确的代理 IP 后仅信任来自该地址的 X-Real-IP |

公开产物只读取三项公开配置；后台/研究数据不进入 Astro。后台不接受 Bearer token 作为管理员凭证。写操作需要 CSRF，GET 注销不生效。会话用 SCS + SQLite 持久化，30 分钟空闲/12 小时绝对有效期，登录轮换、注销销毁。HTTPS 配置启用 Secure/HttpOnly/host-only Cookie。后台私有响应 no-store/noindex，模板自动转义，无第三方脚本。

当前总览的需求/合格/完成数量来自 SQLite，产品来自公开清单，不代表生产发布已核验。缺失运营指标显示“未接入 / 未知”。生产代理必须保持原管理 Host、覆盖 X-Real-IP 并限制后台直连；TLS 和代理部署另行验收，本轮未部署。

## 数据、迁移与回退

SQLite 使用 WAL、foreign_keys、5 秒 busy_timeout 和单连接。首次启动在事务内执行 `migrations/001_initial.sql` 并登记 SHA-256；已登记 SQL 校验和变化、未知版本会拒绝启动。后续变更必须追加迁移，升级前备份，不修改已执行文件。不要将活动 WAL 数据库只复制 `.db`；停服务并关闭连接后再复制，或使用 SQLite 的一致性备份能力。

本轮没有生产数据迁移。回退代码前保留完整私有备份；不要通过删除数据库来回退生产数据。`data/`、环境文件、构建产物与开发流水记录均不提交。会话、审计、密码哈希与失败计数也属于私有数据。

## 验证

所有检查均为本地测试，不使用真实模型、搜索或支付服务。单个测试批次必须有外部不超过 10 分钟的截止时间；CI 中每个测试步骤使用 `timeout 570s`。

```sh
# 同样的自制哨兵用于 CI，不能换成真实密钥。
ASTRO_TELEMETRY_DISABLED=1 DEEPSEEK_API_KEY=creative-test-sentinel-only OPENAI_API_KEY=creative-openai-sentinel-only npm run build
npm test
CGO_ENABLED=0 go test ./... -count=1 -timeout 9m
CGO_ENABLED=0 go build ./cmd/creative ./cmd/creative-admin
CGO_ENABLED=0 go vet ./...
```

Node 检查实际构建产物的路由、零产品、双语 SEO、noindex、公开配置与哨兵隔离。Go 集成测试覆盖认证边界、登录轮换、注销/失效/恢复、持久化重启、CSRF、账号/来源限流、真实计数和迁移漂移。验收结果与剩余条件见 [迭代交付记录](docs/iteration-01-validation.md)。

## 项目结构

```text
src/                    Astro 用户站、文案、空产品清单、公开配置
cmd/creative/           Go 管理 HTTP 服务
cmd/creative-admin/     私有管理员与迁移命令
internal/auth/          密码、账户有效性、限流与审计
internal/httpserver/    会话、CSRF、管理路由与模板渲染
internal/store/         SQLite 打开与迁移校验
migrations/             版本化 SQL
web/admin/              内嵌管理模板与自制 CSS
tests/                  公开产物检查
docs/                   中文方案、计划、运维与验收记录
```

范围按 [敏捷开发计划](docs/superpowers/plans/2026-11-09-creative-agile-delivery.md) 推进；研究 v1、证据核验、定时任务真实接入和开发交接属于后续迭代。无合格真实需求时不预建业务工具。
