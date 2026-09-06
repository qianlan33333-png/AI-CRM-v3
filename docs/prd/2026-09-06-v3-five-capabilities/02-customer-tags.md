# PRD 02：通用客户打标、去标与批量操作

状态：批准开发；遵循总控。

## 来源和差异

旧 aicrm_next/crm/customer_tags/api.py 的 /api/admin/wecom/tags/live/mark、unmark，dto/live_mutation/wecom_tag_live_adapter 为标准语义：单客户多企微企业标签。local_projection.py 问卷投影不是新CRM自定义标签产品。独立多客户旧UI尚未确认，但用户明确要求批量，不能缩为单客户。

V3 Tag目录/绑定完成但排除赋值；WeCom观察只读；web/src/api/admin.ts 的客户PUT/DELETE tag及客户页交互可复用；entry_tag不是通用命令。首次实现冻结行为差异和必要UI改动。

## 用户流程

客户详情选多个标签加/去；列表选多客户执行同组add/remove。预览范围/标签/不可执行行及原因，确认后返回持久批次、逐客户收据和进度。展示成功/失败/未知、Provider接受及观察差异；不覆盖未请求标签。

复用现有详情/目录选择。若无旧批量组件，最小增加范围选择/确认/结果列表，不建标签设计器。单客户PUT/DELETE与批量单行共用同一Command Port。

## 领域和接口

分类：canonical客户读取/scoped解析，不provision/merge；UoW、River、Provider写与观察读。

Customer拥有tag_command_*批次/行/收据/审计/effect绑定；Tag拥有目录和Provider tag映射；Identity提供可信scoped外部身份；Access/WeCom提供授权与合法跟进员工；WeCom拥有观察及可信刷新。outbound拥有通用customer-tag意图与mark_tag叶子，不伪装entry_tag。

internal/customer/port/tag_command.go提供提交单/批量和结果查询。必须接通客户管理及至少一个其他真实业务调用（优先已有自动化加去标签动作；若无则兼容已有渠道标签入口，保留渠道独立来源/时限语义），先向根提交具体调用点。不用测试假调用证明复用，也不另写Provider。

冻结客户集合、去重add/remove、主体/企业/来源和稳定key。同tag同时add/remove拒绝。权限、tag有效/映射、客户身份和合法跟进人逐行验证；不随意选跟进人。批次+customer为稳定子命令，配置变化不换key。并发同客户命令要显式串行/冲突策略，未知前序不能被后序冒充成功。

逐行接受必须同PG UoW命令/收据/审计/Outbox/EER接受；网络事务外。批量River分段；不另建lease/retry。明确Provider接受后通过现有WeCom读Port刷新观察；requested/provider_status/observed三事实分开。管理员路由Session/RBAC/CSRF完整；未来机器入口用同Port和显式机器权限，无超级管理员捷径。

## 历史和范围

旧操作历史源ID幂等只读导入，客户/员工/tag映射不确定保留原因。历史观察不反向生成mark/unmark。输出总数、导入/重复/pending/conflict/失败明细，重跑零新效果。不做新CRM标签体系、规则推荐或新自动化产品。

## 验收

- T01 单客户多标签及列表批量可操作，局部失败可回读，其他标签保留。
- T02 客户页和至少一真实业务适配器共用Port/outbound，零新Provider writer。
- T03 越权/跨scope/无映射/身份冲突/无合法跟进人零外部写。
- T04 真实PG原子回滚/重放/并发/100+分段重启；逐行结果不丢。
- T05 协议fixture字段/Token边界/部分失败；断连unknown不盲重试；观察不伪造。
- T06 页面旧交互/失败提示，历史fixture幂等/数量守恒/零效果。

Provider默认disabled，测试Provider与真实企微验收分开，local accepted不是完成。

## 首次派发后的接口细化（2026-09-06 根审核批准）

第二真实调用方固定为Channel entry_tag（现有Automation无可用标签动作）。只迁新接受命令，旧KindChannelEntryTag任务继续原路径；欢迎语快路径不改。以旧callback/action稳定source检查旧新幂等，不能同一入客双发。Channel行动行、Customer命令与EER必须同一个transaction；tag_id由Tag Port验证，不由Channel自报。

## 冻结供体复用清单（本次确认收口）

旧仓 https://github.com/qianlan33333-png/AI-CRM，提交 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。下表与总控最新规则共同生效；已有V3实现优先复用，实际完成状态以验收矩阵当前HEAD为准。

| 分类 | 冻结依据/复用对象 | 收口要求 |
|---|---|---|
| 原样复用 | crm/customer_tags 的mark/unmark字段与载荷样例；既有客户详情/标签选择/失败提示 | 保留原用户语义，不重新设计标签产品 |
| Go 等价迁移 | 授权、合法跟进员工、客户标签赋值/移除、结果与旧历史 | 当前Customer命令和outbound链继续收口 |
| V3 已有 | 标签目录、Identity/Access/WeCom Port、客户列表Host、River/EER和渠道入客链 | 客户页与渠道调用同一受控命令，分别保留来源启用开关 |
| 待补齐 | 真实装配的来源开关矩阵、101客户停止/恢复、真实浏览器结果回读及完整CI | 批量操作为用户明确要求；最小范围选择/确认/结果适配，不另造新目录 |
