# Payment domain placeholder

Payment 拥有 PaymentAttempt、ProviderRequest/Receipt、Callback、Refund 和 Reconciliation。任何超时或不确定结果必须进入 `outcome_unknown`，不得盲目换键重试。
