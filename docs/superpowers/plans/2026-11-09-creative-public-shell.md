# Creative 用户空站实施计划

> **给开发执行者：** 按任务实施时使用 `superpowers:subagent-driven-development` 或 `superpowers:executing-plans`。以下复选框仅在实际完成后勾选。本文件是计划，不表示已经开始开发。

**目标：** 交付可本地构建与预览的中英双语用户空站，产品清单为空，没有虚构工具、收入或完成需求。

**架构：** Astro 生成静态页面，TypeScript 维护文案和有限公开配置。公开构建不连接私有研究数据库或模型接口；Go 管理后台和搜寻接收属于后续独立子系统计划。先交付本子任务，再完成主方案阶段 A 的剩余能力，不能据此宣布整个阶段 A 完成。

**技术栈：** Astro、TypeScript、Node.js 24；实施时核对官方支持条件并锁定兼容依赖。浏览器检查采用届时环境内可用的工具，不为静态页面提前引入大型测试平台。

**规格：** [Creative 最终方案](../specs/2026-11-08-creative-platform-design.md)，重点为第 1、4、7、10、11、13 节。搜寻 v1 不在本子任务中实现。

**敏捷归属：** 本计划作为 [敏捷开发计划](2026-11-09-creative-agile-delivery.md) 的迭代 1 用户页面子任务执行；与管理员登录和真实空状态共同演示。外部搜寻按新方案使用定时提示词任务，不开发独立 Go 搜寻程序。

## 全局约束

- “网站初始没有完成任何需求”；“当前只接入 DeepSeek 和 OpenAI 的模型”。本子任务没有模型调用。
- “不设置预算边界”；“不设置固定搜索次数限制”。本子任务没有搜寻、消费或采购行为。
- “初始空站不加载广告脚本、不收款、不预售、不展示购买按钮。”
- 产品数据初始为 `[]`；测试示例隔离，不得进入公开目录。
- 文档用中文；页面提供 `zh/en`；使用自制文字和简单 CSS，不下载素材。
- 开发前重新核验远程状态，拉取最新 master 后新建分支。远程仍为空时先落实主线初始化；未落实前不以文档分支冒充已更新主线。
- 每次测试批次不超过 10 分钟，执行器设置外层截止时间。以后 Go 调试及常规测试使用 `CGO_ENABLED=0`。
- 完成代码后独立代码审查、回归，通过 Git 创建 MR/PR；发布按对应授权执行。本计划不包含生产部署。

## 审查重点

1. 用户从不存在的工具链接进入：必须返回 404，不能回退成看似已上线的首页。归任务 1。
2. 访问未知语言或页面参数：只生成明确的 `zh/en` 页面，不能批量生成内容或泄露私有路由。归任务 1。
3. 构建环境存在私有密钥：只能显式读取允许公开的配置，不导出整个环境。用自制哨兵值验证产物。归任务 2。
4. 正式域名、后台或反馈入口尚未配置：本地可预览，不能显示虚构可用服务；正式发布前补齐真实配置。归任务 2。
5. 搜索引擎遇到空目录与错误页：不得把占位页列为可索引产品；中英文对应关系准确。归任务 2。

## 文件与职责

| 新增或修改 | 职责 |
| --- | --- |
| `package.json`、`package-lock.json`、`astro.config.mjs`、`tsconfig.json` | 构建与检查命令、锁定依赖、站点源地址 |
| `src/layouts/Base.astro`、`src/lib/i18n.ts` | 页面骨架、语言和文案，不增加客户端框架 |
| `src/pages/index.astro`、`src/pages/404.astro` | 语言入口、真实错误页 |
| `src/pages/[lang]/index.astro`、`src/pages/[lang]/tools/index.astro` | 两种语言首页与空目录 |
| `src/pages/[lang]/[page].astro` | 仅 `about/privacy/feedback` 三种说明页 |
| `src/data/products.json` | 公开产品数据，初始内容严格为 `[]` |
| `src/config/public.ts`、`src/pages/sitemap.xml.ts` | 允许公开的配置与仅含可索引介绍页的 sitemap |
| `tests/public-shell.test.mjs` | 对实际构建产物检查公开边界与路由，不存页面整页快照 |
| `README.md`、`.env.example`、`.gitignore` | 中文运行说明、配置占位、依赖/产物忽略规则；保留既有秘密忽略规则 |

## 任务 1：可构建的双语空站

**文件：** 上表的构建文件、页面、布局、文案、产品清单、测试文件和运行说明；任务 2 再补充 SEO 与公开配置。

**输入接口：** `src/data/products.json` 是唯一产品清单，本阶段为 `[]`。不读取数据库、外部 API 或私有报告。

**输出接口：**
- `src/lib/i18n.ts` 导出 `locales = ['zh', 'en'] as const` 及 `Locale` 类型；文案按该类型索引。
- 语言路由的 `getStaticPaths()` 只返回这两种语言；`[page].astro` 只返回两种语言与 `about/privacy/feedback` 的六种组合。
- 构建产物为 `dist/`；公开路径为 `/`、`/zh/`、`/en/`、各语言 `/tools/` 和上述三种说明页；没有任何 `/tools/<slug>/` 产品页。
- 根页让用户选择语言，不按 IP 强制跳转；每个页面设置正确 HTML `lang`。

