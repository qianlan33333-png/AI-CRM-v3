# PRD-03 商品、购买与支付闭环

状态：批准开发；先读 00-control.md。主责“交易管理”，Terra xhigh；普通商品叶子可由“修复商品管理板块逻辑”限定协作。

## 1. 基线与分类

复用 V3 product/order/payment/public purchase 以及 `docs/07-PRD-交易管理与历史用户数据迁移.md`。旧版来源为 commerce 商品、public_product、微信支付及退款/对账叶子协议。保留完整购买链路与旧版已售数量口径。

OneID：可信支付身份解析/既有显式建客、付款人和受益人分别引用 canonical customer；持久化：本地 UoW、内部任务、Provider read、支付/退款外部效果。

## 2. 当前差异

商品管理、历史订单、支付适配、回调及对账已存在；生产记录主要是历史订单，不能由记录数推导新购买链已验证。OAuth/支付关闭是部署事实；本轮补代码缺口、隔离协议测试与跨板块结算合同，不启用生产。

## 3. 用户流程

1. 旧版商品创建/修改/上下架/复制/分享、价格与销售统计保持原操作与口径；分享进入同源公开购买链接/二维码。
2. 用户从公开页进入安全 checkout，可信 OAuth 关联付款人；受益人独立确定，不默认将每个付款人都当权益受益人。
3. 服务端读取商品/价格快照与优惠券资格；创建订单和支付意图，不能信任浏览器金额或 customer_id。
4. prepay 调用、支付回调、主动查询、退款申请/回调和未知结果对账按现有 PaymentV1 契约执行；验签结果才可改变结算事实。
5. 列表、详情、筛选和导出显示历史/原生来源、金额、支付与退款事实；排队和 Provider 接受不当作支付成功。
6. 微信小店沿用旧版确有的同步/读取/退款范围；支付宝只保留旧版已证实的读取及历史能力，不虚构新支付退款 Provider。

## 4. Owner 与和 PRD-04 的共同合同

- product 管定义和价格，order 管订单/金额快照，payment 管尝试/回调/退款/对账，coupon 管券，现有 order 权益边界由 04 扩展。
- 沿用现有 HTTP 路由、订单和 PaymentV1 Port；必要新增仅限事务化 coupon 预占/核销/释放与权威结算读/事件窄契约。
- coupon 预占与订单折扣快照同 UoW；支付成功以订单行/结算事实标识幂等通知权益。通过 Owner Port 协调，不跨域表写入。
- 成功支付后权益处理失败不得倒改支付事实；保存可恢复处理依据。退款影响按旧版规则，由 04 幂等应用。
- 支付未知时不释放券或重建不同付款请求；累计退款并发不得超过权威实付。

## 5. 历史及前端

复用现有冻结商品、订单和公开购买前端，仅修改 V3 Host/Adapter。历史订单/支付/退款导入工具按 Provider、状态、币种、金额、source key/digest 对账；历史事实不重新申请支付退款，不自动补发权益。受益人缺失保持未解析，不能复制付款人兜底。

## 6. 测试与验收

- 旧版商品所有可见动作、分享链接、已售统计、上下架及完整购买 journey。
- 本地签名 Provider 模拟 prepay、重复/乱序/非法签名回调、超时未知、原键查询、并发退款。
- PostgreSQL 订单/收据/效果同事务失败回滚，优惠券并发预占，支付成功重复不重复核销/开通；支付未知不释放。
- 历史导入重放、金额/笔数对账与未解析受益人保留。
- 03 自身可先独立 PR；联合 04 的购买-支付-优惠券-权益和退款 journey 必须在集成分支完成后才算板块测试通过。
- 不生产付款、退款或启用 OAuth。返回未合并 PR、HEAD、测试证据和部署待办。

## 7. 已核实的接线与增量

