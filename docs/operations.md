# 迭代 5 运维与开发交接

本轮交付立项、开发记录、发布核验、日报、非敏感模型设置、对账和备份恢复基础能力。真实产品清单仍为 `[]`；定时搜寻、机器接收和两家模型默认禁用。没有部署或收入验收，不把平台代码视为已完成用户需求。

## 管理员操作

1. 在需求池打开**当前核验版本**，逐条复核计入资格的原文，实测至少 3 个替代方案；不足时记录搜索范围、全部找到的方案及原因。填写缺口、最小范围、排除项、验收、自助流程和获客依据。
2. 选择免费或付费：免费按广告收入 0 测算，明确单位成本、资源保护和可持续依据；付费补价格、支付费及对应付费原文，页面计算单位毛利。不使用预算阈值代替需求判断。
3. 批准后先导出**需求包**，交给设计智能体。完成规格、架构图、文件影响、逐任务计划和可行性/兼容性审查后，在开发记录确认仓库内文档路径、文件范围、环境变量名称、master 基线、新分支、产品 slug 及生产 HTTPS origin，再导出**开发包**。
4. 开发者在隔离工作区拉取最新 master、新建分支。基线或方案改变须回后台重新确认设计；网站不执行任务包中的代码或网页指令。开始工作前登记开工，记录 MR/PR、提交、审查和测试报告；新提交必须重新绑定审查与回归。
5. 开发登记不代表上线。只有下面的受控命令成功核验生产后才增加完成数量；表单和机器令牌都没有直接写上线的接口。

证据过期、补证或旧版本会导致 409；刷新并重新审核。资料保留仍为原始材料 30 天、核验摘要 180 天，立项记录不阻止清理。需求包按白名单导出且拒绝常见凭据格式，但不保证识别任意自定义秘密；任何表单都不要填密码、令牌、环境变量值或完整账单文件。

## 日报、费用与模型

- `/admin/reports` 按上海自然日统计接收候选、核验、立项、上线和失败；日期可选择。费用归属消费发生日，后来核账回填原日期；日报不是收入报表。
- `/admin/usage` 分开显示原自报与实际核账金额。输入账单行唯一标识、实际金额/币种和核账备注后保存；同厂商同账单行不能重复核账，已核账行不可被外部补报覆盖。跨币种不折算，未知金额不算零。
- `/admin/settings` 只保存 OpenAI/DeepSeek 主备、模型名、启用及人工验证声明。页面区分保存值与运行值，保存后重启才生效；有数据库设置时优先于 `config/providers.json`。勾选验证只是操作者声明，不会替你调用真实模型验证。
- 凭据仅放私有进程环境；本程序不自动读取 `.env`。执行端令牌继续在原执行端页面创建、轮换、撤销；不在配置页保存密钥。
- 备份状态显示最近一次维护结果。失败时检查磁盘空间、私有目录权限和文件系统，修复后执行一次手动备份。应用启动时和每日上海 08:00 后维护一次，不调度外部搜寻。

```sh
CGO_ENABLED=0 go build -o bin/creative ./cmd/creative
CGO_ENABLED=0 go build -o bin/creative-admin ./cmd/creative-admin
bin/creative-admin report --db /private/creative/creative.db --date 2026-11-09
```

命令路径为示意，换成真实私有绝对路径；CLI 的 report 不传日期时使用上海今天。运维命令不自动创建空数据库，首次初始化明确执行 migrate。

## 发布核验

仅用于首个真实获批产品完成之后。发布仍需单独授权；以下命令不自动部署，也不向 GitHub 推送。先通过常规审查、回归并提交全部设计/验收文件，在已登记的干净 Git 分支构建站点。Git 的 HEAD、审查提交、测试提交、批准基线和仓库必须一致。保留此构建直到生产核验结束，不可边验证边改文件。

产品清单协议为只含 `need_id` 和 `slug` 的非空 JSON 数组，例如 `[ { "need_id": 7, "slug": "example" } ]`。实际目录必须有 `/en/tools/example/index.html` 与 `/zh/tools/example/index.html`。这是未来获批产品的发布协议，示例不能导入生产；当前空站会拒绝生成产品上线记录。

```sh
# 在批准的工作区完成实际构建后执行；17 为页面显示的开发记录版本示意。
bin/creative-admin release-prepare --db /private/creative/creative.db --need 7 --version 17 --repo . --dist dist --catalog src/data/products.json
# 人工完成授权的原子部署（包括 dist/.well-known/creative-release.json）之后：
bin/creative-admin release-verify --db /private/creative/creative.db --need 7 --version 17 --repo . --dist dist --catalog src/data/products.json
# 生产已恢复上一版本后，登记撤销此次完成状态；使用最新记录版本。
bin/creative-admin release-rollback --db /private/creative/creative.db --need 7 --version 18 --reason '已恢复上一生产版本，正在修复'
```

