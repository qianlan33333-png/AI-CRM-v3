// Runtime Host for the frozen ownerMig page. The legacy template, controller,
// CSV/XLSX guard and donor behavior remain byte-frozen; this Host only binds
// the approved V3 two-mode API and never calls a Provider itself.
type Mode = 'local_only' | 'wecom_then_crm';
type Preview = { ID: string; Mode: Mode; SourceStaffID: number; TargetStaffID: number; CorpScope: string; Hash: string; ConfirmationPhrase: string; ExpiresAt: string; Rows: Array<{ Line: number; CustomerID: number; State: string; Reason?: string }> };
type Batch = { ID: string; Mode: Mode; State: string; Lines: Array<{ Line: number; CustomerID: number; State: string; TransferStatus?: number; TakeoverAt?: string | null }> };
type FileRange = { customerIDs: number[]; sourceStaffID: number; targetStaffID: number };

const base = '/api/admin/customers/owner-handoffs';
const key = () => `owner-handoff-${crypto.getRandomValues(new Uint32Array(2)).join('-')}`;
const esc = (x: unknown) => String(x ?? '').replace(/[&<>'"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[c] || c));

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...init, headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `请求失败（${response.status}）`);
  return body as T;
}

function positiveID(value: unknown, label: string): number {
  const id = typeof value === 'number' ? value : Number(String(value).trim());
  if (!Number.isSafeInteger(id) || id < 1) throw new Error(`${label}必须是正整数`);
  return id;
}

function enteredIDs(raw: string): number[] {
  const out = raw.split(/[\s,，;；]+/).filter(Boolean).map(value => positiveID(value, '客户 ID'));
  if (!out.length || out.length > 20000 || new Set(out).size !== out.length) throw new Error('客户 ID 必须是 1 至 20000 个唯一正整数');
  return out;
}

// The pre-existing XLSX guard turns the first sheet into CSV. This parser then
// accepts only its frozen four columns; V3 uses the file only to select one
// source/target range and does not accept per-row target changes silently.
function csvRows(input: string): string[][] {
  const rows: string[][] = [[]];
  let cell = '', quoted = false;
  for (let index = 0; index < input.length; index++) {
    const char = input[index];
    if (quoted) {
      if (char === '"' && input[index + 1] === '"') { cell += '"'; index++; continue; }
      if (char === '"') { quoted = false; continue; }
      cell += char;
      continue;
    }
    if (char === '"') {
      if (cell !== '') throw new Error('CSV 引号格式无效');
      quoted = true;
    } else if (char === ',') {
      rows.at(-1)!.push(cell); cell = '';
    } else if (char === '\n' || char === '\r') {
      if (char === '\r' && input[index + 1] === '\n') index++;
      rows.at(-1)!.push(cell); cell = '';
      rows.push([]);
    } else {
      cell += char;
    }
  }
  if (quoted) throw new Error('CSV 引号格式无效');
  rows.at(-1)!.push(cell);
  return rows.filter(row => row.some(value => value.trim() !== ''));
}

async function rangeFromFile(file: File): Promise<FileRange> {
  const { ownerReassignmentCsvFromFile } = await import('../ownerReassignmentFile');
  const rows = csvRows(await ownerReassignmentCsvFromFile(file));
  const header = ['customer_id', 'expected_owner_staff_id', 'expected_updated_at', 'target_owner_staff_id'];
  if (!rows.length || rows[0].length !== header.length || rows[0].some((value, index) => value !== header[index])) throw new Error(`文件第一行必须且只能是：${header.join(',')}`);
  const customerIDs: number[] = [];
  let sourceStaffID = 0;
  let targetStaffID = 0;
  for (const [offset, row] of rows.slice(1).entries()) {
    if (row.length !== 4 || !row[2].trim()) throw new Error(`第 ${offset + 2} 行格式无效`);
    const customerID = positiveID(row[0], `第 ${offset + 2} 行客户 ID`);
    const source = positiveID(row[1], `第 ${offset + 2} 行原负责人 ID`);
    const target = positiveID(row[3], `第 ${offset + 2} 行目标负责人 ID`);
    if (!sourceStaffID) { sourceStaffID = source; targetStaffID = target; }
    if (source !== sourceStaffID || target !== targetStaffID) throw new Error('一次迁移文件只能包含同一源负责人和目标负责人');
    customerIDs.push(customerID);
  }
  if (!customerIDs.length || customerIDs.length > 20000 || new Set(customerIDs).size !== customerIDs.length) throw new Error('文件必须包含 1 至 20000 个唯一客户 ID');
  return { customerIDs, sourceStaffID, targetStaffID };
}

