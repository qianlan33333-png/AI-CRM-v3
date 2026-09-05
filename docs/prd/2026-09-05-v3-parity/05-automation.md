# PRD-05 自动化运营旧能力补齐

状态：批准开发；先读 00-control.md；Terra high；沿用“自动化运营”。

## 1. 基线与分类

沿用 `docs/13-PRD-自动化运营-OneID与持久执行.md`，复用 V3 segment、automation、outbound、media 和 frozen UI。旧版参照 ai_audience_ops、automation_agents、已有 send_records 与自动化绑定行为。

OneID：读取 canonical 客户，解析发送资格；不 Provision/合并。持久化：本地事务、受众与调度内部任务、Provider read、outbound 外部写效果。

## 2. 已有与缺口

人群包、计划/策略、话术、发送人、版本和执行内核已存在。当前装配的受众来源覆盖不等于界面全部条件覆盖；现有 ActiveWithin 使用目录 updated_at 不能解释为真实客户互动。生产未执行不等于缺少代码。

## 3. 需求

- 对照旧版实际可用条件，补齐人群定义、预览、刷新、发布、成员列表与入选原因；每个旧条件对应一个已有领域数据源 Port，不能允许任意生产 SQL。
- “活跃”按旧版已确认的业务事件口径移植；目录同步不能冒充用户活跃。来源缺失返回可解释的不可用，不造人数。
- 话术、素材、发送人、策略启停、时间及免打扰规则保持旧版；本轮不新做通用流程编辑器。
- 人群成员进入等旧版实际触发器形成唯一 enrollment，重复事件不重复执行；已有不支持/禁用的旧条件不得凭菜单文案编造能力。
- 手动群发和自动任务均先形成可预览的不可变目标/内容，沿用既有确认操作；实际执行前复核当前发送资格。
- Provider 的接受、执行、未知和结果证明分别回到原记录页面，失败可在现有内核恢复。

## 4. Owner、UI 与历史

segment 拥有人群定义/快照，automation 拥有策略/运行/enrollment，outbound 拥有发送意图和企微写；通过稳定 Ports 和既有版本化事件接线。审批/运行事实与效果接受按需要同 UoW。

保留原人群、Agent、策略和发送记录页面；仅改 V3 Adapter。旧配置及可归属历史记录以现有 importer 延续，历史运行强制只读，不因导入重新触发。新策略默认不自动向生产客户执行。

## 5. 测试及验收

- 每个迁入条件至少一组真实 PG 夹具，验证目录更新不扩大活跃集合、过滤交并行为与旧版一致。
- 预览/发布版本漂移、成员进入重复/乱序、停用、免打扰和发送上限。
- 事务失败全部回滚，重启继续原任务，unknown 不改键重发；测试 Provider disabled 零网络。
- 本地完整人群→策略→批准/触发→发送→结果 journey，旧版 UI 操作对照。
- 历史导入重放及 source 收据对账。输出未合并 PR 与测试证据。

## 6. 非目标

不实现新的指标平台、统一消费者治理、完整 Campaign、AI 生成、短信/邮件/朋友圈或新的身份冲突产品。确实需要补一个旧条件的数据读 Port 可在本板块完成，不扩大成平台重构。

## 7. 总控供体定位与语义冻结

供体 `extensions/ai/ai_audience_ops/template_registry.py` 实际有六种模板，逐项映射现有V3领域数据与Owner Port：

| 旧模板 | 必须保留的关键语义 |
|---|---|
| wecom_contact_registration | 负责人、active/deleted 联系状态、any/registered/unregistered 注册状态 |
| questionnaire_choice_answers | 首次完整提交；题间 AND、题内选项 OR，负责人范围 |
| paid_order | 商品集合、支付时间左闭右开、负责人、有效企微联系人选项 |
| channel_entry | 渠道集合、距进入时间上下界、负责人、有效企微联系人选项 |
| radar_first_click_elapsed | 多雷达 OR，以首次可归因点击为锚点；后续点击不重置；小时/天窗口 |
| member_usage_status | 负责人、服务期、注册、真实使用、会员层级/状态 |

原刷新选项 every_3m / daily_0200 / manual 已有V3持久调度机制，复用相应映射。受限SQL旧入口只允许供体 allowlist 读视图，不意味着可在V3直接跨领域表跑任意SQL；先核对已有DSL编译和兼容入口，保持旧表单操作并通过Owner只读Port获取事实。

d6 composition 实际只装配 `segmentadapter.CustomerSource`，仅处理 ActiveContacts；其 `customer/store.ActiveWithin` 基于目录 updated_at。执行任务需明确覆盖上述六模板，不能把仅有AST或菜单当作已接线。真实使用/注册若已有业务同步数据经其Owner读取；缺少可信数据时明确不可用，不能临时以目录同步或存档收到消息替代旧口径，也不把修复扩大为新分析系统。

paid_order补充冻结：供体模板只选择status=paid；供体迁移0065的orders_v1来自wechat_pay_orders.unionid，对应付款OAuth身份。因此使用可信PayerCustomerID，不能替换成权益受益人，也不擅自把部分退款加入受众。历史已支付订单先核对Order是否有可信paid_at事实；没有status_history时不加时间窗口仍可按paid状态纳入，有时间窗口但没有可信时间则不匹配并保留原因，不能猜时间。现有TestPostgreSQLPaidProductProjectionUsesHistoryAndExactFallback的fallback是商品ID到精确商品码，不是支付时间回退；不得据其推断source_metadata中存在paid_at。补付款人/受益人不同、部分退款、历史paid无状态历史反例。

