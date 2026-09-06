# PRD 04：商品/原生已支付订单的真实外推闭环

状态：批准开发，名额释放后派发；遵循总控。

## 冻结来源和差异

旧 dd8d60d：aicrm_next/extensions/commerce/commerce/repo.py 的商品外推配置；api.py 配置/测试/投递记录；external_push_outbox.py 的 transaction.paid；external_push_admin.py 的 paid规划、headers、测试和结果；platform/external_push/service.py 的build_external_push_payload与HMAC；platform/platform_foundation/external_effects/adapters.py 的真实Provider；platform/shared/outbound_https/security.py。

V3 Product ExternalPush配置ref已存，NewLocalExternalPushEffectAccepter只返回进程内eer_N/accepted且RealExternalCallExecuted=false；composition实际注入此占位。Payment已验证回调→Order结算必须复用，历史订单history/effect_eligible=false必须保留。EER/River及Survey受控HTTPS可复用可靠性/传输，不能复制问卷业务状态或替换商品签名。

## 用户和事件流程

1. 商品管理员维护开关、业务参数、受控目标引用；原旧URL/Secret入口通过最小Host适配登记允许目标/安全引用，Product只存opaque ref。空Secret保留语义需与引用轮换一致，不允许页面回读密钥。
2. 已验证付款首次让原生订单进入paid，在原结算UoW追加一次 order.paid.v1 业务事件。重复回调、重复结算不追加第二事件；事件不得把payer和beneficiary混成同一人。
3. 现有Outbox/内部持久任务消费事件，读取Order/Product/Identity稳定Port。首次受理冻结目标/协议/配置revision/必要payload，保存业务投递/effect_id和EER接受同消费事务。无需新轮询器或队列。
4. outbound经现有EER/River执行真实受控HTTPS，安全引用解密在允许adapter内，网络事务外。结果/尝试/失败/unknown可回读，accepted不能称已执行。
5. 商品测试按钮同一真实通道发送明确synthetic数据；不借历史订单做测试。订单可读原逻辑投递与对账；重复测试请求幂等，显式新测试才有新操作ID。

## 旧接收协议必须等价

只实现现有商品协议：Signature为 `sha256=`+HMAC(secret, timestamp + '.' + exact_raw_json)，headers X-AICRM-Event/Delivery-Id/Timestamp/Signature。不能使用Survey的换行签名，不能新增第二协议版本作为本轮产品。

支付payload按旧build_external_push_payload冻结字节fixture和字段表：phone_number/type/day/frequency/remark/submitted_at/questionnaire_title/delivery_id/event；order含id/order_no/out_trade_no/status/paid_amount/paid_at/pay_channel；product含id/code/name/price；buyer含id/openid/unionid/phone。旧openid为掩码，不能升级为原值。测试payload有event/delivery_id/occurred_at/tenant/product/custom_params。金额单位、日期时区、空值、字符串ID和business参数与旧接收方兼容。

身份只从正确scope的可信Identity/领域Port形成快照；禁止从metadata猜phone或直接沿旧fallback复制未验证值。保留字段结构，缺可信证据用协议允许空值/明确阻止并显示原因；不能静默错绑或删必要字段。用fixture覆盖付款人/受益人不同与原有业务配置。接收方确需但证据缺失的字段列明确阻塞，不用新身份规则补。

## Owner、幂等、状态

OneID：读取canonical、获取scope正确的可信快照，不建客。持久化：支付原UoW/消费UoW、River、Provider写。

Order拥有native首次paid事件；Product拥有外推配置ref；outbound拥有commerce-push意图/投递/安全payload与effect绑定及结果sink。通过稳定Port连接，不跨表。HTTP安全基础可下沉既有适当共享层，但不得将Survey业务import进Product/outbound。

逻辑发送ID=原生paid事件+稳定订阅/目标槽（不含可变配置digest）。四摘要首次受理冻结，再次消费先回读原dispatch。首次无配置/disabled也记录明确规划结果及幂等语义，历史回放不得因后来启用自动补推。安全当前停用/授权撤销每次执行检查，但内容/目标/幂等不能被新配置重写。

HTTPS仅受控登记目标：禁止credentials/fragment/非HTTPS/非法端口、内网/回环/云metadata地址、重定向绕过/DNS重绑定；复用现有安全transport。敏感配置ref严格白名单，不任意env/file路径。请求响应安全摘要，PII不进日志/EER。

2xx等旧接收约定证明请求成功，业务送达含义按协议；发送后断连/模糊5xx不能一概安全重试。未知仅原delivery_id对账/Provider确认的幂等重试；没有远端查询能力就明确needs_attention，不能造对账成功。历史manual retry UI只能请求安全重试/对账，不换key。

## 历史/UI与不做

旧商品配置、投递历史离线导入：URL/Secret转受控引用（生产配置留部署），源行ID幂等；历史paid、history/effect_eligible=false、归属回填零新事件/效果。保存原状态/attempt摘要/源时间，未知映射列pending和对账报告，不直接重试历史。

复用商品配置/测试和订单投递页面，Host只补ref、真实状态/attempt与对账动作；原local-only测试历史继续可读，不把eer_N当真实EER。迁移0095，不能修改已上线0010约束历史而无兼容路径。

不重写订单/支付/权益/优惠券，不引入新Webhook产品/协议、多租户、新执行框架。

## 验收

- P01 商品配置→原生支付fixture→同事务事件→真实测试HTTPS→结果页面闭环。
- P02 真实PG支付状态/事件回滚、消费事实/EER原子回滚；回调/事件并发重放恰一逻辑发送；配置变更不增加发送。
- P03 旧字节签名/字段金额时区/掩码身份/业务参数兼容；测试synthetic同Provider链。
- P04 目标限制/重定向/DNS/密钥日志检查；未知结果不盲重试，进程重启保留原ID。
- P05 payer/beneficiary分开，unresolved不猜；历史导入/已支付回放零新效果且逐行可对账。
- P06 页面真实accepted/attempted/响应/unknown显示，disabled不伪成功；真实Provider验收单列生产待办。

付款人/受益人字段映射细化：复用Order现有PayerCustomerID和BeneficiaryCustomerID。旧支付开通会员业务的顶层phone_number从受益人可信电话快照产生（与Order服务权益开通对象一致）；buyer对象从付款人可信scoped身份产生，不将两人拼成一个身份。旧自购场景二者相同，字段兼容；代付fixture明确断言顶层服务对象不因付款人不同而错开权益。缺可信所需字段按已定pending/拒绝规则，不从metadata猜测。
