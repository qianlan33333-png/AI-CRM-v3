# 各板块独立合并与生产验收待办

## 当前部署状态（2026-09-06 20:24 UTC）

五个业务 PR #169/#170/#171/#172/#173 与新前端壳 #164 均已合并并部署，生产准确版本为 `5291366b9742030957f48ebf7464a040a3ab46db`。PR 与实际 main 的完整检查均通过；原 installer/run1018 安装完成，双服务实际程序、本机及公网 readyz 一致。生产30页面、284资源及新壳/业务Host检查通过。开放平台两个运行参数生效，正常HTTPS OAuth/REST/MCP生命周期通过；17客户端与14审计已导入并核验。外推24配置/9启用、标签Provider及原有全局开关在最终版本保持生效。

本轮开发、审核、合并、代码部署、必要运行配置和冻结历史导入已完成。仍须另行处理：负责人4,084条、标签115条、外推7配置/3投递的历史待映射；12旧开放客户端需重新签发，5项旧授权无V1等价关系已排除。配置仍revision0/environment_default/max_recipients1，未发布新业务值。真实企微转接/mark-unmark、新订单外推和企微WebView业务场景未执行现场验收，不将协议测试或代码上线冒充这些真实业务结果。以下日期段落是历史记录，最终事实以本节及末尾上线核验为准。

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
- 2026-09-06 16:56 UTC交叉复核纠正：31条旧外推配置中24条有明确V3商品来源映射（9启用）；7条缺映射，其中3条启用配置引用的商品在旧wechat_pay_products和service_period_products中均不存在，另外4条停用配置中3条对应现存active普通商品、1条也不存在。此前把 enabled_configs=3 与 existing_configs=3 误当同3行，预计27条恢复的结论撤销。PR175的3商品追加不适用于恢复这些启用配置，停止合并和执行；不得创建替代商品或猜配。先恢复准确映射的24条，7条完整保留历史并逐条记录目标缺失或定义未导入原因。只读交叉证据：aicrm-push-config-cross-classify.py。
- V3 Open JWT签名材料及可信代理配置已在 `/var/tmp/aicrm-open-platform-v1-20260906/runtime-prepared.json` 受保护文件准备，尚未应用、未发放机器凭据。可信代理仅127.0.0.1/32，对应只读核实的现有Caddy回源；不得信任任意转发头。密钥不进入文档或代码。
- 根已用服务器现有受保护 bootstrap 凭据验证正常 HTTPS 登录并退出，未重置管理员。外推业务配置恢复将沿真实认证/CSRF/Product HTTP UoW路径，不能伪造actor或直接写Product表。


## 2026-09-06 16:15 UTC 标签与配置历史已应用并核验

使用生产8ec实际发布二进制，对旧revision41f80a11835445c034fdd39f69a6b6712722bb98只读提取：标签305条、配置发布8条。受保护目标目录 `/var/tmp/aicrm-tag-config-history-20260906T161200Z`（0700），tag.json、config.sealed、config.key均0600；旧主机临时文件已移除，本地未落敏感副本。标签文件SHA256 `0127bcc47fa7a8bb1937aed6e47a93f5f74c78a32cdc6b51cd0ce8b1ec945cd3`；配置manifestSHA256 `b70c5265a89e80e43db7a67aaf653fc0988f6b6fdbd2cad9a8c84a6e060a4b99`，配置密封文件SHA256 `8171dc5168dd65793347f3a02c6df42bd62524466074947e9329484d5b748390`。

inspect/dry-run审核后，root以准确摘要apply并verify均通过：标签190条imported，115条pending（106 tag_unmapped、8 follow_user_unresolved、1 staff_unresolved），0conflict/failed；未猜测或自动创建身份。8条配置发布历史均以no_v3_runtime_equivalence保留排除事实，不应用旧值。实际生产回读customer_tag_commands仍0条、published runtime releases仍0条；Provider/effect/River计数全部0。能力上线与历史待映射保持分开报告。

## Open 历史准备（历史检查点；最终应用见末尾）

