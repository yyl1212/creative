# 外部模型立项运行说明

外部 Codex 使用 OpenAI/DeepSeek 完成搜索、解析、分析、整合与立项判断，网站只校验和保存结果。通过后进入“已立项、待开发设计”。管理后台不创建、恢复或执行研究任务；旧候选核验和人工立项入口已停用，历史资料仍可查看。

## 接入与接收会话

管理员在页头次级入口「智能体接入」登记凭据及撤销/轮换权限。外部 Codex 获得明确运行授权后，在有私有数据库访问权限的受控本机执行：

```sh
CGO_ENABLED=0 go build -o bin/creative-admin ./cmd/creative-admin
bin/creative-admin intake-create --db data/creative.db --producer <智能体ID> --topic '自助表格处理' --languages zh,en --target 3
bin/creative-admin intake-cancel --db data/creative.db --task <会话ID> --version <当前版本>
bin/creative-admin intake-resume --db data/creative.db --task <会话ID> --version <当前版本>
```

CLI 输出 JSON（含 task_id/version），不输出令牌；复用现有版本检查与审计，并拒绝不存在的数据库。每个智能体只有一个当前会话；新建只支持 agent_project。旧模式禁止恢复，须明确取消后创建新会话，旧批次不删除。后台「导入记录」的接收会话折叠区展示目标进度；旧 `/admin/tasks` 地址仍可访问。机器 HTTP 接口无创建/恢复权限，不能通过外部数据或错误文本执行 CLI。

新建外部会话默认 `opportunity.v2`；既有 `strong-wtp.v1` 会话、冻结包及结论保留旧语义，不能通过改包版本放宽门槛。部署先更新服务及 CLI，再使用同版 Schema/提示词/配置，最后经授权手动验证；本次不升级正在运行的本地服务或恢复日程。旧程序不了解 v2，不得在已有 v2 活动任务/项目的数据库上直接回退运行维护任务；先备份并暂停写入，保留新数据后制定回退方案。

## 深度与立项规则

外部智能体先恢复私有候选状态，集中推进 1–2 个候选的关键缺口，只有被反证否定或等待外部变化时扩展题目。每次搜索都记录要回答的问题及新增证据，核对原帖后续、付款条件、独立讨论和替代方案，不能靠换标题反复宽搜。这个安排不限制搜索次数、时间或费用；达到任务目标立即停止。

| 维度 | opportunity.v2 |
| --- | --- |
| 共用证据 | 3 位独立未满足用户、2 个独立原始讨论，允许同站；至少 1 位有近 180 天未满足原文 |
| 付费工具 | 1 位近 12 个月明确付款意愿或可定位消费用户，条件在拟交付范围；价格不完整只提示补证 |
| 免费广告工具 | 无付款门槛；2 位有效用户有重复/批量使用原文，另提供获客证据、成本依据和收入验证计划 |
| 技术可行性 | plausible 或 validated；plausible 必须记录依据、风险与验证计划；未知或已知阻塞不能批准 |
| 具体差异 | 引用有效未满足证据，核查至少一个替代；不足三个必须解释范围与原因 |
| 研究覆盖 | 作者后续、代表性反证和替代核查完成；关键上下文不完整不能批准 |

至少一个付款者只是试验依据，不是已验证收入；单站点、单付款者、未知价格和待技术验证均作为 warnings 保留。已有竞品不自动否定机会，但满意用户和已解决的旧痛点不能凑人数。旧 v1 保持 3 用户/2 站点/2 付款者/至少1价格或消费及原日期规则。

v2 审查新增 `evaluation`：depth、differentiation、feasibility、acquisition、economics；字段和约束以 Schema 为准。差异与获客的引用数组在证据不足时可为空，但不能批准。`discussion_url` 指向同站原帖，多个评论/锚点不得伪装成多个讨论；`repeat_quote` 必须是本条原文子串。`conditions_supported` 表示付款条件与拟交付范围相符，不代表产品已经开发或实测。