- d6 基线的公开购买 Handler、Composition Adapter 和0061迁移已经存在。局部 `ShareLocalProduct` 在缺少权威公开路由时的拒绝是安全默认，不能据此重新开发整个购买链。
- `payment/session.Service.IssueTrusted` 当前把未指定受益人直接置为付款人。本轮按已批准的独立受益人合同修复：migration0068允许未确定的会话受益人，并记录明确选择来源；旧记录原值保留，不伪造新确认事实。
- 公开购买沿既有确认动作明确“为本人购买”，由服务器按可信付款人落定；已有管理员辅助入口只采用已受信的会话受益人。前端不能传任意CustomerID；不新增赠送/代购流程。选择、建单与失败回滚必须覆盖真实PG的CAS、并发和重放。
- 03负责普通建单/结算接线和order/port/port.go；04负责coupon Port与实现、order既有权益文件和周期商品叶子。券持有人沿旧可信付款人，与权益受益人分别记录；预占返回原价/折扣/实际应付/币种/规则版本及预占引用，订单保存不可变快照，退款上限依据实付。

## 8. 下一轮03/04联合实现交接

03的PR136@1329c1e已完成独立购买链接和受益人选择缺陷，04的PR138提供领域Port但仍待自身PG修复。接线开始前从root领取PR138最终HEAD，沿用03原clone，从1329c1e建立新codex分支并合入已核实前置提交；保留父PR，不改主线。

- Coupon：OrderCouponCoordinator的ReserveWithin/ConsumeWithin/ReleaseWithin。无指定券且无可用券返回CouponApplied=false原价快照，显式无效券失败；订单冻结完整金额/币种/商品type+id+code/规则版本/预占引用，同UoW接受支付效果。选择当前付款人的券，不能按受益人误取。先核收据重放，避免原价订单重放时又选到新领取的券。
- Order：ServicePeriodEntitlementCoordinator的GrantPaidServicePeriodWithin及ApplyServicePeriodRefundWithin。商品周期按服务端CheckoutProduct.ServicePeriodDurationDays冻结；明确self选择后按受益人发放。首次来源订单成功退款按原整期撤回一次，后续同订单退款不重复扣减。历史映射只用HistoricalServicePeriodSourceCoordinator的可信订单行及实际天数。
- Product：ServicePeriodPublicReader.ReadPublicServicePeriodByCode只允许enabled周期商品精确code。新增独立周期公开Host/Payment入口，复用旧公开模板，不让普通product数字别名误命中周期商品。表单、复制、分享、上下架和DurationDays保存需完整原UI回归。
- 公开领券：按PRD04第8节补/c/{public_slug}与可用券/状态/领取入口；复用现有支付OAuth身份会话，禁止第二套认证/OneID。购买页可用券选择来自Coupon Port，浏览器不能指定客户、价格或周期。
- 数据页：04已提供CouponClaimAdminReader与BindWithClaims，需composition挂couponData，补旧统计和列表适配；spProductData/周期会员表读模型与原页面也需真正挂载。内部Port存在不算UI完成。
- 历史订单、支付、退款、会员开通、券领取/核销快照逐条对账，缺失归属/周期/时间证据不猜测；不得以补造grant/effect完成迁移。
- 实际共用PG/Provider协议/Runtime验证：公开购买→用券→支付→权益→退款，所有回调重放、unknown、并发、同键跨时刻、用户视角页面结果；本轮真实Provider仍只用本地模拟。

05任务独占各Owner新增的audience_read.go/port/audience.go及对应cmd受众适配，不与交易任务同时改支付文件；03不删除这些预留读接口。迁移0066—0074已登记，联合订单快照等若确需新迁移先向root申请编号。

## 9. PR143 总控审核退回项

本节是原验收范围的具体缺口，未增加产品能力；尚未修复和验证前不得声明03/04闭环完成。

