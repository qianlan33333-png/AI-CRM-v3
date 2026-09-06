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

## 负责人历史上线准备（2026-09-06 14:10 UTC）

根以旧环境运行进程的受保护数据库配置执行单次 REPEATABLE READ READ ONLY 导出；旧/V3的企微企业ID一致，未把旧数字客户ID直接用作V3客户主键。旧34个结果批次展开为32,411条明细（0空批次）。未修改旧环境服务、配置或数据库。

已在V3临时目录 `/var/tmp/aicrm-owner-history-20260906T140303Z/owner.snapshot` 使用现有Survey数据密钥生成0600 AEAD快照，源流/审核过的离线导入器均经SHA-256校验传输。快照SHA-256：`282d2b230cc0d4b4dbeb372148b78555f5cda81d393ad95c1ab2714e2a36cb38`；run_key：`owner-handoff-history-588ca152f03b9084e990ad58`。inspect-stream和dry-run均验证34批次/32,411行，原始明文流已从本地与目标临时目录删除。

这里的dry-run只验证离线快照结构，未解析目标数据库身份，输出pending/conflict/invalid为0不代表全部可映射。尚未apply/verify，未写入V3业务表；Provider调用、EER、新建/关联OneID、负责人更新均为0。待#171准确HEAD整板块审核并部署0092后，用发布包导入器按上述精确快照摘要apply并verify，只写历史账本，实际映射结果另记。离线inspect不等于#171已部署。


## 商品外推历史与运行配置准备（2026-09-06 15:28 UTC）

根使用审过的 514068d 历史工具，对旧生产实际 HEAD `41f80a11835445c034fdd39f69a6b6712722bb98` 做 REPEATABLE READ READ ONLY 提取：31 配置、385 投递、384 Outbox。多执行关系保留完整数组，不选最新任务代替归属。源数据库写入 0。

V3 受保护文件为 `/var/tmp/aicrm-push-history-20260906T150000Z/history.sealed` 和同目录 `history.key`（均0600）。canonical manifest SHA256 是 `c8c20c8c1ef30bb19296eb01a7274a39c8997fb4b27375d3818d9bb8b784fbce`；加密文件传输 SHA256 是 `68ce63b90028288d6a6c97b329714723c4206275b4f9d2cc6a733f7961e4d7b9`，两者用途不同。源临时文件和本地敏感副本已删除，尚未应用目标数据库。

根只读核实 V3 商品外推运行配置为 0 行；既有 config_definition_import_source_maps 有31普通商品与2周期商品映射。历史账本导入不能代替运行配置启用。部署 #172 后须用真实管理员认证/CSRF，通过现有 Product HTTP + UoW/CAS/receipt 保存旧业务字段，URL/密钥只写受保护目标配置。旧12启用项在唯一稳定映射与受控目标核实后方可启用；不得伪造 actor、直接写 Product 表或补推历史支付。


## 2026-09-06 16:06 UTC 运行前置核验

- #172已合并为 main65d，但主线CI失败、部署跳过；须经独立修复#174完整门禁及实际release验证后才能应用0095和外推历史。
- 31条旧外推配置中，24条有明确 V3 商品来源映射，7条缺映射。旧库只读核实缺映射中的3条仍有 active 商品且启用外推，另外4条对应商品已不在两张旧商品表中且配置停用。不能按数字ID猜测。先恢复3条真实商品的受控追加映射；预计27条运行配置可恢复，4条仅保留历史并显式记录未恢复原因。现有全量配置定义迁移器强制31条基线且拒绝已存在商品代码，不能直接重复全量导入。
- V3 Open JWT签名材料及可信代理配置已在 `/var/tmp/aicrm-open-platform-v1-20260906/runtime-prepared.json` 受保护文件准备，尚未应用、未发放机器凭据。可信代理仅127.0.0.1/32，对应只读核实的现有Caddy回源；不得信任任意转发头。密钥不进入文档或代码。
- 根已用服务器现有受保护 bootstrap 凭据验证正常 HTTPS 登录并退出，未重置管理员。外推业务配置恢复将沿真实认证/CSRF/Product HTTP UoW路径，不能伪造actor或直接写Product表。
