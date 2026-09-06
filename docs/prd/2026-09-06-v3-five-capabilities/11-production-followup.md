# 各板块独立合并与生产验收待办

用户已允许整板块验收后独立合并。每个板块分别记录代码合并及既有自动部署的实际结果；以下配置、历史导入和真实业务操作保持独立验收，不混写成代码开发已完成：

- 每个板块核对准确 HEAD、完整 PG/前端/协议/历史/架构门禁及回滚方案，通过后独立合并；#167共用修复单独处理。main既有自动部署按实际流水线结果记录，不等待五项汇总，不使用#168发布。
- 在受保护环境核对身份scope、staff/provider tag映射，迁移新增表，配置Provider开关保持可控；先备份/预演/可逆验证。
- 负责人及标签使用用户明确的小范围真实样本，核对Provider逐行结果、观察同步、部分失败和对账；不得全量默认执行。
- 商品外推登记真实受控目标与安全引用，接收方验签/字段fixture确认，使用独立测试事件，再观察一次授权真实支付；历史订单不补推。
- 机器客户端签名引用/issuer/audience/CIDR/权限范围、一次发放新凭据；旧客户端导入默认disabled需明确重新发放，不恢复历史Token。
- 配置旧历史导入保持inactive；审核业务草稿后发布，分别回读API/Worker实际使用revision，回滚验证。
- 历史导入工具先dry-run/fixture对账，再独立生产导入，pending/conflict逐条处理，不因导入触发发送、转接或标签写入。
- 最终分开报告上线代码、有效配置、实际业务Provider证据、尚未启用能力；旧系统独立保留。

负责人冻结快照复用现有AICRM_SURVEY_DATA_KEY，AAD独立为customer-owner-handoff:v1；不增加Owner专用主密钥。后续轮换该现有键必须同时考虑Survey与Owner历史密文，不能单独替换使旧记录不可读。

截至 2026-09-06 10:58 UTC：#170/main5494537 自动部署成功，生产 /readyz 已核实该 SHA；#169/main8ec5072 已合并，部署待核实。两者均未据此执行生产业务配置发布、历史导入或真实 Provider 操作。上述待办保持未完成。

2026-09-06 12:07 UTC更新：#169/main8ec5072自动部署已成功，根GET生产readyz核实完整SHA与ready状态。配置#170随该main继续包含。真实打标/转接/外推、业务配置发布、凭据启用与历史导入仍未执行；负责人/外推/开放平台未合并部署。

2026-09-06 用户最新明确要求继续直到完全上线并包含PR164新壳，开发后发布已有授权。根13:01 UTC只读复核V3：ubuntu SSH可用，current及readyz均为8ec5072，aicrm.service和aicrm-effects-worker.service active。后续以实际最终准确HEAD、制品及业务流程验收推进；旧root SSH账号不可用，不属于密钥整体失效。新壳release制品缺页及CSP兼容列入12文档。

2026-09-06 13:23 UTC：根按最新上线授权启用已验收并部署的通用客户标签运行开关。启用前只读PostgreSQL核实customer_tag_commands为空，不存在遗留执行任务；确认AICRM_OUTBOUND_PROVIDER_ENABLED与CHANNEL_TAG已有true。对/etc/aicrm/aicrm.env创建0600受保护备份，仅新增AICRM_CUSTOMER_TAG_PROVIDER_ENABLED=true，原子保留文件Owner/权限，重启aicrm.service与aicrm-effects-worker.service。核实两服务active，/proc实际进程环境该布尔均true，公开readyz仍准确8ec5072d169c25abd25f80e29cb9f6222834b320且ready。没有发送真实mark/unmark，没有历史导入；配置生效与真实业务验收保持区别。