function saveCSV(filename: string, rows: Array<Array<string | number>>) {
  const quote = (value: string | number) => `"${String(value).replaceAll('"', '""')}"`;
  const blob = new Blob([rows.map(row => row.map(quote).join(',')).join('\r\n') + '\r\n'], { type: 'text/csv;charset=utf-8' });
  const href = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = href; link.download = filename; link.click();
  setTimeout(() => URL.revokeObjectURL(href), 0);
}

function previewRows(items: Preview['Rows'] | Batch['Lines']) {
  return `<table><thead><tr><th>行</th><th>客户 ID</th><th>状态</th><th>企微回查</th></tr></thead><tbody>${items.map(item => `<tr><td>${esc(item.Line)}</td><td>${esc(item.CustomerID)}</td><td>${esc(item.State)}</td><td>${'TransferStatus' in item && item.TransferStatus ? esc(item.TransferStatus) : '—'}</td></tr>`).join('')}</tbody></table>`;
}

function render(stage: HTMLElement, preview?: Preview, batch?: Batch, error = '') {
  const mode = preview?.Mode || batch?.Mode || 'wecom_then_crm';
  stage.innerHTML = `<section data-owner-handoff-host style="padding:20px;max-width:960px;display:grid;gap:14px"><header><h1>负责人迁移</h1><p>本地负责人和企微跟进关系分开保存。企微模式仅在每位客户明确受理后更新本地；最终接替需单独回查。</p></header><section><label>模式 <select data-mode><option value="wecom_then_crm" ${mode === 'wecom_then_crm' ? 'selected' : ''}>先企微转接，再更新 CRM</option><option value="local_only" ${mode === 'local_only' ? 'selected' : ''}>只更新本地 CRM</option></select></label><label>Corp scope <input data-scope value="${esc(preview?.CorpScope || 'wecom-corp:')}"/></label><label>源负责人 ID <input data-source value="${esc(preview?.SourceStaffID || '')}"/></label><label>目标负责人 ID <input data-target value="${esc(preview?.TargetStaffID || '')}"/></label><label>客户 ID <textarea data-customers rows="5">${esc(preview?.Rows.map(row => row.CustomerID).join('\n') || '')}</textarea></label><label>CSV/XLSX 范围文件 <input data-file type="file" accept=".csv,text/csv,.xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"/></label><button data-template>下载 CSV 模板</button><label>转接提示语 <textarea data-welcome rows="2"></textarea></label><button data-preview>生成预览</button><output data-error>${esc(error)}</output></section>${preview ? `<section data-preview-result><h2>冻结预览</h2><p>${esc(preview.ID)} · ${esc(preview.ExpiresAt)}</p>${previewRows(preview.Rows)}<button data-export-preview>导出预览</button><label>确认语 <input data-phrase value="${esc(preview.ConfirmationPhrase)}"/></label><button data-confirm>确认执行</button></section>` : ''}${batch ? `<section data-batch-result><h2>执行结果</h2><p>${esc(batch.ID)} · ${esc(batch.State)}</p>${previewRows(batch.Lines)}<button data-export-results>导出结果</button>${batch.Mode === 'wecom_then_crm' ? '<button data-refresh>回查企微转接结果</button>' : ''}</section>` : ''}</section>`;
  bind(stage, preview, batch);
}

