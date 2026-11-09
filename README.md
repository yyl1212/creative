# Creative

从真实需求出发的工具平台。当前已实现双语空站、安全管理登录、研究任务与候选接收，以及迭代 3 的原文核验、需求池、两家模型适配和费用记录。产品清单为 `[]`，生产需求库初始为空。模型默认禁用，未接入真实搜寻、付费模型调用、广告、支付、收入或访问统计。本轮只验收 macOS 桌面浏览器，其他终端不在当前范围。

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
| `CREATIVE_RESEARCH_ORIGIN` | 默认空，关闭机器接口；本地可用 `http://127.0.0.1:8080`，必须与管理 Host 不同，非本机须 HTTPS |
| `CREATIVE_LISTEN` | `127.0.0.1:8080`；默认仅本机监听 |
| `CREATIVE_CSRF_KEY` | 必填，32 字节密钥的 64 位十六进制表示 |
| `CREATIVE_PRODUCTS` | `src/data/products.json`；启动时校验并读取，清单改变后重启以刷新总览；缺失/损坏拒绝启动 |
| `CREATIVE_PROVIDERS_CONFIG` | `config/providers.json`；仅 OpenAI/DeepSeek，默认均禁用，模型名称必须显式填写 |
| `CREATIVE_TRUSTED_PROXY` | 默认空，忽略 X-Real-IP；填写明确的代理 IP 后仅信任来自该地址的 X-Real-IP |

公开产物只读取三项公开配置；后台/研究数据不进入 Astro。后台不接受 Bearer token 作为管理员凭证。写操作需要 CSRF，GET 注销不生效。会话用 SCS + SQLite 持久化，30 分钟空闲/12 小时绝对有效期，登录轮换、注销销毁。HTTPS 配置启用 Secure/HttpOnly/host-only Cookie。后台私有响应 no-store/noindex，模板自动转义，无第三方脚本。

当前总览的需求/合格/完成数量来自 SQLite，产品来自公开清单，不代表生产发布已核验。缺失运营指标显示“未接入 / 未知”。生产代理必须保持原管理 Host、覆盖 X-Real-IP 并限制后台直连；TLS 和代理部署另行验收，本轮未部署。

## 研究任务与候选接收

启动 Go 服务时设置 `CREATIVE_RESEARCH_ORIGIN=http://127.0.0.1:8080`。同一个监听端口按 Host 分流：浏览器使用 `http://localhost:8080/admin/`，执行端使用 `http://127.0.0.1:8080/api/research/v1/`。正式环境请配置独立 admin/API 主机和 HTTPS，代理保留原 Host；不允许拿管理 Cookie 调用机器接口。

1. 管理员进入“执行端”，登记名称并私下保存本次显示的令牌。服务端只保存 SHA-256 摘要；重新生成立即撤销旧令牌。
2. 进入“搜寻任务”，选择执行端、主题、中文/英文和合格需求目标（默认 3）。每个执行端只有一个当前任务；取消后可恢复，或创建新任务替代旧任务。
3. 执行端使用 `Authorization: Bearer <私有令牌>` 读取 `GET /api/research/v1/task`。无任务返回 `{"task":null}`；任务携带完整 `strong-wtp.v1` 硬规则及当前状态。
4. 按 [v1 Schema](schemas/demand-batch.v1.schema.json) 提交 `POST /api/research/v1/batches`，Content-Type 为 application/json；先在执行平台私有工件中保存稳定批次 ID 和准确载荷字节。不要把实际令牌写入命令历史、样例或提示词。
5. 202 与 `Location`/`receipt_url` 只代表可靠接收。首次 GET 回执为 `pending_validation`，逐项 outcome/need_id 为 null；工作循环随后给出 `qualified/needs_evidence/duplicate/rejected`。模型未配置或技术故障时批次为 `paused_service`，未完成项仍为 null。到“提交批次”“需求池”查看原因及当前有效资格。

单包 1 MiB / 20 候选、单执行端 60 请求/分钟和进程 8 个并发是传输保护；返回 429 与 Retry-After 后可以继续，不是总搜寻次数或费用上限。无效 JSON（含重复键）400，身份无效 401，未分配任务/回执 404，冲突/取消/旧规则 409，过大 413，Schema/内容类型错误 422，暂不可用 503。API Host 不符为 421，方法错误为 405。所有错误为 `{"error":{"code":"…","field":"…","message":"…"}}`，不会回显请求内容或内部堆栈。

重试必须保留原始字节（空格也属于内容）；相同身份与批次 ID 返回原接收回执，不同字节为 409。取消后拒绝新批次，仍允许查询及原样重试已入库批次；读取任务不会撤销取消。不同执行端的批次隔离。原始包、候选索引、运行费用报告和待验证状态在同一事务保存。网站进程启动持久核验循环，用数据库租约和逐候选检查点恢复；失去租约的旧进程不能写入结果，崩溃后租约最多约 2 分钟到期。模型默认禁用时明确暂停，不访问外部原文或模型。

