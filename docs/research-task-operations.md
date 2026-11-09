> 新建任务推荐使用外部模型立项流程，见 [运行说明](agent-project-operations.md)。本文描述保留兼容的旧候选核验流程；定时执行继续禁用。

# 定时提示词任务运行手册

当前交付固定提示词、非敏感配置、纯传输工具和隔离验收材料；真实任务默认禁用。是否达到 M3，以 [迭代 4 验收记录](iteration-04-validation.md) 为准；[迭代 0 网络试验](iteration-00-scheduler-probe.md) 保留历史依据。

## 组件与权限

平台负责串行、唤起与执行窗口，提示词负责搜寻和整理，Go 网站负责规则、资格和计数。`scripts/research_transport.py` 只有标准库 HTTP 与文件传输，没有搜索循环、模型调用、调度器或数据库。需要 Python 3.9+（本机验证 3.13），网站接口和 UI 不变。

[OpenAI 官方说明](https://learn.chatgpt.com/docs/automations?surface=app)明确：本地定时任务需要电脑开机、应用运行、项目存在；无人值守运行沿用默认沙箱，workspace-write 下网络可能失败。文档不能证明实际网络或串行能力。不要自动打开全局 Full Access。若需精确命令规则，应先审查脚本、固定版本与绑定路径，再由经营者配置；不能放行任意 Python 命令或绑定文件。

工具只向绑定的 origin 请求三个 v1 接口，禁止代理继承、重定向与任意路径，token 只有网站研究权限。工具限制不等于本地凭据对运行进程不可见；脚本及绑定目录须防其他用户修改。提示词不读取绑定内容，脚本不打印 token，不使用网页或回执中的 URL 发送凭据。

## 一次性配置

1. 管理页登记专用执行端并创建任务，默认目标 3，主题/语言由网站提供。私下保存一次展示的执行端 token；它不是管理员 cookie 或模型密钥。真实核验仍需另行配置已授权且验证成功的 OpenAI/DeepSeek 连接。
2. 在仓库之外选择持久的私有目录，权限 0700。Mac 使用物理绝对路径（如 `/private/...`，不使用 `/tmp` 符号链接）；不能使用每轮删除的临时工作树。固定审查后的脚本与提示词版本。若配置命令放行规则，安装副本必须位于智能体不可写的目录；不能对可被当前工作区修改的脚本给予沙箱外运行豁免。
3. 通过私有编辑器建立 0600 绑定文件，只有 origin/token/state_dir 三个字段。以下是假值，不能直接启用：

   ```json
   {"origin":"https://research.example.invalid","token":"REPLACE_PRIVATELY","state_dir":"/absolute/private/creative-research/outbox"}
   ```

   origin 无用户信息、路径、查询或片段，正式使用独立 HTTPS API 主机。仅本机测试允许 localhost/127.0.0.1/::1 的 HTTP。每个执行端独立 state_dir，轮换同一执行端凭据时保留目录。
4. 在**私有配置副本**填写 transport.script、prompt、schema 的固定版本绝对路径，以及 transport.binding_file、artifacts_dir 的绝对路径；先保持 enabled=false。仓库模板不保存私有路径或凭据，工件不提交 Git。
5. 手动试运行固定提示词并完成平台验收。六个 activation_checks 都有证据后，经营者才将私有配置 enabled=true 并创建正式日程。配置本身不是调度器；传输脚本不读取 enabled，启用检查由启动提示词执行，不能绕开它直接启用日程。

建议每日上海时间 08:10。真实日程的启动提示词按以下含义填写一次：

> 执行 Creative 定时需求搜寻。先读取 `<私有配置绝对路径>`，确认版本为 creative.research-task.v1、enabled=true 且所有 activation_checks=true，路径与部署记录一致。任一条件不满足则只报告未接入。读取配置绑定的固定版本 research-task.v1.md 和 demand-batch.v1.schema.json，严格按提示词恢复、搜寻、提交与查询；只通过绑定的 research_transport.py 和私有 binding_file 请求 Creative。跨轮工件固定在 artifacts_dir。不要修改配置、代码、权限或执行开发/发布。

当前调度模型选择 OpenAI `gpt-6.1-sol`，以目标账户实测可用性为准。提示词升级先暂停日程，保留旧 outbox，兼容性审查后恢复；不用“新版本”作为改写未确认原包的理由。

## 状态、恢复和费用

- 每轮先 task/outbox，查未确认回执，再决定搜索；每次新搜索和接收回执后重读任务。
- stage 原子、排他保存准确字节的 Base64 包与 SHA-256；同 ID 不同字节拒绝。完整业务 Schema 验证仍由网站负责，本地只校验运输所需字段与大小。
- submit 再检查 task_id、规则、active 和 should_continue；取消/达标/等待/故障/换任务后拒绝新 POST。检查后的竞态由 Go 网站在事务内裁决。
- 202 只代表接收；completed 才有逐项结果。取消后仍可查回执。字节冲突须排查，不能换 ID 绕过。
- API 不允许研究端修改任务。故障需管理员恢复，等待线索由网站维护检查恢复，取消不能自动恢复。
- 单次 socket I/O 超时 20 秒，脚本不自动重试；提示词最多额外重试 2 次，429 尊重 Retry-After，轮询至少间隔 5 秒。这不是搜索总次数、研究总时长或费用上限。
- 原文和原包保存 30 天，必要研究元数据 180 天。删除原包前持久化 batch_id/task_id/hash/最后回执/未确认原因；过期未确认包标为 expired_requires_review，只查回执，不能重造。`receipt BATCH_ID` 不依赖原始包。当前没有后台工件清理服务，由定时提示词按保留规则执行、经营者核查；网站清理由现有 Go 维护循环执行。
- 调用开始前分配稳定 usage_id；失败或中断费用保持 null，后续补账沿用 ID。平台运行费用与网站/外部 API 调用分开，未知不是 0。账单核对入口属于迭代 5。

轮换 token：暂停日程，在管理页轮换执行端凭据，私下更新原绑定，保持 origin/state_dir 不变，验证 task/既有 receipt 后恢复。撤销或 401 不能触发切换身份、清空 outbox 或重注册。无法提交的暂停报告先留本地，不声称网站收到。

## 平台验收

`tests/research-task/scenarios.json` 是输入与预期轨迹，**不是通过报告**。覆盖两次独立唤起、旧规则、响应丢失与原样重试、无新线索、取消不恢复、达标不搜、窗口续跑、恶意原文及平台重叠。

```sh
python3 -m unittest discover -s tests -p 'test_research_transport.py' -v
python3 tests/research-task/probe.py --root /private/tmp/creative-unique-probe --seconds 540
```

接收器每次使用全新目录，只监听回环，9 分钟内自动退出并删临时凭据；它接受空 candidates，首包接收后丢响应，随后提供 completed 回执。它不是完整 Go v1 核验器，只用于平台运输测试；本地回归覆盖接收器自检与清理。

临时日程第一轮 task→stage→submit，丢响应后保留包；下一轮从 outbox 查回执，不改字节或另造包。两轮须有不同平台运行记录。重叠测试让首轮有效请求后持续运行跨过下一调度点，核对平台跳过/排队；本地锁不算平台证据。网络失败则不伪造后续通过结论，记录错误并删除测试日程。

隔离关卡通过、公开原文工具可用、真实调用获授权后，再执行实际 Go API→公开原文→v1 JSON→回执。0 条合格、needs_evidence 或空报告都可以是诚实结果；不得导入虚构测试候选。记录真实回执与费用未知项后，才开启正式日程。

## 故障处置

| 现象 | 处理 |
| --- | --- |
| HTTP 沙箱拒绝/地址不通 | 保持禁用，落实执行环境和最小权限，不建立代理绕过 |
| outbox 损坏/符号链接/字节冲突 | 停止提交，查回执和私有备份，不覆盖原包 |
| 不支持的新规则 | 查旧包回执，保留材料，更新经审查的提示词 |
| 取消/达标 | 只查既有回执，不新搜索或提交 |
| 持续平台故障 | 本地保存暂停原因，服务恢复后遵循网站状态 |
| 无串行证据 | 不启用；更换平台需要重新验收 |

正式启用需要可用执行环境、访问权与调用授权，不需要增加预算或搜索次数上限。