090889a已审工具从同一旧revision只读提取17个API Client、14条调用方审计。材料当时仍在旧主机 `/var/tmp/aicrm-v3-open-platform-history-090889a/open-platform.snapshot` 与同目录snapshot.key；最终部署后才传入V3，原先记为已在V3不准确，父目录0700、文件0600。manifest `6d01d492fb6f4fb5bc450cfa9e65d175ddb56fa836a73efa852e2455d791cfcb`，密封文件SHA256 `911da5d065cc4d4ae50f22306bbd0635fb62eda93fc455a42273b8803590e1b4`。extract/inspect/dry-run通过，旧源及本地敏感临时文件已删除。未应用目标数据库，所有旧client仅允许disabled/reissue_required或带原因excluded，不恢复secret/token。

## 2026-09-06 17:18 UTC 商品外推代码已部署、历史已对账

main05045c645f95d269b624771ceb215713e3300f59的Linux check job101518074524已成功。自动部署跨区上传缓慢，根在独立精确HEAD构建全部20个Linux二进制、94项迁移及前端资源，制品226文件/77,118,012字节；归档SHA256 `7173309f191848d72c705276ca375b2eb4d080f6156f818bdde5f89e651835f0`。本地cgo runner使用Zig 0.13，未声称与CI GCC字节相同；生产服务器已验证SHA及Linux loader兼容。原流水线34044888183仅在check成功后取消慢速deploy，根使用仓库原installer/run983完成安装；这是人工部署证据，不把取消的整条workflow写成成功。

17:15公网readyz、current symlink、aicrm与effects-worker的实际exe均为05045，原outbound/customer-tag/wecom开关均保留true。普通HTTPS管理员登录、运行配置读取和退出通过；有效配置仍revision0/environment_default/max_recipients1，未发布新值。CommercePush Provider仍false，新壳#164和负责人#171/开放平台#173尚未上线。

外推旧源manifest c8c20c8c1ef30bb19296eb01a7274a39c8997fb4b27375d3818d9bb8b784fbce已用当前发布的迁移器apply并verify：800输入=406 imported+10 pending+384 excluded。生产SQL逐项回读：24配置/382投递已关联，7配置/3投递product_mapping_unavailable，384旧domain_event_outbox以legacy_domain_event_not_replayed保留排除事实；live commerce intent及commerce_product_push effect均0。

运行配置受保护准备文件为 `/var/tmp/aicrm-push-history-20260906T150000Z/runtime-prepared.json`，24配置、9原启用、7排除项、9受控目标；payload key已生成并封存，尚未应用runtime或保存业务配置。PR175已关闭且未合并/部署/apply，明确撤回不适用的追加方案。


## 2026-09-06 18:13 UTC 外推配置运行启用与开放平台合并

此前外推第12项404已定位并验证为准备文件路由错误，不是商品源映射错误。正式导入器通过旧 ServicePeriod.TradeProductID 将同一商品投影为 V3 service_period，并保留两个源映射；受保护只读核对确认源关联、导入batch、商品code/price/currency及duration均一致。历史表不依赖ProductKind，该商品1条配置和19条投递历史仍可读取。

原准备bundle SHA256 `969f00b04b94baa61b9d82b026ad4684e15e97fec289fdfd3404840a31b24ef3` 仅将第12项改为正式service-period-products路由，其余字节不变。新SHA256 `ca1c7ae1bc8fe36a758683c1a6db23d35065ed92e045e79249fe20a05aae6358`；原bundle保留0600备份。正常管理员登录后的24项GET均验证了正确product_id/kind和revision0。

根审核过的运维脚本随后在cf663生产版本成功完成：23项普通商品+1项周期商品全部保存并回读revision1，9项沿用启用状态；24次PUT带原幂等键与CSRF。完成受保护收据后启用Commerce Push Provider，并验证双服务环境与原全局开关。零测试调用、零历史paid重放；真实新订单Provider投递仍为独立验收项，不以配置启用冒充投递成功。

开放平台#173准确f8c2a0f完整CI34049122474成功并已合并main `f1ea84caa0070bbd4cc06d6d0628999d31567dc7`（18:05:27 UTC）。主线检查34050611621与新壳验证并行，开放平台运行参数与历史尚未应用，PR164尚未上线。