本板块获准在WeCom、Survey、Order、Channel、Radar、HXCDashboard各Owner新增独立audience_read/port文件提供必要只读事实，由cmd适配到segment契约；Owner不反向依赖segment。共享商品支付及权益已有实现不重写。HXC无已发布投影时显示来源不可用，不把缺失数据冒充空人群。

## 8. 继续实现检查

现有独立工作区提交6cb9133、faa7149仅为局部实现，尚未装配或提交PR。PaidAudienceOrders目前SQL无占位参数却传入reference.UTC()，需修并用真实PG验证；六种Owner来源均须实际查询及边界夹具，不能以编译通过代替SQL执行。保留已有提交继续开发，不重写已冻结接口。

局部LegacyTemplateSource.paid仅在require_active_wecom_contact=true时过滤owner；false时指定负责人条件被绕过，可能扩大受众。负责人条件与联系人活跃条件独立执行，加入“指定负责人且不要求活跃”的反例。HXC来源尚未检查是否存在published版本，不能把未装载投影当空受众。不要把这些局部实现直接启用或标记可发送。

## 9. 接手基线更正与下一批次

最新独立clone实际已有2063669a0b2ece180c3286151ce095369b7fbbf2（在faa7149之后）：已装配六Owner来源并修正SQL多余参数、负责人条件及HXC未发布检查。不要重新实现这些代码。先复核该准确提交的真实PG测试和冻结前端表单到六模板的HttpApi旅程，补缺口后提交以main为base的PR运行PG16检查。根未批准该提交，现有单测不替代全板块。

后续必须补人群刷新/发布→策略/人工群发→批准或触发→共享River→真实本地Provider→原结果页，以及旧配置/历史只读导入逐条对账。此批与03共用Owner读Port仅新增独立文件，冲突及时交根协调；无生产启用、无第二套调度。

## 10. 2063669源码对照发现的未闭环语义

- 原模板参数为owner_userids，V3 AST为owner_staff_ids；冻结HttpApi必须验证边缘转换及Access作用域内标识映射，而非让旧原页面直接发送新自造字段。channel_codes当前与数字ChannelID比较，也须验证原选项值、精确渠道码和历史映射，不能静默空匹配。
- Channel AudienceEntries目前只读channel_history_contacts。必须包含已可信归属的V3原生入客事实，依Owner Port读取已有entrant/assignment/binding；历史与原生相同客户/渠道聚合最后入客时间，重放不重复。不能使上线后新增客户永远不进入渠道人群。
- HXC member当前仅用到期时间推断active，ExpiresAt为空即true；这样会把registered_no_active_membership且无到期时间者选成服务中。按旧is_member与membership_status合同获取明确会员状态，expired也须包含显式expired且日期缺失的事实，不能凭空补到期时间或把注册等同会员。
- 原paid_order按Order来源owner_userid过滤；当前以任意当前企微关系过滤付款人不是同一事实。复核供体0065及后续迁移实际视图、现有Order归属Port，来源缺失时保留明确不可匹配，不擅自用联系人负责人补造订单负责人。
- Radar原0165视图使用可归因authorized/authorized_click及带身份的landing；当前只选内容打开/跳转等后续事件，可能丢失成功授权后内容失败的首次点击。按现有V3已解析事实与原事件语义对照；不能放宽到匿名landing，不能把后续内容请求当新首次点击。owner_scope=all时不要无依据额外要求存在当前企微关系。
- 六Owner真实PG边界用例、实际旧表单预览及每条目标守恒必须随提交交付。以上是旧行为及已入V3事实的接通，不增加新分析平台。

## 11. 原表单复用与Owner类型复核（PR152）

实际V3 `internal/webshell/static/admin_console/admin_audience_detail.js` 的renderTemplates目前只写“AST以JSON保存”，没有渲染六个原表单；savePackage仍读取packageDefinitionInput。仅接收owner_userids的后台转换不等于原界面可操作。

已定位冻结dd8供体 `aicrm_next/app/admin_console/static/admin_console/template_parameter_form.js`，包含TemplateParameterFormController及PackageTemplateController。原页面为`extensions/ai/ai_audience_ops/templates/admin_console/ai_audience_package_detail.html`，已有templateParameterForm节点与脚本引用。优先按必要文件字节冻结复用此脚本及现有原模板，通过V3 Host映射现有闭集模板元数据与接口；不重新手写六套表单，不修改现有冻结业务文件。

- 原表单的questionnaire/conditions.question/options、products、channels、radars需要由各Owner稳定Port解析到可信本地ID/精确code。原可输入稳定ID/code或精确标题；歧义失败，不猜一条，不将旧数字ID直接当新ID。
- owner_userids通过Access解析后持久化本地owner_staff_ids，回填也走同一Access映射；禁止把本地StaffID与Provider userid放入同一个字符串集合。真实Provider userid可为纯数字，必须覆盖StaffID9→userid bob和另一个userid9的碰撞反例。原生Channel Owner提供显式StaffID，历史Provider引用分开处理。指定员工且依赖不可用失败；all scope的空owner_userids不应被422阻止。
- 预览与保存使用同一规范化规则，保存返回可重开回填；激活期间只读状态仍按旧逻辑。实际控件生成的参数进入真实Owner查询的结果，才算六模板Host接通。
- refresh_mode沿原every_3m、daily_0200、every_3m_plus_daily_0200、manual实际选项映射现有River；不能忽略旧增量下拉框或把0200本地时间误作UTC。没有新cron/ticker或自建刷新Worker内核。

本批先保证各Owner查询与字段归属真实可用，再补原表单组合journey。所有PG测试使用真实迁移，不以手造删减约束的表替代；fixtures必须命中需要验证的SQL路径。
