# PRD-07 群运营

状态：批准开发；先读 00-control.md；Terra high。

## 1. 基线与分类

V3 internal/groupops 已有计划、节点、Store、调度、效果绑定、素材准备和回执接线，但发送与目录 Provider 仍缺真正可执行适配。旧版来源以 `docs/migration/groupops/pr06-donor-manifest.yaml` 和 donor 哈希清单为准；复用 web/donors/groupops-v2 与现有 Host。早期 preparation-only 文档的授权限制不是本轮完整迁移的限制，实际实现范围以本 PRD 为准。

OneID：纯群计划/群绑定不涉及 Customer，不人为添加 customer_id；若旧行为确需客户资格读取则通过现有 Port 分类记录。持久化：本地 UoW、共享持久调度、Provider read、outbound 群写效果。

## 2. 用户流程与缺口检查

- 旧版计划列表、新建/编辑/复制/启停/预览，群及员工选择、素材/内容包选择必须完整映射真实 API。
- 支持旧版即时节点、延时节点、执行时间及有序消息；重启不丢后续节点，重复唤醒不重复发送。
- 执行前验证群绑定、发送权限、内容和素材准备凭据；图片/文件/小程序媒体凭据缺失或过期须明确失败，不能伪造 media_id。
- 素材管理复用 media Owner 与现有准备流程；不在 groupops 直接上传 Provider，也不新做素材页面。
- 取消/暂停、部分失败和未知结果延续既有合同；执行结果回到原记录页，不以仅排队为完成。

## 3. Owner 与接口

groupops 管计划/版本/节点/运行/业务回执，media 管素材与准备收据，outbound 管企微写，External Effects 管可靠执行。复用 groupops/port 的 dispatch/runtime/staff/material 相关契约与现有 API。

冻结节点内容、素材、群绑定、发送人政策和 effect binding；需要原子的业务状态、收据、事件及效果接受同 UoW。内部延时使用现有 River scheduled job，不建 groupops cron/lease/retry 系统。

## 4. 历史与前端

迁移已有群计划、节点、素材引用及允许的历史执行只读事实，复用现有 importer，历史记录不得自动恢复为待发送任务。无法确认 Provider 绑定的配置保留并标明不可执行。

原 UI 哈希/交互不变，Host 最小适配。源页面确实没有的扩展管理界面不开发。

## 5. 测试及验收

- 旧版计划所有可见操作及群/员工选择、素材预览、节点顺序与结果展示对照。
- 本地 Provider 完整即时→延时→下一节点 journey；故障注入与进程重启后只执行一次。
- PG 并发触发、暂停/取消竞态、事务接受失败回滚；unknown 不换键，终态不误重启。
- 媒体未准备/已过期、失效群、发送权限变化、Provider disabled 的确定行为。
- 历史计划/节点/绑定结果桶对账；相关 race/PG/协议测试通过；未合并 PR 交付，不向真实群发消息。

## 6. 总控验收证据定位

d6 `cmd/aicrm/group_ops_runtime_integration_test.go:TestGroupOpsPostgreSQLJourney` 证明真实Owner Store/EER事务和结果投影，但通过手动调用RunAttempt执行两个效果，没有实际启动River完成延时节点、重启、暂停/取消竞态。因此不能单凭该用例称完整群运营runtime已验证。

补完整真实共享运行时journey：即时节点执行→进程停启→到期后延时节点执行→后续节点顺序；已接受后暂停/取消及权限/群绑定变更应符合旧合同；同一效果未知不换键重发。复用既有运行内核，补实际 Provider 叶子适配，不能用测试适配器替换真实缺口后宣称完成。

## 7. 总控复查确认的实现缺口

在集成分支4fe92c1（群运营仍为d6基线）实际复查：composition 注册的 outbound.GroupMessageProvider.Execute 即使 enabled 也固定返回 final_failed/provider-not-configured，GroupOpsDirectory 仍为 providerDisabledGroupOpsDirectory。此前“已挂载Provider”的描述只代表对象接线，现更正为发送与目录适配未完成。

- 从只读旧系统冻结实际群写入与群读取协议、发送人政策、素材行为和未知结果处理；在 outbound 既有责任边界内补叶子 Provider，在 wecom 的稳定读取 Port 后补目录，不新建执行框架。只做本地协议服务验证，生产仍默认关闭。
- runtime.buildDrafts 当前把同组同一时刻的多个消息节点全部接受为独立可执行效果，仅 ScheduledAt 相同不能保证顺序。核对旧版即时/延时的顺序语义，以既有共享任务与领域运行事实补依赖检查；同组不能后节点抢先，前节点 unknown 不可被后续节点冒充整体成功。不同群是否独立按旧合同验证。
- 接受后暂停/取消、计划版本、群绑定及发送资格变化必须在实际发送前经既有 Owner Port 再核验；不只验证接受时状态。Provider 网络不得持有业务事务。
- 目录 RefreshOperationMembers/RefreshGroups 当前把Source读取放在UoW内，接真实网络时须先事务外拉取再原子保存完整快照；读取失败或分页不完整不能清空现有目录。
- 复用 `cmd/migrate-v2-config-definitions` 与现有历史导入工具，核对计划、节点、素材引用及历史只读记录的逐条结果；不要另造migrate-groupops框架。现有历史页有读取服务，不代表所有历史来源已导入验收。
