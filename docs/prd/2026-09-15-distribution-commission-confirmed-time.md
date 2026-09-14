# 分销用户收益：系统分账确认时间

## 问题

`ListCommissionsByCustomer` 将 `NULLIF(c.paid_minor, 0)`（金额）扫描到
`*time.Time`。只要该客户有已分账佣金，公开收益明细读取就会失败；金额为零的
夹具没有覆盖该路径。

公开页的时间只能代表结算系统对某一结算单的成功确认，不能以佣金金额、记录
更新时间或银行到账来推断。

## 范围与边界

- **OneID：不涉及。** 已有会话解析为既有分销员 Customer，只在
  `d.customer_id` 范围读取其佣金。
- **持久化／外部效果：不涉及。** 仅改 Distribution Owner 的稳定只读投影和
  用户页既有文字；不写入结算、审计、订单或 Provider。
- **确认事实：** `paid_at` 只取同一 `commission_id` 下、payload 的
  `settlement_reference` 与实际结算单一致的
  `distribution.settlement_paid.v1` 审计时间；多笔匹配结算取最后一笔（`MAX`）。
  没有匹配审计时字段缺失，页面继续显示“未记录”。
- **非范围：** 不处理收益页状态切换／加载更多的请求竞态，不扩展筛选、分页或
  数据规则。

## 真实入口与参考

| 项目 | 真实路径 | 本次处理 |
| --- | --- | --- |
| 用户收益 API | `internal/distribution/store/readmodel.go` → `internal/distribution/http/handler.go` | 修正公开 CommissionPage 的确认时间读模型。 |
| 用户页 | `web/v3/distributionCenter.ts` | 沿用现有收益页和视觉，只纠正为“已分账／最近分账确认时间”，并说明以系统成功确认记录为准；禁止使用“到账／到账时间”。 |
| 同域参考 | PR #301 `internal/distribution/store/order_read.go` | 复用按 `settlement_reference` 匹配审计的只读事实，不读取 Order／Payment 表。 |

Product Design 路由：沿用既有收益页的卡片与明细列表，只纠正事实文案和时间解释；不重构收益页布局。

## 验收

1. PostgreSQL 覆盖 `paid_minor > 0` 但没有匹配审计、同佣金匹配审计、其他佣金或
   错误结算单引用的审计、以及其他分销员客户；只返回本客户记录，且只有精确匹配
   的审计时间进入 `paid_at`。
2. 公开 HTTP 响应在有确认事实时给出 `paid_at`；没有时省略该字段，读取不因金额
   扫描失败。
3. 用户页在 375、390、430 宽度继续用“最近分账确认时间／未记录”展示现有
   API；不宣称银行到账。
