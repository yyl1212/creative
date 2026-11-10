# Creative

从真实需求出发的工具平台。新流程由外部 OpenAI/DeepSeek 智能体完成搜索、原文解析、分析、去重整合和立项判断，通过后调用网站接口导入为“已立项、待开发设计”。网站提供契约校验、幂等存储、证据留痕和后续开发/发布管理；旧资料可查，站内候选核验与人工立项入口已停用。公开产品清单为 `[]`，不把测试样例或研究结论当成已上线产品。当前页面范围为 macOS 桌面浏览器。

定时搜寻保持禁用。已提供 Codex 提示词任务、新立项包契约、完整需求目录接口和固定 outbox 传输工具；真实联网及跨运行验收通过前不启用。见 [外部模型立项运行说明](docs/agent-project-operations.md)、[验证记录](docs/agent-project-validation.md)；旧流程见 [运行手册](docs/research-task-operations.md)。

外部模型审查使用 [单候选 JSON 契约](schemas/agent-review.v1.schema.json)，先执行 `creative-admin review-check --file <审查JSON>` 去重计数，提交前加 `--project <立项包JSON>` 核对一致性。失败响应不自动修补；流程与限制见运行说明。

管理后台主导航为总览、需求池、开发工作区、导入记录、费用与用量五项。日期日报和备份状态并入总览，接收会话并入导入记录，智能体接入位于页头次级入口；开发技术表单可展开登记。搜寻、分析和立项判断全部由外部 Codex 完成；网站不加载模型配置、不抓取原文、不消费旧研究队列。发布与备份恢复仍通过受控 CLI，维护循环保留数据清理和最近 7 份一致性备份。见 [运维手册](docs/operations.md)。

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
| `CREATIVE_RESEARCH_ORIGIN` | 默认空，关闭机器接口；本地可用 `http://127.0.0.1:8080`，必须与管理 Host 不同，非本机须 HTTPS |
| `CREATIVE_LISTEN` | `127.0.0.1:8080`；默认仅本机监听 |
| `CREATIVE_CSRF_KEY` | 必填，32 字节密钥的 64 位十六进制表示 |
| `CREATIVE_PRODUCTS` | `src/data/products.json`；启动时校验并读取，清单改变后重启以刷新总览；缺失/损坏拒绝启动 |
| `CREATIVE_TRUSTED_PROXY` | 默认空，忽略 X-Real-IP；填写明确的代理 IP 后仅信任来自该地址的 X-Real-IP |

公开产物只读取三项公开配置；后台/研究数据不进入 Astro。后台不接受 Bearer token 作为管理员凭证。写操作需要 CSRF，GET 注销不生效。会话用 SCS + SQLite 持久化，30 分钟空闲/12 小时绝对有效期，登录轮换、注销销毁。HTTPS 配置启用 Secure/HttpOnly/host-only Cookie。后台私有响应 no-store/noindex，模板自动转义，无第三方脚本。

当前总览的需求和完成数量来自 SQLite，产品来自公开清单；完成数量仅计通过生产核验的需求。未接入的流量和收入不展示，历史合格资料仍保存在需求池。生产代理必须保持原管理 Host、覆盖 X-Real-IP 并限制后台直连；TLS 和代理部署另行验收，本轮未部署。

## 外部研究结果接收

1. 在「智能体接入」登记 Codex 并私下保存一次展示的 Bearer token。研究 API 默认关闭，启用时必须使用与管理后台不同的 Host。
2. 外部 Codex 在获得授权后，通过 `creative-admin intake-create` 登记接收主题、语言和目标。创建/取消/恢复会话不执行搜寻，也不会启用 Codex 日程；完整命令见 [运行说明](docs/agent-project-operations.md)。后台「导入记录」中的接收会话只读展示状态。
3. Codex 读取 `GET /api/research/v1/task` 与 `GET /api/research/v1/catalog`，完成搜索、原文分析、去重和立项判断。通过后按 [立项包契约](schemas/project-package.v1.schema.json) 提交 `POST /api/research/v1/projects`，直接登记为已立项、待开发设计。
4. 无通过项目时，允许提交 `POST /api/research/v1/batches` 的空 `candidates=[]` 运行报告。新报告仅接受活动且有缺口的外部立项会话，同步保存用量与状态并返回 `completed`，不触发站内核验。新非空旧候选包返回 410。
5. 通过批次回执确认完成，在「导入记录」「需求池」「开发工作区」管理资料。网站不自动恢复等待或暂停会话；取消/恢复由外部 Codex 按授权通过私有 CLI 完成。

同一身份与批次 ID 的原字节重试返回原接收回执，不同字节 409；旧包即使已取消也可查询和原样重试。旧任务的 `should_continue` 固定为 false。已有队列保留为历史记录，不再继续执行。旧后台研究写入口返回 410，旧模型设置书签跳转费用页；无需数据库迁移。

