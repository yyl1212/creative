# 外部模型立项运行说明

外部 Codex 使用 OpenAI/DeepSeek 完成搜索、解析、分析、整合与立项判断，网站只校验和保存结果。通过后进入“已立项、待开发设计”。管理后台不创建、恢复或执行研究任务；旧候选核验和人工立项入口已停用，历史资料仍可查看。

## 接入与接收会话

管理员在「智能体接入」登记凭据及撤销/轮换权限。外部 Codex 获得明确运行授权后，在有私有数据库访问权限的受控本机执行：

```sh
CGO_ENABLED=0 go build -o bin/creative-admin ./cmd/creative-admin
bin/creative-admin intake-create --db data/creative.db --producer <智能体ID> --topic '自助表格处理' --languages zh,en --target 3
bin/creative-admin intake-cancel --db data/creative.db --task <会话ID> --version <当前版本>
bin/creative-admin intake-resume --db data/creative.db --task <会话ID> --version <当前版本>
```

CLI 输出 JSON（含 task_id/version），不输出令牌；复用现有版本检查与审计，并拒绝不存在的数据库。每个智能体只有一个当前会话；新建只支持 agent_project。旧模式禁止恢复，须明确取消后创建新会话，旧批次不删除。后台「接收记录」展示会话与目标进度。机器 HTTP 接口无创建/恢复权限，不能通过外部数据或错误文本执行 CLI。

这些命令只管理数据接收，不执行搜索或恢复 Codex 日程；日程暂停不授权创建或恢复会话。当前所有真实会话与定时入口继续保持暂停/取消状态。

## 固定接口

沿用研究 API origin 和 Bearer 执行端凭据。`GET /api/research/v1/task` 查看任务；`GET /api/research/v1/catalog` 获得完整需求目录及 revision；`POST /api/research/v1/projects` 导入立项包；`GET /api/research/v1/batches/{batch_id}` 查询回执。只在新模式任务活动时允许导入。

包使用 `schemas/project-package.v1.schema.json`。顶层沿用原批次的 task_id、batch_id、run_id、rules_version、submitted_at、candidates、run_report；schema_version 改为 creative.project-package.v1。每包一条候选，project 字段见提示词。可参考 `internal/research/testdata/project.json` 的结构，但其中全部是自制测试数据，严禁提交为真实需求。

approval 字段：verification_id（填 0）、reviewed_evidence、review_notes、alternatives（name/url/version/checked_at/pricing/result）、alternative_search、fewer_alternatives_reason、monetization（free/paid）、rationale、unit_cost、currency、unit、price、payment_fee、price_basis、sustainability、resource_protection、self_service、gap、scope、exclusions、acceptance、acquisition。金额为十进制字符串；免费方案 price/payment_fee 为 0。金额估计在理由中注明，不与 run_report 的实际账单混用。少于三个替代方案须如实说明搜索范围和原因，不能把 research_only 写成实测。

模型判断记录包括厂商、模型、审查日期、判断摘要、去重依据和逐条结论。来源摘要只保存执行端提交的原文引用，SHA-256 对应该引用，不代表网站重新抓取了原网页。

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