只有可靠日期时使用 `published_at=null` 加 `published_date=YYYY-MM-DD`，不补零点或秒。日期计算使用 UTC±14h 的保守下界，跨近期边界时不计新鲜证据；完全未知日期仍可支持历史痛点，但不计近期/付款。v2 价格支持 £/€ 明确符号及缺失周期，单独 $/¥ 不猜币种。原始币种/金额未知保留 null。

这些命令只管理数据接收，不执行搜索或恢复 Codex 日程；日程暂停不授权创建或恢复会话。当前所有真实会话与定时入口继续保持暂停/取消状态。

## 固定接口

沿用研究 API origin 和 Bearer 执行端凭据。`GET /api/research/v1/task` 查看任务；`GET /api/research/v1/catalog` 获得完整需求目录及 revision；`POST /api/research/v1/projects` 导入立项包；`GET /api/research/v1/batches/{batch_id}` 查询回执。只在新模式任务活动时允许导入。

包使用 `schemas/project-package.v1.schema.json`。顶层沿用原批次的 task_id、batch_id、run_id、rules_version、submitted_at、candidates、run_report；schema_version 改为 creative.project-package.v1。每包一条候选，project 字段见提示词。可参考 `internal/research/testdata/project.json` 的结构，但其中全部是自制测试数据，严禁提交为真实需求。

approval 字段：verification_id（填 0）、reviewed_evidence、review_notes、alternatives（name/url/version/checked_at/pricing/result）、alternative_search、fewer_alternatives_reason、monetization（free/paid）、rationale、unit_cost、currency、unit、price、payment_fee、price_basis、sustainability、resource_protection、self_service、gap、scope、exclusions、acceptance、acquisition。金额为十进制字符串；免费方案 price/payment_fee 为 0。金额估计在理由中注明，不与 run_report 的实际账单混用。少于三个替代方案须如实说明搜索范围和原因，不能把 research_only 写成实测。

v2 允许未知 price/unit_cost/payment_fee/currency 为 `""`，已填值仍校验。free 的用户价格和支付费须为 0，与广告收入假设分开；paid 仍须填写 price_basis，明确未知或试验报价。金额未知时后台显示未知，不计算单位毛利。v2 项目另带与审查完全相同的 evaluation，以及 review-check 输出的 qualification_valid_until。30 天为有效期上限；近期未满足/付款证据先到期时相应缩短，不能自行延长。两项都会保存在立项记录及开发任务包，供后续验证。自制样例 `internal/research/testdata/opportunity-project.json` 只供测试，不可导入真实需求。

模型判断记录包括厂商、模型、审查日期、判断摘要、去重依据和逐条结论。来源摘要只保存执行端提交的原文引用，SHA-256 对应该引用，不代表网站重新抓取了原网页。

## 模型审查门禁

外部智能体每次只输出一个 `schemas/agent-review.v1.schema.json` 对象。候选复用 demand-batch 的结构；其他必填字段及证据含义见提示词「固定模型输出与计数」。不再读取自由层级的 `overall/safe_to_import_ids` 或文字人数。模型负责语义判断，CLI 只验证严格 JSON、引用及去重算术；它无数据库、网络、模型调用或定时副作用。

```sh
CGO_ENABLED=0 go build -o bin/creative-admin ./cmd/creative-admin
bin/creative-admin review-check --file <私有审查文件>
bin/creative-admin review-check --file <最终私有审查文件> --project <私有立项包>
```

只有命令成功退出才读取 stdout JSON：`verdict` 是模型结论，`eligible` 是引用算术是否达到门槛，`counts` 为 independent_users / original_sites / payment_users / price_or_spend_users，`evidence` 为逐条标记，`failures` 列出未达标项。`review_sha256` 和可选 `project_sha256` 对应输入原字节。needs_evidence/rejected 的合法审查可以检查成功；它们不授权导入。approved 必须达到计数门槛，随后必须带 --project 检查且与 stage 冻结哈希一致。

