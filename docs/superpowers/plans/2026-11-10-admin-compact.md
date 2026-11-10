# 管理后台五项导航实施计划

> **For agentic workers:** 使用 superpowers:executing-plans 在当前会话完成，最后依 requesting-code-review 做新鲜上下文代码审查。

**Goal:** 主导航精简为五项，合并重复页面并清理旧流程文案，保留历史数据和必要操作。

**Architecture:** 复用总览及导入批次 handler，分别嵌入已有日报和接收会话；旧 GET 地址作为兼容入口。智能体权限放页头，技术表单折叠。

**Tech Stack:** Go、现有 HTML 模板和 CSS，无新依赖。

**Spec:** `docs/superpowers/specs/2026-11-10-admin-compact-design.md`

## 全局约束与审查重点

- 最新 master ce489f8，依赖 MR #8 / 0c32e08；当前隔离分支 codex/admin-compact，不自动合并远端 MR。
- CGO_ENABLED=0；单个测试外部截止 570 秒，Go timeout 9m。
- 仅 Mac 桌面范围；数据、接口、权限与保留期限不变；Codex PAUSED、enabled=false。
- 检查日期选择/非法日期、历史分页、旧地址认证、凭据入口与 CSRF、开发折叠表单可达；复用既有 fixture 和端到端 handler 测试。

## 任务 1：页面合并和文案

文件：`internal/httpserver/{admin,operations_admin,research_admin}.go`、`web/admin/templates/*.html`、`web/admin/static/admin.css` 与对应测试。
接口：复用 `DailyReport(date)`、`BackupStatus()`、`Tasks()`、`Batches(before)`，不改服务签名；`reports` 路由交 overview，`tasks` 路由交 batchesPage。

- [x] 新增测试验证五项导航及次级接入、总览日期日报、导入会话与分页、兼容旧地址和历史详情；观察旧实现失败。
- [x] 合并 handler 和模板，移除独立导航及冗余指标；清理所有当前页面的旧研究/人工立项指引，折叠技术表单，保留操作字段与状态保护。
- [x] 调整既有汇总显示断言；执行 HTTP 及开发管理回归，确认旧只读/退役边界仍有效。

## 任务 2：验证和交付

- [x] 更新当前文档与中文验收记录；保留历史验收文档。
- [x] 完整 Go test/build/vet、传输测试与公开站回归；独立审查并修复实质问题。
- [x] 更新本机服务，沿用原数据库和账号；检查五项导航、旧地址、日期、分页、必要操作和数据摘要不变。尝试桌面视觉检查；若工具不可用明确记录。
- [ ] Git 提交并创建依赖 MR，验证远端 CI；不合并，不启用接收接口或日程。

- 审查修订：日报漏计外部立项，增加 internal/research/operations.go 事件计数修复及 project_import_test.go、operations_test.go 回归；先观察真实导入后的 Approved=0 失败，再修为兼容旧/新事件，重复原包不重复计数。

验证结果：全量回归通过，独立审查唯一日报漏计问题已修复并复核。本机五项导航、旧链接/日期/分页和必要操作通过，11 张业务表摘要及凭据不变；浏览器工具超时未完成视觉实测。见 docs/admin-compact-validation.md。