`internal/research/testdata/batch.json` 只有隔离虚构证据，测试全部使用临时库，不得导入真实需求池。替代方案字段使用 name/url/checked_at/method/limitations/basis；模型用量仅接受 OpenAI/DeepSeek 标识，非模型调用使用 model:null，未知费用保持 null，不冒充已核对费用。详见 [迭代 3 子计划](docs/superpowers/plans/2026-11-09-creative-verification.md) 与 [验收记录](docs/iteration-03-validation.md)。

## 核验与费用操作

1. `config/providers.json` 仅含非敏感配置。`primary` 选择主厂商；`fallback` 默认为空，只有明确启用、验证并配置的备选厂商才参与故障切换。模型 ID 不预设账户可用性；启用前完成对应真实连接验证，再设 `enabled/validated` 并由私有进程环境提供凭据。配置变更后重启服务，不在网页或仓库填写密钥。
2. OpenAI 使用 Responses 的严格 JSON Schema、`store:false`；DeepSeek 使用 Chat Completions 的 JSON 模式。拒绝、截断、空白、重复字段或格式异常均不产生资格。格式修正最多一次、单次 HTTP 额外重试最多两次；这不限制总搜寻次数。
3. 来源仅接受公开 HTTP(S) 的 80/443 端口，连接时校验并固定公网地址，逐跳检查重定向，拒绝登录/非正文响应和超大页面。只支持可明确绑定作者与时间的静态文章/评论；不绕过登录、验证码或访问限制。未知元数据、不可访问来源返回补证。
4. 程序核对 3 名独立用户、2 个原始站点、2 名直接付费用户及价格/既有消费依据；重复用户或引文不累计。资格最多 30 天且不超过付费原文的 12 个月时限。补证使用原 external_id、新 batch_id，入库即撤销旧资格；已完成需求不会被补证覆盖。标题不参与单独去重。
5. 到“提交批次”查看每项结果。平台故障在服务恢复后由管理员点击“重试未完成项”；已完成检查点不重跑。单来源失败按补证处理。当前有效资格变化会更新任务缺口；取消不会被定时器恢复。满 24 小时的等待线索任务可由维护循环恢复检查。
6. 网站调用在请求前记一笔未知费用，即使超时也保留；只记录实际返回的 token，用量不自动换算成金额。外部 usage_id 按执行端去重，可补齐未知信息，矛盾金额或重复账单标为冲突。页面按来源/币种分别精确汇总，自报金额不视为已核对；账单核对操作归迭代 5。

维护循环启动时执行一次，此后每日上海时间 08:00 后执行。原始页面与批次字节保留 30 天，来源摘要与研究资料保留 180 天；过期原始包仍保留哈希和初次接收回执，保障原字节幂等重试。必要费用、审计、完成需求和任务配置保留。清理不修改已登记迁移，也不删除公开产品。

## 数据、迁移与回退

SQLite 使用 WAL、foreign_keys、5 秒 busy_timeout 和单连接。首次启动按顺序执行 `migrations/001_initial.sql`、`002_research.sql`、`003_verification.sql` 并登记 SHA-256；原有数据库追加缺失迁移，保留账户、会话、审计、需求与接收队列；已登记 SQL 校验和变化、未知版本会拒绝启动。后续变更必须追加迁移，升级前备份，不修改已执行文件。不要将活动 WAL 数据库只复制 `.db`；停服务并关闭连接后再复制，或使用 SQLite 的一致性备份能力。

本轮未操作生产数据库。003 增加核验状态、租约、来源摘要和费用记录，并为旧候选建立别名；旧程序会拒绝打开含 003 的库。回退时停服务，恢复升级前的一致性备份并使用旧代码，升级后新增材料须另行保全；不要用降级 SQL 静默丢弃资料。回退代码前保留完整私有备份；不要通过删除数据库来回退生产数据。`data/`、环境文件、构建产物与开发流水记录均不提交。会话、审计、密码哈希与失败计数也属于私有数据。

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
cmd/creative/           Go 管理与研究 API 服务
cmd/creative-admin/     私有管理员与迁移命令
internal/auth/          密码、账户有效性、限流与审计
internal/httpserver/    会话、CSRF、管理路由与模板渲染
internal/store/         SQLite 打开与迁移校验
internal/research/      任务、接收、资格、去重、持久队列、需求池与费用
internal/source/        防 SSRF 原文抓取、作者与日期绑定
internal/model/         OpenAI Responses / DeepSeek Chat 语义复核
internal/worker/        进程内恢复循环与每日维护
config/                非敏感的模型配置
schemas/                内嵌需求批次 v1 JSON Schema
migrations/             版本化 SQL
web/admin/              内嵌管理模板与自制 CSS
tests/                  公开产物检查
docs/                   中文方案、计划、运维与验收记录
```

范围按 [敏捷开发计划](docs/superpowers/plans/2026-11-09-creative-agile-delivery.md) 推进；迭代 3 离线接收/核验闭环已实现；定时任务真实接入和开发交接属于后续迭代。无合格真实需求时不预建业务工具。
