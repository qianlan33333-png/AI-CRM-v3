# 配置中心：恢复旧版分类、表单和真实生效

来源：旧 aicrm_next/platform/admin_config（api.py、application.py、application_support.py、config_releases.py 和原配置模板），V3 internal/config、adminops、access、openplatform、platform/config 及新壳。图片目标为旧版“配置类目/是否生效/生效开关/配置”表，移除当前无业务意义的“本地安全配置已生效”替代布局。
分类：纯配置不涉及客户 OneID；保存/版本/发布/审计同 Config UoW；凭据只经已有安全引用。领域运行消费走稳定 Port，不跨域读写或通过 HTTP 任意写环境文件/执行命令。

旧分类清单：企业微信基础、后台访问、侧边栏与身份、AI 与自动化、CRM 开放 API Key、API 接入与 Token、Webhook 与外推、稳定性、微信支付、支付宝支付、微信小店、公众号授权。逐项冻结旧字段/校验/按钮/状态及 V3 消费者。保留分类导航与习惯；旧 API Key/Token 分类接现有 V1 调用方管理，不恢复 Direct API Key 或旧 56 API。支付宝旧图未生效，不为外观新增缺失支付 Provider；该类目如依赖不存在，应真实禁用并说明。

已完成：普通配置安全保存、一个运行键的草稿/校验/发布/回滚（automation.operations.max_recipients_per_run）、调用方管理与 OAuth2。待补：真实业务类别/表单、必要非秘密配置的运行消费、真实状态回读，不能把 local_only=true/runtime_applied=false 画为已生效。

完整流程：进入分类→读取已配置/实际有效值（秘密只掩码/引用）→编辑→字段与依赖校验→保存草稿→发布/必要受控应用→API 与 Worker 消费→回读有效版本→回滚为新版本。修改密码/Token沿现有 Owner 管理命令，不能重写密码表。不同配置区分可立即消费、需要重启、外部待配置、不支持；开关必须控制真实能力且附依赖检查，不能仅改本地展示位。不得在保存时自动发消息/退款/支付。

请先生成逐字段映射附录并向根提交设计检查点，再实现已支持领域必要运行配置。优先复用现有 runtime release 机制，原环境值作为兼容默认；显式覆盖需版本化，业务读公开安全值，Secret引用由 Composition解析。按稳定接口加入群读写分离、自动化mode/limit、存档状态和依赖显示。全环境热更新和新秘密托管框架不在本轮；如安全恢复旧字段必须新增机制，先给根具体边界和最小必要方案，不能削弱密钥安全。

专项验收：图二各类导航、表单/保存/取消/校验/开关有真实后端；禁用及凭据缺失不假绿；API/Worker观察相同发布版本；并发发布CAS与重启恢复；旧表单与新壳浏览器全流程；机器凭据不拥有配置权限；页面/响应/日志不泄密。

## 固定约束与验收
旧供体 AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f，只读检出 /private/tmp/aicrm-release-fallback-donors/sidebar；V3 起点 cc4bb8c7e32d4d7d89f6a9a0b0a928bb562584bc，实施前刷新 origin/main。先读 skills/aicrm-v3-development/SKILL.md。实现者在本 PRD 附录逐字段/命令列出原样复用、Go 等价、V3 已有和待补齐的来源证据，之后再编码。前端照搬供体结构、样式、操作顺序，允许 JS 转 TS，仅在 Host/Adapter 做接口适配；遵循已上线单源供体清单机制，不整目录复制。
每板块完整 PR：实际后端/页面/运行装配、必要迁移、架构/编译/专项、真实 PostgreSQL 事务并发恢复、Chromium 操作、协议测试及完整 CI。先修具体失败再跑全量，不削弱冻结或业务断言。历史数据迁移不在本轮。部署只发布审核通过的准确 HEAD，核 API/Worker 版本、dist 和配置实际生效，保留回滚路径。生产网络配置及凭据只经受保护文件；日志、代码、文档不含秘密或 PII。真实发送/支付需有明确业务目标与内容，不为验收擅自发送。
不恢复旧 56 条机器接口，不新建 OneID 匹配、队列、Worker/重试内核，不顺带开发 Campaign、客服权限或客户个人免打扰。发现本板块旧真实流程依赖未覆盖项，报告具体证据，不伪造成功。
