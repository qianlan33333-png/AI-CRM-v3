# 剩余页面 Chromium 验收 PRD

日期：2026-09-16  
状态：验收测试已实现；最终四页通过证据须在 Archive/Grid 修复合入后重跑。

## 业务判断与分类

现有 HTTP、PG 与 JSDOM 合同不足以证明最终浏览器页面。范围只补 `/admin/message-archive`、`/admin/message-archive/customers/{id}`、`/r/{public_code}`、`/c/{slug}` 和 `/shared/service-period-member-grid` 的真实 PostgreSQL + Chromium 证据。

OneID：会话存档只读取既有 canonical customer；不 Resolve、Provision、关联或合并。

Persistence：仅隔离 UTF-8 PostgreSQL fixture。Radar 允许其既有同源本地 event receipt；无生产写。

External Effects：不涉及 Provider、支付、队列、发送或跨身份行为。

## 复用与范围

GitHub 参考采用 [Playwright screenshot guidance](https://github.com/microsoft/playwright/blob/main/docs/src/screenshots.md) 与 [Chrome DevTools Protocol examples](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/skills/chrome-devtools-cli/SKILL.md)：截图绑定 SHA，并由语义、访问、网络与布局断言补足，而不是单独以像素判定成功。

管理员页使用既有 `admin_base`，公共页使用各 Owner 的既有 public host；不造新页面或组件。截图路径只经 `internal/platform/config.RemainingPagesChromiumScreenshotDirectory` 取得，test 不直接读取该环境变量。

Radar fixture 单独创建 Media-owned 的 320×180 本地彩色 PNG，并要求 natural size 与 CSS box 可见，避免复用 1×1 目录素材导致空白“通过”。

## 证据与边界

- archive entry/detail 均在 1280、1440；公开三页均在 375、390、430；每张图名含执行 SHA。
- 断言实际 root/行/图片、无横向溢出、archive 未认证 401、coupon/radar missing 失败态、无效 grid token 无数据。
- Radar 要求同源 `/api/public/radar/{code}/events` 网络 200，随后在其 Owner `radar_events` 回读 `image_loaded`。
- coupon fixture 领取期为 `now-1h` 至 `now+24h`，显式验证当前在窗口内和非微信安全禁用态。
- Archive 单标题与 Grid unknown-total 修复由独立运行时 PR 负责；它们未合入前，相关截图仅作诊断，不能作为最终放行。
