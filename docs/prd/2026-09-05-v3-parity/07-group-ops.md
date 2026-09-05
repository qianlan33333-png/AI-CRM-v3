# PRD-07 群运营

状态：批准开发；先读 00-control.md；Terra high。

## 1. 基线与分类

V3 internal/groupops 已有计划、节点、Store、调度、效果绑定、素材准备和回执接线；README 某些“contract only”描述较早，必须以当前 composition 与实际代码验证。旧版来源以 `docs/migration/groupops/pr06-donor-manifest.yaml` 和 donor 哈希清单为准；复用 web/donors/groupops-v2 与现有 Host。

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

补完整真实共享运行时journey：即时节点执行→进程停启→到期后延时节点执行→后续节点顺序；已接受后暂停/取消及权限/群绑定变更应符合旧合同；同一效果未知不换键重发。保持现有已挂载Provider装配，不因README过时文字重建执行器。
