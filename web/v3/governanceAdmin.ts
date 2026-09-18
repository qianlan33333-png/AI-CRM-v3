import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';
import { openDetailDrawer } from './shared/ui/detailDrawer';

type Check = { id: string; owner: string; title: string; scope?: string; status: string; code: string; observed_at: string; metrics: Record<string, number> };
type Issue = { id: number; check_id: string; code: string; status: string; severity: string; version: number; first_seen: string; last_seen: string; occurrences: number };
type Overview = { fresh: boolean; observed_at: string; latest?: { id: number; release_sha: string; completed_at?: string }; checks: Check[]; issues: Issue[] };
type Tab = 'overview' | 'checks' | 'issues' | 'reports' | 'diagnostics' | 'profiles' | 'retention';
const root = document.querySelector<HTMLElement>('#governance-admin-root');
const labels: Record<Tab, string> = { overview: '治理总览', checks: '检查详情', issues: '问题跟踪', reports: '巡查报告', diagnostics: '错误聚合', profiles: '性能采样', retention: '数据生命周期' };
const statusLabels: Record<string, string> = { ok: '正常', warning: '需关注', critical: '严重', unknown: '未知', uncovered: '未覆盖', stale: '过期', open: '待处理', acknowledged: '已确认', resolved: '已恢复', queued: '待发送', executed: '飞书已接受', outcome_unknown: '发送结果未知', final_failed: '发送失败', disabled: '发送未启用' };
let tab: Tab = 'overview';
let serial = 0;
let diagnosticQuery = new URLSearchParams(location.search).get('correlation') || '';
if (diagnosticQuery) tab = 'diagnostics';
let overview: Overview | null = null;
let acceptedScan: {job: number; previousRun: number} | null = null;
let profilesEnabled = false;
let profileBusy = false;
let profileRequestKey: string | null = null;
const esc = (v: unknown): string => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
const date = (v: unknown): string => typeof v === 'string' && !Number.isNaN(Date.parse(v)) ? new Date(v).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—';
const badge = (s: string): string => `<span class="governance-status" data-status="${esc(s)}">${esc(statusLabels[s] || s)}</span>`;
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
function isOverview(v: unknown): v is Overview {
  return record(v) && typeof v.fresh === 'boolean' && Array.isArray(v.checks) && Array.isArray(v.issues)
    && v.checks.every((c: unknown) => record(c) && typeof c.id === 'string' && typeof c.status === 'string' && typeof c.title === 'string' && record(c.metrics))
    && v.issues.every((i: unknown) => record(i) && Number.isSafeInteger(i.id) && Number.isSafeInteger(i.version));
}
function csrf(): string {
  for (const part of document.cookie.split(';')) { const [name, ...rest] = part.trim().split('='); if (name === 'aicrm_csrf' || name === 'aicrm_admin_csrf') { try { return decodeURIComponent(rest.join('=')); } catch { return ''; } } }
  return '';
}
class AccessError extends Error {}
async function request(path: string, method = 'GET', body?: unknown, requestKey?: string): Promise<unknown> {
  const headers = new Headers({ Accept: 'application/json' });
  if (method !== 'GET') { headers.set('Content-Type', 'application/json'); headers.set('X-CSRF-Token', csrf()); headers.set('Idempotency-Key', requestKey || crypto.randomUUID()); }
  const response = await fetch(path, { method, headers, credentials: 'same-origin', cache: 'no-store', body: body === undefined ? undefined : JSON.stringify(body) });
  if (response.status === 401 || response.status === 403) throw new AccessError('需要超级管理员权限，请确认登录身份。');
  if (!response.ok) {
    const diagnosticID = response.headers.get('X-AICRM-Diagnostic-ID');
    const suffix = diagnosticID && /^[a-f0-9]{32}$/.test(diagnosticID) ? ` 排查编号：${diagnosticID}` : '';
    throw new Error((response.status === 409 ? '记录已更新或另一操作正在执行，请刷新后查看。' : response.status === 429 ? '已达到本小时次数或存储限额，请稍后再试。' : response.status === 410 ? '采样已超过 30 天，详情和文件不再提供。' : '读取或操作失败，请稍后重试。') + suffix);
  }
  return response.json();
}
function table(headers: string[], rows: string[][]): string {
  return rows.length ? `<div class="governance-table-wrap"><table class="admin-table"><thead><tr>${headers.map((h) => `<th>${esc(h)}</th>`).join('')}</tr></thead><tbody>${rows.map((row) => `<tr>${row.map((cell) => `<td>${cell}</td>`).join('')}</tr>`).join('')}</tbody></table></div>` : '<p class="governance-empty">当前没有记录。未采集不代表系统正常。</p>';
}
function storageEvidence(value: unknown): string {
  if (!record(value)) return '<p>暂无执行证据</p>';
  const n = (v: unknown): string => typeof v === 'number' ? esc(v) : '未知';
  const samples = Array.isArray(value.space_observations) ? value.space_observations.filter(record) : [];
  return `<p>${esc(value.status || value.state || 'unknown')} · ${esc(value.reason || value.code || '')} · ${esc(date(value.observed_at || value.finished_at))}</p>${'deleted_count' in value ? table(['删除发布包', '逻辑字节', '可用回滚版本'], [[n(value.deleted_count), n(value.deleted_bytes), n(value.rollback_count)]]) : ''}${samples.length ? table(['存储范围', '采样状态', '清理前可用字节', '清理后可用字节', '可用空间净变化'], samples.map((v) => [esc(v.resource), esc(v.state), n(v.available_bytes_before), n(v.available_bytes_after), n(v.available_bytes_net_change)])) : ''}`;
}
function metrics(c: Check): string { return Object.entries(c.metrics || {}).map(([name, value]) => `${esc(name)}：${esc(value)}`).join('<br>') || '—'; }
function layout(content: string): void {
  if (!root) return;
  renderHeader();
  root.innerHTML = `<nav class="governance-tabs" aria-label="运行治理">${Object.entries(labels).map(([key, title]) => `<button type="button" class="admin-button" data-tab="${key}" aria-current="${tab === key ? 'page' : 'false'}">${title}</button>`).join('')}</nav><div class="governance-content" aria-live="polite">${acceptedScan ? `<p class="governance-note" role="status">巡查已受理，任务 ${acceptedScan.job}，等待执行结果。可刷新查看最新结果。</p>` : ""}${content}</div>`;
  root.querySelectorAll<HTMLButtonElement>('[data-tab]').forEach((button) => button.addEventListener('click', () => { tab = button.dataset.tab as Tab; void refresh(); }));
}
function overviewContent(data: Overview): string {
  const count = (statuses: string[]) => data.checks.filter((c) => statuses.includes(c.status)).length;
  return `<p class="governance-freshness">${data.fresh ? '最新巡查' : '巡查结果已过期或尚未采集'} · ${esc(date(data.observed_at))} · 运行版本 ${esc(data.latest?.release_sha?.slice(0, 12) || '未知')}</p>
  <div class="governance-metrics">${[['检查目录', data.checks.length], ['异常', count(['warning', 'critical'])], ['未知或过期', count(['unknown', 'stale'])], ['未覆盖', count(['uncovered'])]].map(([title, n]) => `<div class="admin-panel"><span>${title}</span><strong>${n}</strong></div>`).join('')}</div>
  <section class="admin-panel"><h2>需要处理</h2>${table(['检查项', '状态', '原因', '最近观察'], data.checks.filter((c) => c.status !== 'ok').map((c) => [esc(c.title), badge(c.status), esc(c.code), esc(date(c.observed_at))]))}</section>
  <p class="governance-note">业务故障只报告和跟踪。纯过程明细保留 30 天；业务数据及防重依据永久保留。飞书接受与群内可见分别验证。</p>`;
}
async function refresh(): Promise<void> {
  const id = ++serial; const selected = tab;
  if (selected === 'profiles') profilesEnabled = false;
  layout('<p class="governance-empty" role="status">正在读取治理数据…</p>');
  try {
    if (['overview', 'checks', 'issues'].includes(selected)) {
      const data = await request('/api/admin/ops-inspections');
      if (id !== serial) return;
      if (!isOverview(data)) throw new Error('巡查数据格式不完整，暂时无法确认状态。');
      if (acceptedScan && data.latest && data.latest.id > acceptedScan.previousRun) acceptedScan = null;
      overview = data;
      if (selected === 'overview') layout(overviewContent(data));
      if (selected === 'checks') {
        layout(table(['检查项 / 负责人', '状态', '检查范围 / 原因', '指标', '观察时间'], data.checks.map((c) => [`${esc(c.title)}<br><small>${esc(c.owner)} · ${esc(c.id)}</small>`, badge(c.status), `${esc(c.scope || '范围见资源清单')}<br>${esc(c.code)}`, metrics(c), esc(date(c.observed_at))])));
      }
      if (selected === 'issues') {
        layout(table(['问题', '状态', '首次 / 最近', '出现次数', '操作'], data.issues.map((i) => [`${esc(i.check_id)}<br>${esc(i.code)}`, badge(i.status), `${esc(date(i.first_seen))}<br>${esc(date(i.last_seen))}`, esc(i.occurrences), i.status === 'open' ? `<button class="admin-button" data-ack="${i.id}">确认跟进</button>` : '—'])));
        root?.querySelectorAll<HTMLButtonElement>('[data-ack]').forEach((button) => button.addEventListener('click', async () => {
          const issue = overview?.issues.find((i) => i.id === Number(button.dataset.ack)); if (!issue) return;
          const mutationSerial = serial;
          button.disabled = true;
          try { await request(`/api/admin/ops-inspections/issues/${issue.id}`, 'PATCH', { version: issue.version, status: 'acknowledged' }); if (mutationSerial === serial) await refresh(); } catch (error) { if (mutationSerial === serial) showError(error); }
        }));
      }
      return;
    }
    const path = selected === 'reports' ? '/api/admin/ops-inspections/reports' : selected === 'profiles' ? '/api/admin/ops-diagnostics/cpu-profiles' : selected === 'diagnostics' ? `/api/admin/ops-diagnostics${diagnosticQuery ? `?correlation=${encodeURIComponent(diagnosticQuery)}` : ''}` : '/api/admin/ops-retention';
    const data = await request(path);
    if (id !== serial) return;
    if (!record(data) || !Array.isArray(data.items)) throw new Error('返回数据不完整，请稍后重试。');
    const items = data.items.filter(record);
    if (selected === 'reports') {
      layout(`<p>每小时一个报告窗口。飞书已接受不等于群内已确认可见。</p>${table(['类型 / 报告窗口', '发送状态', '关联效果', '详情'], items.map((v, n) => [`${esc(({ hourly: '小时报告', critical: '严重告警', recovery: '恢复通知' } as Record<string, string>)[String(v.notification_kind)] || '巡查报告')}<br>${esc(date(v.hour_key))}`, badge(String(v.effect_state)), esc(v.effect_id || '—'), `<button class="admin-button" data-report="${n}">查看报告</button>`]))}`);
      root?.querySelectorAll<HTMLButtonElement>('[data-report]').forEach((button) => button.addEventListener('click', () => { const v = items[Number(button.dataset.report)]; const body = document.createElement('pre'); body.className = 'governance-report'; body.textContent = record(v.content) && record(v.content.content) ? String(v.content.content.text ?? '') : '内容已到期或不可读取'; openDetailDrawer('巡查报告', body); }));
    } else if (selected === 'diagnostics') {
      layout(`<form class="governance-query"><label>页面排查编号 <input name="correlation" value="${esc(diagnosticQuery)}" maxlength="128" autocomplete="off" placeholder="粘贴报错时的排查编号"></label><button class="admin-button" type="submit">定位问题</button><button class="admin-button" type="button" data-clear-query>查看全部</button></form>${table(['错误类别 / 路由', '关联摘要', '版本', '关联任务 / 效果', '次数 / 首次 / 最近'], items.map((v) => [`${esc(v.code || v.error_code)}<br>${esc(v.route_template || '—')}`, esc(v.correlation_digest), esc(v.release_sha), `${esc(v.job_ref || '—')} / ${esc(v.effect_ref || '—')}`, `${esc(v.occurrences ?? 1)}<br>${esc(date(v.first_seen || v.occurred_at))}<br>${esc(date(v.last_seen || v.occurred_at))}`]))}`);
      root?.querySelector<HTMLFormElement>('form')?.addEventListener('submit', (event) => { event.preventDefault(); const value = root.querySelector<HTMLInputElement>('[name="correlation"]')?.value.trim() || ''; if (value && !/^[a-f0-9]{32}$/.test(value)) { showError(new Error('排查编号应为 32 位字母和数字。')); return; } diagnosticQuery = value; void refresh(); });
      root?.querySelector('[data-clear-query]')?.addEventListener('click', () => { diagnosticQuery = ''; void refresh(); });
    } else if (selected === 'profiles') {
      if (typeof data.enabled !== 'boolean' || data.target !== 'api' || data.duration_seconds !== 5 || data.worker_coverage !== 'not_supported') throw new Error('采样范围未确认，暂时无法启动。');
      profilesEnabled = data.enabled;
      const states: Record<string, string> = { sampling: '正在采样', outcome_unknown: '结果未知', completed: '已完成', failed: '采样失败' };
      layout(`<p>采集当前 API 服务的 5 秒 CPU 性能样本，用于定位耗时函数。Worker 进程尚未覆盖。</p><p>每人每小时最多 3 次，全局每小时最多 6 次；文件和在线详情 30 天后到期。</p>${profilesEnabled ? '' : '<p class="governance-note">采样暂未启用。需要先启用运行巡查与过程清理。</p>'}${profileBusy ? '<p role="status">正在采样，请稍候…</p>' : ''}${table(['采样编号 / 状态', '运行版本', '采样时间 / 到期时间', '文件', '操作'], items.map((v) => [esc(v.id) + '<br>' + esc(states[String(v.state)] || '未知'), esc(v.release_sha), esc(date(v.accepted_at)) + '<br>' + esc(date(v.expires_at)), typeof v.bytes === 'number' ? esc(v.bytes) + ' 字节' : '未知', v.state === 'completed' && typeof v.id === 'string' && /^[a-f0-9]{32}$/.test(v.id) && typeof v.expires_at === 'string' && Date.parse(v.expires_at) > Date.now() ? `<a class="admin-button" href="/api/admin/ops-diagnostics/cpu-profiles/${v.id}/download" download>下载样本</a>` : esc(v.failure_code || '暂无可下载文件')]))}`);
    } else {
      const history = await request('/api/admin/ops-retention/runs');
      if (id !== serial) return;
      if (!record(history) || !Array.isArray(history.items)) throw new Error('清理记录暂不可读，无法确认清理结果。');
      layout(`<p>纯过程数据保留 720 小时；业务事实、防重依据、在途状态和备份不参与按时间自动删除。</p>${table(['策略', '资源', '保留', '状态', '预览'], items.map((v) => [esc(v.id), esc(v.resource), esc(v.retention), esc(v.status), ['enabled', 'preview_only'].includes(String(v.status)) ? `<button class="admin-button" data-preview="${esc(v.id)}">预览候选</button>` : '受原生机制或保护规则管理']))}<h2>宿主过程文件与日志</h2>${record(history.host) && record(history.host.runtime) ? table(['删除文件', '逻辑载荷字节', '受保护文件'], [[esc(history.host.runtime.deleted),esc(history.host.runtime.bytes),esc(history.host.runtime.protected)]]) : ''}${storageEvidence(history.host)}<h2>发布包与回滚保护</h2>${storageEvidence(history.release)}<p>磁盘净变化包含同时发生的写入，可能为负，不能直接归因为清理回收量。</p><h2>最近数据库清理记录</h2>${table(['策略', '截止时间', '状态', '删除行数', '过程载荷字节', '完成时间'], history.items.filter(record).map((v) => [esc(v.policy), esc(date(v.cutoff)), esc(v.state), esc(v.deleted_rows), esc(v.payload_bytes), esc(date(v.completed_at))]))}<p class="governance-note">数据库载荷释放可供复用，不代表文件系统回收。清理仅执行已登记白名单；未分类资源保持保护。</p>`);
      root?.querySelectorAll<HTMLButtonElement>('[data-preview]').forEach((button) => button.addEventListener('click', async () => { const current = serial; button.disabled = true; try { const value = await request(`/api/admin/ops-retention/preview?policy=${encodeURIComponent(button.dataset.preview || '')}`); if (current !== serial) return; if (!record(value)) throw new Error('候选预览格式错误。'); const body = document.createElement('div'); body.textContent = `截止：${date(value.cutoff)}；${value.has_more === true ? '本批候选' : '候选'}：${value.candidates}${value.has_more === true ? '（仍有更多，未统计全部积压）' : ''}；估算载荷：${value.estimated_payload_bytes} 字节。${value.protected_reason || ''}`; openDetailDrawer('清理候选预览', body); } catch (error) { if (current === serial) showError(error); } finally { if (current === serial) button.disabled = false; } }));
    }
  } catch (error) { if (id === serial) showError(error); }
}
function showError(error: unknown): void { if (error instanceof AccessError) overview = null; layout(`<p class="governance-error" role="alert">${esc(error instanceof Error ? error.message : '操作失败')}</p>`); }
function renderHeader(): void {
  mountPageHeaderActions('governance', [{ label: '刷新', variant: 'secondary', onClick: refresh }, tab === 'profiles' ? { label: profileRequestKey ? '查看上次采样结果' : '采样 5 秒', variant: 'primary', disabled: !profilesEnabled || profileBusy, onClick: async () => {
    const current = serial;
    profileRequestKey ||= crypto.randomUUID();
    profileBusy = true;
    renderHeader();
    try {
      const result = await request('/api/admin/ops-diagnostics/cpu-profiles', 'POST', {}, profileRequestKey);
      if (!record(result) || !['sampling', 'outcome_unknown', 'completed', 'failed'].includes(String(result.state))) throw new Error('采样结果未确认，请查看上次结果。');
      if (result.state === 'completed' || result.state === 'failed') profileRequestKey = null;
      if (current === serial) await refresh();
    } catch (error) { if (current === serial) showError(error); }
    finally { profileBusy = false; renderHeader(); }
  } } : { label: '立即巡查', variant: 'primary', onClick: async () => { const current = serial; try { const result = await request('/api/admin/ops-inspections/runs', 'POST', {}); if (current === serial) { if (record(result) && result.state === 'accepted' && typeof result.job_id === 'number') acceptedScan = {job: result.job_id, previousRun: overview?.latest?.id || 0}; await refresh(); } } catch (error) { if (current === serial) showError(error); } } }]);
}
if (root) void refresh();