function bind(stage: HTMLElement, preview?: Preview, batch?: Batch) {
  const fail = (error: unknown) => { const node = stage.querySelector<HTMLOutputElement>('[data-error]'); if (node) node.textContent = error instanceof Error ? error.message : '请求失败'; };
  stage.querySelector('[data-template]')?.addEventListener('click', () => saveCSV('负责人迁移模板.csv', [['customer_id', 'expected_owner_staff_id', 'expected_updated_at', 'target_owner_staff_id']]));
  stage.querySelector('[data-export-preview]')?.addEventListener('click', () => { if (preview) saveCSV(`负责人迁移预览-${preview.ID}.csv`, [['line', 'customer_id', 'state', 'reason'], ...preview.Rows.map(row => [row.Line, row.CustomerID, row.State, row.Reason || ''])]); });
  stage.querySelector('[data-export-results]')?.addEventListener('click', () => { if (batch) saveCSV(`负责人迁移结果-${batch.ID}.csv`, [['line', 'customer_id', 'state', 'transfer_status', 'takeover_at'], ...batch.Lines.map(row => [row.Line, row.CustomerID, row.State, row.TransferStatus || '', row.TakeoverAt || ''])]); });
  stage.querySelector('[data-preview]')?.addEventListener('click', () => void (async () => {
    try {
      const file = stage.querySelector<HTMLInputElement>('[data-file]')?.files?.[0];
      const range = file ? await rangeFromFile(file) : { customerIDs: enteredIDs(stage.querySelector<HTMLTextAreaElement>('[data-customers]')!.value), sourceStaffID: positiveID(stage.querySelector<HTMLInputElement>('[data-source]')!.value, '源负责人 ID'), targetStaffID: positiveID(stage.querySelector<HTMLInputElement>('[data-target]')!.value, '目标负责人 ID') };
      const result = await api<Preview>(`${base}/previews`, { method: 'POST', body: JSON.stringify({ mode: stage.querySelector<HTMLSelectElement>('[data-mode]')!.value as Mode, source_staff_id: range.sourceStaffID, target_staff_id: range.targetStaffID, corp_scope: stage.querySelector<HTMLInputElement>('[data-scope]')!.value.trim(), customer_ids: range.customerIDs, welcome_message: stage.querySelector<HTMLTextAreaElement>('[data-welcome]')!.value, confirmation_phrase: 'CONFIRM', idempotency_key: key() }) });
      history.replaceState(null, '', `?handoff_preview=${encodeURIComponent(result.ID)}`); render(stage, result);
    } catch (caught) { fail(caught); }
  })());
  stage.querySelector('[data-confirm]')?.addEventListener('click', () => void (async () => {
    if (!preview) return;
    try {
      const result = await api<Batch>(`${base}/confirm`, { method: 'POST', body: JSON.stringify({ preview_id: preview.ID, preview_hash: preview.Hash, confirmation_phrase: stage.querySelector<HTMLInputElement>('[data-phrase]')!.value, idempotency_key: key() }) });
      history.replaceState(null, '', `?handoff_batch=${encodeURIComponent(result.ID)}`); render(stage, preview, result);
    } catch (caught) { fail(caught); }
  })());
  stage.querySelector('[data-refresh]')?.addEventListener('click', () => void (async () => {
    if (!batch) return;
    try { render(stage, preview, await api<Batch>(`${base}/batches/${encodeURIComponent(batch.ID)}/transfer-result`, { method: 'POST', body: JSON.stringify({ idempotency_key: key() }) })); } catch (caught) { fail(caught); }
  })());
}

export async function mountOwnerHandoff(stage: HTMLElement): Promise<void> {
  const query = new URLSearchParams(location.search);
  try {
    const previewID = query.get('handoff_preview');
    const batchID = query.get('handoff_batch');
    const [preview, batch] = await Promise.all([previewID ? api<Preview>(`${base}/previews/${encodeURIComponent(previewID)}`) : Promise.resolve(undefined), batchID ? api<Batch>(`${base}/batches/${encodeURIComponent(batchID)}`) : Promise.resolve(undefined)]);
    render(stage, preview, batch);
  } catch (caught) { render(stage, undefined, undefined, caught instanceof Error ? caught.message : '页面加载失败'); }
}