- 周期会员表：`product/http/handler.go` 的 serviceMembers 仍返回空数组，memberGridAccess 全部 false，视图/分享仍为固定占位。须通过既有 Order 权益与权限 Port 接真实查询、筛选、备注、保存视图和旧版已有协作行为；每个可见动作必须有实际结果及 HttpApi journey。
- 为补上述既有会员表行为，批准0079由Product Owner持有保存视图、协作者及旧有分享设置元数据；Order继续持有权益及其既有备注事实，客户显示经既有Customer Port。先冻结旧版视图/协作/分享合同，复用Access主体及权限，不新造公开客户目录或第二套鉴权。越权用户、已撤销协作及无效分享引用不能读取会员资料；更新同UoW记录审计/Outbox。不得把客户身份或权益复制为Product第二份主数据。
- 周期商品公开页：当前新写 service_period_template.go 与仅作哈希检查的冻结 Python 供体没有形成实际复用。应提取旧版模板、样式、脚本并作最小 Go Host 适配，恢复旧版详情媒体、添加企微二维码、不可购买状态和真实剩余周期比例等已有行为。逐项说明必要适配；不在运行时依赖旧 Python，不重写新的页面。不以只校验未使用供体的哈希证明 UI 保真。
- 公开购买页券选项 value=0 当前显示“不使用优惠券”，但 Owner 合同为自动选择最佳可用券；按旧版实际选择合同统一界面和服务端，不能以“不使用”的文案暗中选券。
- 购买请求网络失败或结果未知后的重试必须保持同一逻辑 checkout 幂等键及已知 merchant_order_no。当前每次点击生成新随机键，会绕过原未知结果的恢复路径。首次请求前持久保存键，未知时查询/恢复原请求，只有权威终态和明确新购买才生成新键；覆盖创建响应丢失、轮询超时、刷新与再次点击。
- 补真实 PostgreSQL 的公开购买、优惠券、支付、权益联合 journey：本地签名 Provider/回调驱动结算，验证开通、续期、首次正额退款、重放、未知、同事务故障回滚和并发。现有“原价订单重放不新选券”测试保留，但它不能替代支付回调与权益的完整路径。
- 历史订单/支付/退款/权益/券的冻结快照导入、重放与逐行对账必须在隔离库完成本轮测试。生产执行留下一阶段，测试工具及隔离验收不能整体移入生产待办。每行有明确来源、摘要、目标或隔离原因；核实不会创建新的付款、退款、发送或历史补发权益。

迁移0076归 Order checkout 不可变快照，0077归 Coupon slug；不自行占用其他编号。历史与会员表可分成依赖明确的独立 PR，最终验收仍覆盖以上全部行为。

公开图片复用线索：Product LegacyAdminProjection 已持久保存 slices；Media 已有 ImageVariantReader.GetImageVariant、ImageLibraryReader.LocalImageExists。旧 `_detail_image_source` 支持 image_library_id/library_image_id/asset_id 及 URL/src，公开 `/api/h5/product-images/{code}/{id}/variants/original` 依据商品绑定读取。沿现有 Product 绑定与 Media 只读 Port 最小适配，不能把封面 Images 当作完整详情图，也不能开放任意后台媒体ID；历史媒体ID若未建立可信映射须在导入对账中明确，不能猜测。

## 10. 周期公开页渲染复查（68e811e后）

模板已真正复用，但Go Host只归一化双大括号，没有执行Python字符串字面量转义。总控按现有替换步骤提取状态脚本并交Node语法检查，fetch路径正则报 `SyntaxError: Invalid regular expression flags`。修复Host的模板提取/转义，不改冻结供体；对实际Go渲染出的完整HTML执行脚本及浏览器交互测试，不能只断言字符串标记存在。

当前按map遍历在完整页面不断ReplaceAll，然后对已插入用户内容与普通三引号函数片段再全局折叠大括号，可能改变标题/正文中的占位符或合法JS括号。模板转换应在插入动态事实前完成，动态事实只代入一次；保持原DOM/样式/行为。用含花括号和模板字样的标题、详情及企微模态脚本验证不受二次替换影响。服务中/过期/未开通/不可购买、详情图、二维码、状态刷新、报名/续费跳转均需实际浏览器无脚本错误。

沿现有可信OAuth会话适配旧公开页身份恢复；不得直接删除旧身份引导后默认为所有新打开者都未开通，也不能接受旧签名片段或裸外部ID自报可信。记录必要Host变化和原入口的测试结果。上海日期边界的到期日须在服务端初始页面与刷新后结果一致。
