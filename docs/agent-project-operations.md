# 外部模型立项运行说明

新流程由外部 OpenAI/DeepSeek 智能体完成搜索到立项，网站只校验和保存立项包。管理员创建任务时选择“外部模型立项后导入”，通过后直接进入开发工作区的“已立项”状态。旧任务保留网站核验方式，不会自动转换。

## 固定接口

沿用研究 API origin 和 Bearer 执行端凭据。`GET /api/research/v1/task` 查看任务；`GET /api/research/v1/catalog` 获得完整需求目录及 revision；`POST /api/research/v1/projects` 导入立项包；`GET /api/research/v1/batches/{batch_id}` 查询回执。只在新模式任务活动时允许导入。

包使用 `schemas/project-package.v1.schema.json`。顶层沿用原批次的 task_id、batch_id、run_id、rules_version、submitted_at、candidates、run_report；schema_version 改为 creative.project-package.v1。每包一条候选，project 字段见提示词。可参考 `internal/research/testdata/project.json` 的结构，但其中全部是自制测试数据，严禁提交为真实需求。

approval 字段：verification_id（填 0）、reviewed_evidence、review_notes、alternatives（name/url/version/checked_at/pricing/result）、alternative_search、fewer_alternatives_reason、monetization（free/paid）、rationale、unit_cost、currency、unit、price、payment_fee、price_basis、sustainability、resource_protection、self_service、gap、scope、exclusions、acceptance、acquisition。金额为十进制字符串；免费方案 price/payment_fee 为 0。金额估计在理由中注明，不与 run_report 的实际账单混用。少于三个替代方案须如实说明搜索范围和原因，不能把 research_only 写成实测。

模型判断记录包括厂商、模型、审查日期、判断摘要、去重依据和逐条结论。来源摘要只保存执行端提交的原文引用，SHA-256 对应该引用，不代表网站重新抓取了原网页。

## 重试与复核

固定传输脚本增加 catalog 操作；stage/submit 自动根据 schema 选择固定路由。返回 202/completed 后仍查询回执核对目标状态。同 ID 相同原字节返回同接收回执，不产生第二个项目；同 ID 不同字节返回 409。目录 revision 变化必须重新整合判断，生成新包。

失效或备份恢复后的项目，模型重新阅读原文并判断，通过后带 `project.replaces={need_id,version}` 提交。需归属当前执行端，且目录、资料版本一致。只允许待设计、失效、失败或未立项项目；正在开发、审查或已上线项目不能被重置。复核保留需求 ID，新增证据历史，回到待开发设计，不恢复旧发布状态。目标按同一任务的不同已导入 need_id 累计，不因开始开发扣减，复核不重复计数；过期不会自动触发新搜寻。

原包 30 天、研究摘要 180 天清理继续有效；长期哈希/回执用于幂等。超过研究保留期的历史项目需管理员检查归属资料后再处理，不从丢失工件推断批准。

## Codex 定时入口

按原计划每天北京时间 08:10，但当前配置 enabled=false、全部 activation_checks=false，调度入口保持 PAUSED。新提示词为 `prompts/project-research-task.v1.md`，只在显式完成绑定、工具可用性、串行执行、跨运行恢复和真实往返验收后启用。创建暂停入口不授权费用或网络规则变更。

Codex 支持将项目、提示词、周期和执行环境绑定成定时任务；这适合已经稳定的重复流程。[OpenAI 官方文档](https://learn.chatgpt.com/guides/best-practices#use-scheduled-tasks-for-repeated-work)。本次不依赖独立 Go 搜寻程序，网站后台 worker 只继续兼容旧候选核验任务。