- [ ] **步骤 1：完成基线与分支准备。** 检查工作区、远程 master 和 AGENTS 指令；在隔离工作区以最新 master 创建 `feat/public-shell`，记录基线提交。若须初始化主线，先完成该步骤并核验，再继续。
- [ ] **步骤 2：加入最小构建配置和失败检查。** 新建 Node 测试 `emptyCatalogAndRoutes`：断言产品数组严格等于 `[]`、两种语言页面存在、中文空目录包含“暂无已上线工具”、英文包含“No tools are available yet.”，产物不存在工具详情、`admin` 或 `api` 目录；不生成未知语言或未知说明页。测试只检查约定边界，不断言 CSS 或完整 HTML。
- [ ] **步骤 3：确认检查会失败。** 首次安装并锁定受支持的 Astro 依赖后，运行 `npm run build` 与 `node --test tests/public-shell.test.mjs`；记录当前页面尚未实现导致的预期失败，不把下载错误当测试失败证据。
- [ ] **步骤 4：实现最小页面。** 使用上述接口生成页面；首页只说明项目用途，工具目录只显示空状态，关于页描述真实阶段。隐私页列出当前实际数据行为，反馈未配置时明确说明且不收集输入。创建正常 404 页面。增加清楚的导航、键盘焦点、语义标签和移动端布局；不引入图像、图标包、复杂组件库或客户端状态库。
- [ ] **步骤 5：构建与验证。** 在每批不超过 10 分钟的执行器中分别运行 `npm run build`、`node --test tests/public-shell.test.mjs`，预期退出码 0。启动本地预览，验证 `/zh/tools/nonexistent/`、`/fr/`、`/zh/unknown/` 返回 HTTP 404；用真实浏览器检查中英文切换、窄屏和键盘导航，记录实际结果。
- [ ] **步骤 6：审查并提交。** 独立审查代码和空状态边界，修复阻塞问题并重跑受影响检查；只暂存本任务文件，提交 `feat: 增加双语用户空站`。

## 任务 2：公开配置、索引规则与交付

**文件：** 新建 `src/config/public.ts`、`src/pages/sitemap.xml.ts`；修改配置、布局、页面、测试、README 和 `.env.example`。

**输入接口：** 任务 1 的 `Locale`、静态路径及空产品数组。仅允许配置 `SITE_URL`、`ADMIN_URL`、`FEEDBACK_EMAIL`；服务器模型密钥与数据库路径不是公开配置。

**输出接口：** `src/config/public.ts` 导出 `PublicConfig = { siteURL: string; adminURL: string | null; feedbackEmail: string | null }` 和 `readPublicConfig(env: { SITE_URL?: string; ADMIN_URL?: string; FEEDBACK_EMAIL?: string }): PublicConfig`。只传入并读取这三个字段；本地未提供 SITE_URL 时使用 `http://localhost:4321`，管理和反馈值为空时不生成外链。配置的非本地站点/后台必须使用不含凭据的 HTTPS URL；无效值使构建报出字段名，不输出原始秘密或整个环境。

- [ ] **步骤 1：增加失败检查。** 新增 `privateConfigDoesNotLeak`：在子构建进程注入自制 `DEEPSEEK_API_KEY=creative-test-sentinel-only` 和 `OPENAI_API_KEY=creative-openai-sentinel-only`，递归检查实际 `dist` 文本不含这两个哨兵及变量名，不读取真实 `.env.local`。新增 `emptyPagesAreNotIndexed`：空目录和错误页含 noindex、sitemap 无空目录/产品/管理/API 路径、每个介绍页只有一个对应自身的 canonical，中英配对 hreflang 相互对应；未配置后台/反馈时没有假链接。
- [ ] **步骤 2：运行新增检查确认失败。** 运行构建和指定 Node 测试，预期因索引和配置行为尚未实现而失败；检查失败原因确实对应待实现边界。
- [ ] **步骤 3：实现配置与索引。** 按输出接口实现显式配置，根语言入口作为 x-default。介绍页使用配置源地址生成 canonical、双向及自引用 hreflang；空目录和 404 设置 noindex，不进入 sitemap。配置了真实反馈邮箱后才展示可用异步联系方式；不增加表单后台、发送邮件、广告或统计脚本。用自制测试值验证错误配置会被拒绝。
- [ ] **步骤 4：回归与交付审查。** 在独立批次运行 `npm ci`、`npm run build`、`node --test tests/public-shell.test.mjs`，预期退出码 0；浏览器再次验证两种语言、404、空目录、配置有/无两条路径。审查产物无私有配置、外部请求和虚构内容，真实公开发布前必须补齐域名、后台入口、反馈及实际托管隐私说明。
- [ ] **步骤 5：提交并创建 MR/PR。** 提交 `feat: 完善空站公开配置与索引规则`，记录最终待合并提交与测试结果。用 Git 推送开发分支并创建到 master 的 MR/PR，说明这是阶段 A 的用户空站子任务，后台尚未交付；未经对应授权不部署生产。

## 后续计划与覆盖自查

本计划覆盖主方案中用户空站的页面、语言、真实零产品状态、索引边界、公开配置和交付流程。五项审查重点分别有路由/浏览器检查或产物检查；任务 2 复用任务 1 的语言与路由接口，不重复实现产品或认证系统。

后续工作按敏捷总计划排序并滚动细化，包括 Go 管理与持久化、搜寻 v1 接收和核验、定时提示词任务接入以及开发交接。完成主方案 A/B 的所有验收后才可宣布平台具备相应能力，不能只完成空页面就宣布需求平台可运行。真实业务工具、支付、广告和产品统计分别由合格需求及对应阶段的方案驱动，不在本子任务中预建。
