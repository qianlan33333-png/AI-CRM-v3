// Shared, fact-preserving presentation labels for every V3 distribution view.
// "已分账" means the platform recorded a successful split. It must not be
// presented as a bank-account arrival time.

function valueOf(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

export function distributionCommissionStatusLabel(value: unknown): string {
  const labels: Record<string, string> = {
    pending: '待结算', held: '暂缓结算', settling: '分账处理中', paid: '已分账',
    cancelled: '已取消', exception: '异常待处理', zero_commission: '零佣金成交',
  };
  return labels[valueOf(value)] || '状态待确认';
}

export function distributionAdjustmentLabel(value: unknown): string {
  const labels: Record<string, string> = {
    buyer_refund: '买家退款调整', qualification_hold: '资格暂缓',
    qualification_revoke: '资格撤销', qualification_restore: '资格恢复',
    manual_recovery: '人工追回登记', merchant_liability: '商户承担登记',
  };
  return labels[valueOf(value)] || '调整待确认';
}

export function distributionSettlementStatusLabel(value: unknown): string {
  const labels: Record<string, string> = {
    planned: '待提交', accepted: '已受理', attempted: '处理中',
    outcome_unknown: '结果待核验', receiver_succeeded: '分账成功',
    cancelled: '已取消', exception: '异常待处理',
  };
  return labels[valueOf(value)] || '状态待确认';
}

export function distributionExceptionLabel(value: unknown): string {
  const labels: Record<string, string> = {
    settlement_unknown: '结算结果待核验', settlement_not_paid: '分账未完成',
    settlement_deadline: '结算时限异常', settlement_deadline_imminent: '结算时限提醒',
    receiver_unavailable: '收款准备未完成', qualification_revoked_after_paid: '资格变化后已分账',
    buyer_refund_after_paid: '退款后已分账', unfreeze_final_failed: '解冻失败',
    merchant_liability: '商户承担', recovery: '追回登记',
  };
  return labels[valueOf(value)] || '异常待确认';
}
