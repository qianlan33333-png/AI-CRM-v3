# 群运营：旧版 UI 与真实企微目录、运行链路

来源：旧 automation/automation_engine/group_ops、相关 admin_pages 和模板；V3 internal/groupops、internal/wecom/adapter、cmd/aicrm。已有侧边任务“修复群运营UI并打通企微数据”继续收口。
分类：群/员工不接客户 OneID，员工复用 Access/WeCom 现有作用域映射；持久化包括群计划及内部持久任务，目录 Provider read 与群发 Provider write 分开。

已完成：V3 群计划、步骤、延时和结果读写；旧群目录 Provider 适配器。待修：群目录读取被 GROUP_OPS_PROVIDER_ENABLED 发送开关一起关闭；页面统计列/详情与旧版不一致。目标流程：选择已有员工→从企微分页刷新群→选择群/素材→配置计划与即时/延迟步骤→按原有启动规则运行→查看逐项结果。
必须用独立目录读取控制，不因刷新群而开启群发；全量分页/失败中断不误清空群，员工映射缺失有明确提示。重复/重启不重复任务。群结果的 provider_accepted 与实际送达分开。前端以用户侧边任务最新图及旧版模板为准，停止自行重画。

## 固定约束与验收
旧供体 AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f，只读检出 /private/tmp/aicrm-release-fallback-donors/sidebar；V3 起点 cc4bb8c7e32d4d7d89f6a9a0b0a928bb562584bc，实施前刷新 origin/main。先读 skills/aicrm-v3-development/SKILL.md。实现者在本 PRD 附录逐字段/命令列出原样复用、Go 等价、V3 已有和待补齐的来源证据，之后再编码。前端照搬供体结构、样式、操作顺序，允许 JS 转 TS，仅在 Host/Adapter 做接口适配；遵循已上线单源供体清单机制，不整目录复制。
每板块完整 PR：实际后端/页面/运行装配、必要迁移、架构/编译/专项、真实 PostgreSQL 事务并发恢复、Chromium 操作、协议测试及完整 CI。先修具体失败再跑全量，不削弱冻结或业务断言。历史数据迁移不在本轮。部署只发布审核通过的准确 HEAD，核 API/Worker 版本、dist 和配置实际生效，保留回滚路径。生产网络配置及凭据只经受保护文件；日志、代码、文档不含秘密或 PII。真实发送/支付需有明确业务目标与内容，不为验收擅自发送。
不恢复旧 56 条机器接口，不新建 OneID 匹配、队列、Worker/重试内核，不顺带开发 Campaign、客服权限或客户个人免打扰。发现本板块旧真实流程依赖未覆盖项，报告具体证据，不伪造成功。