## 2026-09-06 最终代码发布：5291366

#164准确cf0130536c5160fe5929cd592f343b09a88942c9完整CI34054695213通过，已squash合并main5291366b9742030957f48ebf7464a040a3ab46db。PR/main Git tree一致。实际main CI34055889032/check101547594868于20:07:41 UTC成功（仓库、所有Chromium、race）；随后取消重复自动部署，deploy101550645752的SSH与安装步骤均skipped，不将整条cancelled workflow称为成功。

根对独立exact main制品逐文件审核：归档SHA256 `10d47a9c875baab1b6164b891610226fc7ae434f800e1f3d57e3c3bc428722ec`，84,984,686 bytes，291常规文件=290 manifest条目+manifest自身；22个Linux amd64程序、100迁移、145 web/dist文件。22个Go程序均revision=5291366且modified=false。无AppleDouble、额外文件、链接或逃逸路径。源码与index干净，运行资源及供体检查通过；CGO runner用Zig0.13交叉编译，不声称与CI GCC字节相同。

服务器归档及原installer再次验SHA；installer `f1c3fac230b86be4a42920a668aedc82e6ace62b79ff6c57db53fc93fd933e70`，按实际run1018执行exit0，日志 `/var/tmp/aicrm-release-529136-local-install.log` 保持0600。current、双服务实际exe、本机与公网readyz均精确5291366，last-successful-run-number=1018。

Open两个受保护运行参数已应用，正常HTTPS管理员登录、OAuth client_credentials、REST/MCP按grant过滤、轮换撤销旧JWT、新凭据激活、停用撤销token/credential及audit回读全部通过。验证调用方最终disabled，session正常退出；Provider/customer/payment写入均0。旧Open历史首次apply包装器在inspect前因受保护快照目录缺失停止，目标manifest所有batch/receipt/linked-client/audit计数为0，无部分导入；材料查找与恢复继续单列。

发布后外推只读复核通过：24配置/9启用（23普通+1周期）、revision/receipt、双进程三个owned配置及既有全局flag均保持一致。真实新外推intent/effect/job/attempt/test均0，不声明真实投递完成。生产登录页已用实际浏览器验证布局；后台HTML/资源检查正在修正Owner脚本合法版本query造成的验证器误判。


## 最终上线核验（2026-09-06 20:24 UTC / 北京时间09-07 04:24）

Open首次受保护材料缺失在任何导入前中止，无部分写入。随后在旧主机找到原冻结材料，源文件摘要仍为911da5d065cc4d4ae50f22306bbd0635fb62eda93fc455a42273b8803590e1b4；通过两端已验证SSH直传至V3的0700目录，snapshot与key均0600，本地不落密钥。当前发布CLI inspect/apply/verify均成功：17客户端=12 imported/reissue_required+5 excluded，14审计imported。根另用强制只读PostgreSQL聚合核实1个batch、17客户端收据、12关联客户端全部disabled且reissue_required、5排除、14审计收据，replayed均0。旧secret/token不恢复，不触发业务效果。

生产页面验收最终通过30页/14组、3条精确规范重定向、负责人/Open/周期运营Host标识；284同源资源（263JS/21CSS）均HTTP200、MIME正确且不是HTML，包含62个哈希新壳资源。开放平台目录恰六个V1操作，5个退休路径实际GET返回404；正常管理员登录/退出通过。此前两次失败分别为Owner脚本合法版本query、AI别名精确302到/admin/cloud-orchestrator/plans未登记，均只修复本地验收器；保留原路径、状态、同源及资源断言，没有更改生产业务代码。

最终验收器SHA256 d6f13edb63cbeae0e9e94bfd8021c7f083ace58745812705a3d41ba51f17d60a；根独立审查改动并运行合同测试通过，执行者生产全量只读运行exit0。业务写入与Provider调用均0。实际浏览器验证生产登录页布局；后台业务真实Chromium证据来自完整Linux CI，生产上述30页为HTTP/资源验证，不冒充浏览器业务操作。

所有实现PR维持各板块独立闭环；#166仅提交本记录，不再次汇总业务实现或触发多余代码发布。#168与#175保持关闭。旧系统未停机、未切流。