核验比较完整本地文件集合、SHA-256、产品目录及生产标记，每个公开文件均检查远端摘要；生产只允许公网 HTTPS，无代理/重定向，不向私人地址发请求。单文件最多 32 MiB、最多 10000 文件、标记最多 4 MiB、网络核验总计 2 分钟。这些是产物防护，不是搜寻次数/预算边界。静态类型白名单可能拒绝新的素材格式，扩展前须审查。预览不能标为上线；重复成功核验幂等，失败或回滚撤销完成数量。核验失败时刷新后台查看新版本再重试。

CLI 核验 Git 身份与文件存在，并不替代 CI 构建来源证明或实测业务功能。操作者必须保证产物来自该提交的已审查构建；仅修改 manifest 无法通过摘要核验，但拥有运维权限的人仍须承担发布审查职责。后续修改生产内容后应重新核验，离线记录不是持续在线监控。

## 一致性备份与恢复

维护备份位于数据库相邻的 `backups/` 私有目录。使用 SQLite `VACUUM INTO` 获取包含 WAL 的一致快照，私有临时文件、校验与原子发布，仅保留本工具命名的最近 7 份，不删除其它文件。七份指快照数，启动重备份也占一份。备份含账户哈希、审计和研究资料，不得放到 dist 或任何文件服务根目录；应另行做异地保管。

```sh
bin/creative-admin backup --db /private/creative/creative.db --directory /private/creative/backups
# 停服务后选择一份完整快照，恢复到不存在的新路径；不能覆盖当前库及其 WAL/SHM/journal。
bin/creative-admin restore --source /private/creative/backups/creative-backup-示意.db --destination /private/creative/creative-restored.db
bin/creative-admin reset-password --db /private/creative/creative-restored.db --username owner
```

恢复不会修改原快照，损坏或未知迁移版本会拒绝。恢复库会清空会话、禁用管理员、撤销机器令牌、暂停非取消任务、关闭两家模型、将发布/开发标为待重新核验，并撤销完成计数。重置管理员密码后，人工将 `CREATIVE_DB` 指向恢复库，再启动服务。保持定时任务禁用；确有授权后才轮换执行端令牌并逐项恢复服务。

恢复后不能直接用旧上线记录恢复完成数量。重新验证原文取得有效资格、重新批准设计并核对当前生产，再用受控发布命令确认。备份并不备份静态产物或私有进程环境，这些要单独保管。恢复结果不冒充对真实生产做过核验。

004 为追加迁移，不修改 001–003。升级前先使用当前版本一致备份；新代码自动应用 004，旧程序会拒绝新版库。代码回退应停服务，用旧代码和升级前备份，在隔离路径复核后切换，保全升级后的新增数据；不能用删除数据库或随意降级 SQL 回退。

## 三入口 HTTPS 部署模板

`deploy/Caddyfile` 供未来授权部署使用，当前未启动 Caddy、未申请证书或修改 DNS。主机需先安装并验证 Caddy，将三项 host 设为不同的实际域名：

```sh
export CREATIVE_PUBLIC_HOST=creative.example.com
export CREATIVE_ADMIN_HOST=admin.example.com
export CREATIVE_API_HOST=research.example.com
caddy validate --config deploy/Caddyfile --adapter caddyfile
```

只把已核验静态构建放 `/srv/creative/public`，后台仅监听 `127.0.0.1:8080`。后台进程使用 `CREATIVE_ADMIN_ORIGIN=https://admin.example.com`、`CREATIVE_TRUSTED_PROXY=127.0.0.1`；`CREATIVE_RESEARCH_ORIGIN` 保持空。代理仅转发管理路径，机器域名固定返回 503，公众域名没有数据库或管理路由。

Caddy 直面公网时用连接来源覆盖 `X-Real-IP`，不接受浏览器自报来源；不随意添加更上游可信代理。反向代理默认保留 HTTP 上游 Host 并处理 X-Forwarded-*，参见 [Caddy 官方反向代理文档](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#headers)。不要将本模板直接套到 CDN 多层代理上；需按真实代理链重新审核来源信任与 HTTPS cookie。证书、三域名隔离、持久卷、原子发布和故障回滚要在首次真实部署单独验收。
