# 模型 JSON 与证据计数修复计划

> 执行方式：superpowers:executing-plans，当前会话实施，完成后独立审查。

**目标：** 固定单候选模型契约，阻断错误 JSON/漂移字段，按证据集合计数并核对交付包。

**架构：** 外部模型理解、判断；现有 Go CLI 离线验证；现有传输与网站接口不变。

**技术：** Go 标准库、已有 jsonschema/v6、JSON Schema；无新依赖。

**规格：** `docs/superpowers/specs/2026-11-10-research-review-contract.md`

## 约束与审查重点

- `CGO_ENABLED=0`；每次测试不超过 9 分钟；文档中文。
- 原始失败响应只读私有回放，不提交真实个人材料或密钥；测试引用自制。
- 审查重复作者、跨站别名、同站子域、重复引用、引用缺失和顺序变化。
- 审查无金额实际消费、条件式意愿、已满足/无关需求、旧时间及未知元数据。
- 审查包候选或元数据改变后不能沿用审查；JSON 重复键、缺字段、截断、未知字段拒绝。
- 无网站语义判定、迁移或兼容性变更；定时配置不修改。

## 任务 1：契约、计数与 CLI

新增 schema、`internal/research/agent_review.go`/测试、`cmd/creative-admin/review.go`/测试；修改 CLI 分派。

接口：`CheckAgentReview(raw, project []byte, now time.Time) (ReviewCheck, error)`；命令 `review-check --file <审查JSON> [--project <立项包JSON>]`，无网络或数据库。

- [x] 编写失败测试：严格 JSON、独立集合计数、付费/消费区分、时间/身份、包绑定和 CLI 无数据库副作用。
- [x] 执行测试看到失败，再实现 schema、机械检查和 CLI。
- [x] 运行 `CGO_ENABLED=0 go test ./internal/research ./cmd/creative-admin -count=1 -timeout 9m` 通过。

## 任务 2：接入与回归交付

修改 `prompts/project-research-task.v1.md`、`docs/agent-project-operations.md`、README；新增验证记录。

- [x] 提示词固定单候选 JSON、字段/语义定义、原文追溯、错误重生成和提交前强制检查。
- [x] 私有失败 JSON 离线回放，确认拒绝，不修改原文件。
- [x] Go 全量测试、vet、CLI 构建及既有 Python 传输回归，单次不超过 9 分钟。
- [x] 独立代码审查，修复阻塞项并回归。交付步骤：创建 MR，不自动合并。
- [x] 对照配置和自动化状态证明定时搜寻仍暂停；记录未做真实 API 回访的限制。

## 执行记录

任务 1：先运行新测试，因 CheckAgentReview/reviewCheck 缺失失败；实现后研究与 CLI 全套通过。包绑定测试还捕获了未初始化基础 schema 的问题，复用已编译 schema 后通过。

任务 2：两份真实原始失败输出离线回放均拒绝、原件不变；全量 Go / vet / build、Python 15 项、公开页 5 项通过。独立审查补出分离回复与转义凭据问题，新增用例 RED→GREEN 后再跑全量 Go。

Final: fixed 分离回复付款漏计 — TestAgentReviewSeparatedFollowup 正反序失败后修复，跨人隔离通过。

Final: fixed 转义凭据绕过 — TestAgentReviewRejectsEscapedCredentials 失败后修复，沿用现有解析后凭据检查。

Final: Ruling: 提示词字段路径视为功能问题并修复 — 错误路径可能诱发 schema 漂移 — 若忽略会增加无效模型输出。

Final: Ruling: 原文真实性、跨站身份与业务语义仍由模型判断 — 符合外部智能体职责 — 集合计数无法证明这些事实，下一轮真实搜寻仍须反向复核。
