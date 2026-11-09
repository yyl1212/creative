# 外部大模型立项实施计划

Spec：../specs/2026-11-09-agent-project-intake.md。用户已确认直接已立项入库，采用 executing-plans 持续执行。

## 全局约束

起点 master `23bb7f3`，当前隔离分支已拉取最新。站内原文解析方案被用户的新流程替代。只支持 OpenAI/DeepSeek，保持定时配置 disabled，不修改真实研究记录或凭据。Go CGO_ENABLED=0，测试外层 570 秒、Go 9 分钟。中文文档，固定目的地址，完整回归及审查后 Git MR。

## Task 1: 立项包与事务导入

范围：schema、迁移、project_import、tasks/retention/approval/qualification。
接口：CreateProjectTask、ProjectCatalog、ImportProject；新模式以 rules.decision_mode=agent_project 区分，旧规则号保留。复用候选 schema、validApproval 和现有表，新增来源表。先测试 RED：模型关闭情况下仍能原子导入已立项状态；重复包无重复；旧任务拒收；目录冲突/精确重复/撤销/取消/错误证据关联均拒绝；新目标计数不扣掉已立项项目；下游任务包可导出且旧门槛测试不变。实现后 source 不改，全量 research/store 测试 GREEN。

## Task 2: API、传输、提示词与暂停任务

范围：HTTP research 入口、任务创建表单/来源展示、固定传输脚本、config 和新提示词，中文操作文档。
接口：GET catalog、POST projects、仍使用 GET batches/{id} 查询回执。传输按冻结 schema 选择固定路由，不接受模型提供的 URL；新增 catalog 操作。先写 endpoint 与传输测试 RED，再接线 GREEN。提示词覆盖搜索到立项全部步骤、原文与反证、目录去重、暂停/恢复、目标数量、费用、无新信号退出，只有 approved 才提交立项包。Codex 调度工具创建 PAUSED 任务；不扩大权限，不触发真实搜索。

## Task 3: 兼容性、回归、独立审查与 MR

审查新入口与旧入口状态隔离、并发导入目录检查、目标计数、证据有效期、回滚、保留期与备份恢复。Go test/build/vet、前端 build/test、Python 传输测试；每次不超过 10 分钟。执行 fresh-context 全分支审查，修复重要问题并补 RED→GREEN，记录验证。通过 Git 提交推送并创建 MR，检查 CI；不自动合并。

Review Focus：旧 token/任务是否被静默提升权限；任一路径是否重新覆盖已立项资料；导入是否误进站内核验；模型判定与技术校验是否混淆；所有来源是否在有效期/恢复流程下保持同一状态；旧测试是否因兼容被绕开；提示词所用 API/传输是否真实存在。
