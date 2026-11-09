# Creative 现代极简改版实施计划

> **执行者：** 使用 superpowers:executing-plans 依次实施；复用已存在的隔离工作区。

**目标：** 按用户选择交付简洁务实的公开站与管理页面。
**架构：** 仅更新 Astro / Go 模板与 CSS，复用已验证的路由、配置、认证和真实数据。
**技术栈：** 已锁定的 Astro/TypeScript、Go 模板和 CSS，不新增依赖。
**设计：** [改版设计](../specs/2026-11-09-creative-minimal-redesign-design.md)。

## 约束与审查重点

- 初始产品 []、真实需求为空；无虚构工具/数据。只提供必要且已存在的入口。
- 文档中文、页面双语、自制 SVG；响应式、键盘焦点及对比度。
- 保留路由、SEO、公开配置与 CSRF；无兼容性或数据库改动。
- 检查窄屏导航/长英文文案、缺少反馈配置、未知页面 404、登录错误提示和注销字段。
- 每批测试外层 570 秒，Go 使用 CGO_ENABLED=0。

## Task 1: 用户页面

文件：src/styles/global.css、src/layouts/Base.astro、src/lib/i18n.ts、src/pages 中现有页面；新增 src/components/EmptyCatalog.astro。
接口：EmptyCatalog 接受 lang: Locale；Base 已有 Props 与公开配置保留。组件包含约定中英文空状态，不展示假搜索或分类功能。

- [x] 运行既有公开站测试，确认基线。
- [x] 实现中性视觉、紧凑导航、首页/目录空状态及说明/错误页面。
- [x] 实际构建后运行 tests/public-shell.test.mjs；视觉低风险改动不增加 CSS 快照或实现镜像测试。
- [ ] 检查真实预览、语言导航、窄屏与键盘，记录实测或阻塞。

## Task 2: 管理视图与交付

文件：web/admin/static/admin.css、templates/login.html、templates/index.html；新增 docs/minimal-redesign-validation.md。
接口：沿用现有模板 view 字段、表单 action/name 与 CSRF 字段；不修改 Go 业务逻辑。

- [x] 使用同一视觉语言重做登录、真实数量与服务状态展示。
- [x] 执行 CGO_ENABLED=0 go test ./internal/httpserver -count=1 -timeout 9m 及 go build ./cmd/creative，外层 570 秒。
- [x] 独立审查全部视图改动，修复实际缺陷并回归。
- [ ] 提交并创建以 feat/iteration-0-1 为目标的 PR，注明之前的迭代验收限制与本次结果。

自查：任务 1/2 无共享运行时接口，只共享视觉约定；不新增跨系统依赖。既有测试验证所有受影响功能边界，浏览器负责视觉与交互验收。未发现阻塞性设计问题。
