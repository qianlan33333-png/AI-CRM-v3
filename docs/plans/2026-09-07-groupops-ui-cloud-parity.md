# 群运营标准版 UI 与企微目录读取修复

OneID: 不涉及客户解析或建客；群负责人复用 Access 员工 Port 中的唯一企微 userid 映射。
Persistence: Provider read + 本地 PostgreSQL 事务。所有 Provider 分页成功后才替换目录，目录、收据、审计沿用现有同一 UoW。
External Effects: 不新增效果；独立启用读取，不改变群发开关，不新增队列或重试状态机。

## 已核验根因

生产 cc4bb8c，企微配置存在，群运营 Provider 开关关闭，读取和群发共用开关。真实 gettoken、follow_user_list、groupchat/list、groupchat/get 均 errcode=0；目标运营成员在云端授权列表中，本地映射唯一；本地群目录 0 条。

## 行为合同

以用户图一图二和 AI-CRM main 的 group_ops.css、group_ops.js 为视觉参考；保留四个列表统计、七列计划表、四个详情统计及基础配置/绑定群/Webhook/标准编排四个切换维度。业务值来自 V3，不硬编码标准版截图中的历史数值。冻结供体不修改，使用 V3 Host。

读取开关默认关闭，可独立开启；错误分页不替换旧目录；用户点击刷新后必须报告真实同步结果；不以本地重读作为云端读取成功。
