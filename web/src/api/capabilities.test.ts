import { ADMIN_SCREENS, CAPABILITIES, transactionReadiness } from './capabilities';

function assert(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }

export function runCapabilityTests(): void {
  assert(transactionReadiness.orderRead.ready === true, 'order reads must be real after TX02 migration/API readiness');
  assert(transactionReadiness.refundIntent.ready === false, 'refund intents must stay blocked before TX05 Payment/EER readiness');
  assert(transactionReadiness.alipayRefundIntent.ready === false, 'Alipay refund must stay blocked because the frozen donor is read-only');
  const transaction = CAPABILITIES.filter((cap) => cap.surface === 'admin' && cap.screen.split('/').includes('orders'));
  assert(transaction.length === 3 && transaction.filter((cap) => cap.state === 'real').length === 1 && transaction.filter((cap) => cap.state === 'backend_blocked').length === 2, 'transaction capabilities must project each typed readiness independently');
  assert(transaction.filter((cap) => cap.state === 'backend_blocked').every((cap) => Boolean(cap.reason)), 'blocked transaction capabilities need an explicit unavailable reason');
  const stale = CAPABILITIES.filter((cap) => cap.state === 'backend_blocked' && cap.reason === 'OpenAPI operation 已存在，Kimi 壳尚未完成 DTO Adapter');
  assert(stale.length === 0, 'existing OpenAPI operation must be real or have a precise semantic reason');
  const pending = CAPABILITIES.filter((cap) => cap.state === 'backend_blocked' && /待批次|adapter.pending|DTO Adapter/i.test(cap.reason || ''));
  assert(pending.length === 0, 'adapter-pending is not an allowed backend_blocked reason');
  assert(CAPABILITIES.every((cap) => cap.state !== 'excluded_duplicate_page'), 'excluded legacy rows belong only in docs/frontend-capability-scope.md');
  assert(CAPABILITIES.filter((cap) => cap.state === 'real').length > 0, 'real inventory must not be empty');
  const memberGridPublicShare = CAPABILITIES.filter((cap) => cap.surface === 'admin' && cap.screen === 'spProductData' && cap.state === 'backend_blocked');
  assert(memberGridPublicShare.length === 1 && memberGridPublicShare[0]?.action === '周期商品分享读取、二维码与链接预览', 'Only Member Grid QR and link-preview gaps may stay backend_blocked');
  assert(CAPABILITIES.some((cap) => cap.screen === 'spProductData' && cap.action === '成员网格公开只读分享、撤销与一次性链接' && cap.state === 'real'), 'Member Grid public read-only share must be classified as real');
  assert(ADMIN_SCREENS.length === 39, 'Admin screen denominator changed without capability review');
  assert(!CAPABILITIES.some((cap) => cap.surface === 'h5' && cap.state === 'real' && /OAuth|授权/.test(cap.action)), 'Disabled H5 OAuth cannot be classified as real');
  assert(CAPABILITIES.some((cap) => cap.surface === 'sidebar' && cap.state === 'real' && /JSSDK/.test(cap.action) && /真实外部效果/.test(cap.reason || '')), 'Sidebar client-side sending needs an explicit external-effect boundary');
  for (const screen of ADMIN_SCREENS) assert(CAPABILITIES.some((cap) => cap.surface === 'admin' && cap.screen.split('/').includes(screen)), `Admin screen has no capability classification: ${screen}`);
}