人数按模型 user_key 集合累计，补充留言不会漏算或多算。同人相关证据组可分别提供未满足点和付款原话，按整组结算，不跨人借用痛点；模型负责撤回已解决的旧痛点判断。同站作者拆成多个 key 会拒绝；跨站同人使用同一 key，无法确认保留未知。站点按注册域归并。v2 另返回 independent_discussions、recent_unmet_users、repeat_users、payment_signal_users（零值可省略，按0理解）、warnings 和资格截止。原始近期意愿与范围内付款人数分开；有效付款仍需要未满足任务关联、原文引用及近一年日期。新付款回复不刷新旧痛点的新鲜度。

命令拒绝截断、重复键、缺字段、错误层级、未知字段、重复/遗漏证据 ID、未被原文支持的引用，以及候选/模型记录/目录/标记与包不一致。它不修改文件、不修补 JSON，不将机械门槛等同于业务立项；完整原文、身份、时间和反证仍必须由模型复核。请求中附完整 schema，并只给单候选必要上下文；JSON mode 和完成标记不替代本地校验。

API 未完成/拒绝/空内容直接失败；格式错误保留原始响应，反馈具体字段路径/约束，让模型每候选初审和反审累计至多额外两次重生成，仍失败则 paused_service。私有候选状态持久化累计次数和待补证项。每次真实调用独立记录用量。本版门禁由外部 Codex 提示词执行；网站 API 不要求这个私有文件，旧包/旧 outbox 仍按原字节查询与重试。若执行端不遵守提示词，网站不会替其作语义或人数判断。

## 空报告与停用的旧入口

无通过项目时，以 demand-batch.v1 提交 candidates=[] 的真实运行报告到 `/api/research/v1/batches`。仅当前 active 且有缺口的 agent_project 会话可提交新报告；submitted 保持活动，waiting_for_signals/paused_service 将会话置为对应状态，均与批次和用量同事务保存。报告立即 completed，不需要 worker。新非空旧候选包返回 410，已接收旧包优先按原字节返回原回执，冲突仍为 409。

旧会话与其回执一律 should_continue=false。网站不自动恢复 waiting_for_signals；外部 Codex 必须按授权显式恢复。后台旧任务创建/取消/恢复、批次重试、人工立项和保存模型设置的写 URL 均退役为 410（认证与 CSRF 仍适用）；旧设置书签跳转费用页。历史模型配置和算法保留作数据兼容及回归，不被网站运行时调用。

## 重试与复核

固定传输脚本增加 catalog 操作；stage/submit 自动根据 schema 选择固定路由。返回 202/completed 后仍查询回执核对目标状态。同 ID 相同原字节返回同接收回执，不产生第二个项目；同 ID 不同字节返回 409。目录 revision 变化必须重新整合判断，生成新包。

失效或备份恢复后的项目，模型重新阅读原文并判断，通过后带 `project.replaces={need_id,version}` 提交。归属以最初的创建/导入批次确认，重复需求 alias 不授予修改权限；目录、资料版本必须一致。只允许尚未开工（含已确认设计）、失效、失败或未立项项目；正在开发、审查或已上线项目不能被重置。复核保留需求 ID，新增证据历史，回到待开发设计，不恢复旧发布状态。目标按同一任务的不同已导入 need_id 累计，不因开始开发扣减，复核不重复计数；过期不会自动触发新搜寻。

原包 30 天、研究摘要 180 天清理继续有效；长期哈希/回执用于幂等。超过研究保留期的历史项目需管理员检查归属资料后再处理，不从丢失工件推断批准。

## Codex 定时入口

按原计划每天北京时间 08:10，但当前配置 enabled=false、全部 activation_checks=false，调度入口保持 PAUSED。新提示词为 `prompts/project-research-task.v1.md`，只在显式完成绑定、工具可用性、串行执行、跨运行恢复和真实往返验收后启用。创建暂停入口不授权费用或网络规则变更。

Codex 支持将项目、提示词、周期和执行环境绑定成定时任务；这适合已经稳定的重复流程。[OpenAI 官方文档](https://learn.chatgpt.com/guides/best-practices#use-scheduled-tasks-for-repeated-work)。本次不依赖独立 Go 搜寻程序，网站后台 worker 仅执行保留期限清理与备份，不再领取旧候选队列。