单包 1 MiB、单执行端 60 请求/分钟、进程 8 并发仍作为传输保护，不是总搜索次数或费用上限。认证、Host、CSRF、JSON Schema 与归属校验保持。无效 JSON 400，身份无效 401，未分配任务/回执 404，冲突或非活动会话 409，退役操作 410，过大 413，Schema/内容类型错误 422，暂不可用 503；错误不回显原文或内部堆栈。

## 费用与历史资料

模型调用只记录 OpenAI/DeepSeek，非模型工具的 model 为 null；未知费用保持 null。费用页区分外部自报、已核账与未知，不把推测金额当实际账单。网站已停止自身模型调用，历史费用仍可查询和核账。仓库 `testdata` 全为隔离测试材料，不能导入真实需求池。

维护循环启动时执行一次，此后每日上海时间 08:00 后执行。原始页面与批次字节保留 30 天，每次核验的 Decision、来源摘要与研究资料保留 180 天，补证不删除旧核验版本；过期原始包仍保留哈希和初次接收回执，保障原字节幂等重试。必要费用、审计、完成需求和任务配置保留。清理不修改已登记迁移，也不删除公开产品。

## 数据、迁移与回退

SQLite 使用 WAL、foreign_keys、5 秒 busy_timeout 和单连接。首次启动按顺序执行 `migrations/001_initial.sql`、`002_research.sql`、`003_verification.sql`、`004_lifecycle.sql` 并登记 SHA-256；原有数据库追加缺失迁移，保留账户、会话、审计、需求与接收队列；已登记 SQL 校验和变化、未知版本会拒绝启动。后续变更必须追加迁移，升级前备份，不修改已执行文件。不要将活动 WAL 数据库只复制 `.db`；停服务并关闭连接后再复制，或使用 SQLite 的一致性备份能力。

本轮未操作生产数据库。005 新增外部项目导入来源关联表，旧程序不能打开已升级数据库；升级前备份及回退方式同下述要求。003 增加核验状态、租约、来源版本和费用记录，并为旧候选建立别名；同一迁移事务回填 002 的费用报告，复用在线费用去重和冲突规则；旧程序会拒绝打开含 003 的库。回退时停服务，恢复升级前的一致性备份并使用旧代码，升级后新增材料须另行保全；不要用降级 SQL 静默丢弃资料。回退代码前保留完整私有备份；不要通过删除数据库来回退生产数据。`data/`、环境文件、构建产物与开发流水记录均不提交。会话、审计、密码哈希与失败计数也属于私有数据。

## 验证

所有检查均为本地测试，不使用真实模型、搜索或支付服务。单个测试批次必须有外部不超过 10 分钟的截止时间；CI 中每个测试步骤使用 `timeout 570s`。

```sh
# 同样的自制哨兵用于 CI，不能换成真实密钥。
ASTRO_TELEMETRY_DISABLED=1 DEEPSEEK_API_KEY=creative-test-sentinel-only OPENAI_API_KEY=creative-openai-sentinel-only npm run build
npm test
CGO_ENABLED=0 go test ./... -count=1 -timeout 9m
CGO_ENABLED=0 go build ./cmd/creative ./cmd/creative-admin
CGO_ENABLED=0 go vet ./...
python3 -m unittest discover -s tests -p 'test_research_transport.py' -v
```

Node 检查实际构建产物的路由、零产品、双语 SEO、noindex、公开配置与哨兵隔离。Go 集成测试覆盖认证边界、登录轮换、注销/失效/恢复、持久化重启、CSRF、账号/来源限流、真实计数和迁移漂移。验收结果与剩余条件见 [迭代交付记录](docs/iteration-01-validation.md)。

## 项目结构

```text
src/                    Astro 用户站、文案、空产品清单、公开配置
cmd/creative/           Go 管理与研究 API 服务
cmd/creative-admin/     私有管理员与迁移命令
internal/auth/          密码、账户有效性、限流与审计
internal/httpserver/    会话、CSRF、管理路由与模板渲染
internal/store/         SQLite 打开与迁移校验
internal/research/      外部立项导入、接收、历史资料、开发与费用
internal/source/        防 SSRF 原文抓取、作者与日期绑定
internal/model/         OpenAI Responses / DeepSeek Chat 语义复核
internal/worker/        数据保留与备份维护（不消费研究队列）
internal/usage/         在线接收与迁移共用的外部费用记录
internal/jsonutil/      接收包与模型结果共用的严格 JSON 检查
config/                外部提示词任务配置与历史模型配置
schemas/                内嵌需求批次 v1 JSON Schema
migrations/             版本化 SQL
web/admin/              内嵌管理模板与自制 CSS
tests/                  公开产物检查
docs/                   中文方案、计划、运维与验收记录
```

范围按 [敏捷开发计划](docs/superpowers/plans/2026-11-09-creative-agile-delivery.md) 推进；迭代 3 离线接收/核验闭环已实现；定时任务真实接入和开发交接属于后续迭代。无合格真实需求时不预建业务工具。

管理页面旧地址 `/admin/reports` 与 `/admin/tasks` 仍可访问，分别展示合并后的总览和导入记录；日期筛选、批次分页、原始包、历史证据与原权限边界保持。
