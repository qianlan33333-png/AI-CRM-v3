type ClientSummary = {
  client_id: string;
  display_name: string;
  purpose: string;
  credential_hint?: string;
  audiences: string[];
  scopes: string[];
  capabilities: string[];
  allowed_cidrs: string[];
  owner_scope?: Record<string, string[]>;
  token_ttl_seconds: number;
  expires_at?: string;
  enabled: boolean;
  reissue_required: boolean;
  auth_version: number;
  last_used_at?: string;
  created_at: string;
};

type AuditEntry = {
  actor_admin_user_id?: number;
  action: string;
  outcome: string;
  details: unknown;
  created_at: string;
};

type OperationDescriptor = {
  operation_id: string;
  rest_method: string;
  rest_path: string;
  mcp_tool: string;
  capability: string;
  required_scope: string;
  schema_version: string;
};

type IssuedSecret = { clientID: string; secret: string };

const V1_PURPOSE = 'external_agent';
const V1_AUDIENCE = 'external_integration';
const KNOWN_SCOPES = ['read', 'write'];
const title = '开放平台调用方';

function cookie(name: string): string {
  const prefix = `${name}=`;
  for (const entry of document.cookie.split(';')) {
    const item = entry.trim();
    if (item.startsWith(prefix)) {
      try {
        return decodeURIComponent(item.slice(prefix.length));
      } catch {
        return '';
      }
    }
  }
  return '';
}

function csrf(): string {
  return cookie('aicrm_admin_csrf') || cookie('aicrm_csrf');
}

function element<K extends keyof HTMLElementTagNameMap>(tag: K, text?: string): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(label: string, onClick: () => void | Promise<void>, kind = 'secondary'): HTMLButtonElement {
  const node = element('button', label);
  node.type = 'button';
  node.dataset.openPlatformAction = label;
  node.className = `open-platform-button open-platform-button-${kind}`;
  node.addEventListener('click', () => { void onClick(); });
  return node;
}

function field(label: string, input: HTMLElement, note?: string): HTMLDivElement {
  const wrapper = element('div');
  wrapper.className = 'open-platform-field';
  const heading = element('span', label);
  heading.className = 'open-platform-field-label';
  wrapper.append(heading, input);
  if (note) {
    const help = element('small', note);
    help.className = 'open-platform-field-note';
    wrapper.append(help);
  }
  return wrapper;
}

function textInput(value = '', type = 'text'): HTMLInputElement {
  const node = document.createElement('input');
  node.type = type;
  node.value = value;
  node.className = 'open-platform-input';
  return node;
}

function textArea(value = ''): HTMLTextAreaElement {
  const node = document.createElement('textarea');
  node.value = value;
  node.className = 'open-platform-textarea';
  node.rows = 3;
  return node;
}

function checkList(values: string[], selected: string[], name: string): HTMLDivElement {
  const result = element('div');
  result.className = 'open-platform-checklist';
  for (const value of values) {
    const row = element('label');
    row.className = 'open-platform-check';
    const input = document.createElement('input');
    input.type = 'checkbox';
    input.name = name;
    input.value = value;
    input.checked = selected.includes(value);
    row.append(input, document.createTextNode(value));
    result.append(row);
  }
  return result;
}

function checkedValues(container: ParentNode, name: string): string[] {
  return [...container.querySelectorAll<HTMLInputElement>(`input[name="${name}"]:checked`)].map((entry) => entry.value).sort();
}

function cidrs(value: string): string[] {
  return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean);
}

function validTTL(value: string): number | undefined {
  const ttl = Number(value.trim());
  return Number.isInteger(ttl) && ttl >= 60 && ttl <= 3600 ? ttl : undefined;
}

function safeOwnerScope(value: string): Record<string, string[]> | null {
  const source = value.trim();
  if (!source) return null;
  const parsed: unknown = JSON.parse(source);
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) throw new Error('invalid_owner_scope');
  return parsed as Record<string, string[]>;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  const method = (init.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    headers.set('Content-Type', 'application/json');
    const token = csrf();
    if (token) headers.set('X-CSRF-Token', token);
  }
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' });
  const body: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    const code = body && typeof body === 'object' && 'error' in body && typeof body.error === 'string' ? body.error : 'request_failed';
    throw new Error(code);
  }
  return body as T;
}

function dateTimeLocalValue(value?: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return '';
  const pad = (part: number): string => String(part).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function unchangedDateTimeLocal(value: string, initial: string): boolean {
  if (!value || !initial) return false;
  const current = new Date(value).valueOf();
  const original = new Date(initial).valueOf();
  return !Number.isNaN(current) && current === original;
}

function formatTime(value?: string): string {
  if (!value) return '未设置';
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? '未设置' : parsed.toLocaleString('zh-CN', { hour12: false });
}

function exactV1Capabilities(items: OperationDescriptor[]): string[] {
  return [...new Set(items.map((item) => item.capability).filter(Boolean))].sort();
}

function styles(): HTMLStyleElement {
  const style = element('style');
  style.textContent = `
    .open-platform-root{box-sizing:border-box;min-height:100%;padding:20px;background:#f6f7f9;color:#1f2329;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    .open-platform-header,.open-platform-card{background:#fff;border:1px solid #dee0e3;border-radius:8px}
    .open-platform-header{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:16px 20px;margin-bottom:16px}.open-platform-header h1{font-size:18px;margin:0}.open-platform-muted{color:#646a73;font-size:13px}
    .open-platform-layout{display:grid;grid-template-columns:minmax(280px,34%) minmax(0,1fr);gap:16px}.open-platform-card{padding:16px;min-width:0}.open-platform-card h2{font-size:15px;margin:0 0 12px}.open-platform-list{display:grid;gap:8px}.open-platform-client{width:100%;text-align:left;border:1px solid #dee0e3;border-radius:6px;background:#fff;padding:10px;cursor:pointer}.open-platform-client[aria-current="true"]{border-color:#3370ff;background:#eff4ff}.open-platform-client strong,.open-platform-client small{display:block}.open-platform-client small{color:#646a73;margin-top:2px}
    .open-platform-form{display:grid;gap:12px}.open-platform-field{display:grid;gap:5px}.open-platform-field-label{font-weight:600}.open-platform-field-note{color:#646a73}.open-platform-input,.open-platform-textarea{box-sizing:border-box;width:100%;border:1px solid #bcc0c6;border-radius:5px;padding:7px;font:inherit;background:#fff}.open-platform-textarea{resize:vertical}.open-platform-checklist{display:flex;flex-wrap:wrap;gap:8px}.open-platform-check{border:1px solid #dee0e3;border-radius:4px;padding:4px 7px;display:inline-flex;gap:5px;align-items:center;font-size:12px}.open-platform-actions{display:flex;gap:8px;flex-wrap:wrap}.open-platform-button{border:1px solid #bcc0c6;border-radius:5px;background:#fff;padding:7px 10px;cursor:pointer;font:inherit}.open-platform-button-primary{background:#3370ff;border-color:#3370ff;color:#fff}.open-platform-button-danger{color:#c9352b;border-color:#d83931}.open-platform-status{min-height:20px;color:#646a73}.open-platform-status[data-error="true"]{color:#d83931}.open-platform-table{border-collapse:collapse;width:100%;font-size:12px}.open-platform-table th,.open-platform-table td{padding:8px;border-bottom:1px solid #eff0f1;text-align:left;vertical-align:top;word-break:break-word}.open-platform-table th{color:#646a73;font-weight:600}.open-platform-secret{white-space:pre-wrap;word-break:break-all;padding:10px;border-radius:6px;background:#f5f6f7;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.open-platform-dialog{border:0;border-radius:8px;box-shadow:0 12px 48px #0004;max-width:560px}.open-platform-dialog::backdrop{background:#0006}.open-platform-dialog-body{display:grid;gap:12px;min-width:min(460px,80vw)}.open-platform-catalog{margin-top:16px}.open-platform-code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}.open-platform-empty{padding:16px;color:#646a73}
    @media (max-width:800px){.open-platform-layout{grid-template-columns:1fr}.open-platform-root{padding:12px}}
  `;
  return style;
}

function setStatus(node: HTMLElement, message = '', error = false): void {
  node.textContent = message;
  node.dataset.error = String(error);
}

function secretDialog(issued: IssuedSecret, onActivate: () => Promise<void>, onRefresh: () => void | Promise<void>, onClose: () => void | Promise<void>): HTMLDialogElement {
  const dialog = document.createElement('dialog');
  dialog.className = 'open-platform-dialog';
  dialog.dataset.openPlatformSecret = issued.clientID;
  const body = element('div');
  body.className = 'open-platform-dialog-body';
  body.append(element('h2', '复制调用方密钥'));
  body.append(element('p', '此密钥只在本次创建或轮换后展示。复制并确认后才能启用调用方。'));
  const secret = element('code', issued.secret);
  secret.className = 'open-platform-secret';
  body.append(secret);
  const message = element('p');
  message.className = 'open-platform-status';
  body.append(message);
  const actions = element('div');
  actions.className = 'open-platform-actions';
  let activationUnconfirmed = false;
  const activate = async (): Promise<void> => {
    if (activationUnconfirmed) return;
    try {
      await onActivate();
      dialog.close();
    } catch {
      // A transport failure can arrive after the server committed activation.
      // Refresh the owner projection, keep this one-time secret unavailable for
      // retries, and never describe an unknown result as disabled.
      activationUnconfirmed = true;
      setStatus(message, '未确认启用结果，请刷新核对状态。', true);
      void Promise.resolve(onRefresh());
    }
  };
  actions.append(button('复制并确认启用', async () => {
    if (!navigator.clipboard || typeof navigator.clipboard.writeText !== 'function') {
      setStatus(message, '当前浏览器无法安全复制；请手动复制后再确认启用。', true);
      return;
    }
    try {
      await navigator.clipboard.writeText(issued.secret);
    } catch {
      setStatus(message, '复制失败；调用方仍保持停用。', true);
      return;
    }
    await activate();
  }, 'primary'));
  actions.append(button('我已手动复制并确认启用', activate));
  actions.append(button('关闭并清除密钥', () => dialog.close()));
  body.append(actions);
  dialog.append(body);
  dialog.addEventListener('close', () => {
    secret.textContent = '';
    void Promise.resolve(onClose());
    dialog.remove();
  }, { once: true });
  document.body.append(dialog);
  dialog.showModal();
  return dialog;
}

async function boot(): Promise<void> {
  if (document.body?.dataset.page !== 'apidocs') return;
  const stage = document.querySelector<HTMLElement>('#stage');
  if (!stage) return;
  const query = new URLSearchParams(window.location.search);
  let selectedID = query.get('client') || '';
  let selectedClient: ClientSummary | undefined;
  let clients: ClientSummary[] = [];
  let catalog: OperationDescriptor[] = [];
  let issued: IssuedSecret | null = null;

  document.head.append(styles());
  stage.replaceChildren();
  const root = element('section');
  root.className = 'open-platform-root';
  root.dataset.openPlatformHost = 'v1';
  stage.append(root);

  const loadSelected = async (): Promise<void> => {
    const clientID = selectedID;
    selectedClient = undefined;
    render();
    if (!clientID) return;
    try {
      const detail = await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(clientID)}`);
      if (selectedID !== clientID) return;
      selectedClient = detail.client;
      render();
    } catch {
      if (selectedID !== clientID) return;
      selectedClient = undefined;
      render();
    }
  };

  const refresh = async (): Promise<void> => {
    root.replaceChildren();
    const loading = element('p', '正在加载开放平台调用方…');
    loading.className = 'open-platform-empty';
    root.append(loading);
    try {
      const [clientResult, catalogResult] = await Promise.all([
        request<{ items: ClientSummary[] }>('/api/admin/open-platform/clients'),
        request<{ items: OperationDescriptor[] }>('/api/admin/open-platform/routes'),
      ]);
      clients = clientResult.items || [];
      catalog = catalogResult.items || [];
      if (!selectedID && clients.length) selectedID = clients[0].client_id;
      await loadSelected();
    } catch {
      root.replaceChildren(element('p', '开放平台管理暂不可用。'));
    }
  };

  const choose = (id: string): void => {
    selectedID = id;
    const next = new URL(window.location.href);
    next.searchParams.set('client', id);
    window.history.replaceState({}, '', next);
    void loadSelected();
  };

  const showIssuedSecret = (result: { client: ClientSummary; secret: string }): void => {
    issued = { clientID: result.client.client_id, secret: result.secret };
    secretDialog(issued, async () => {
      if (!issued) throw new Error('missing_secret');
      await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(issued.clientID)}/activate`, {
        method: 'POST', body: JSON.stringify({ client_secret: issued.secret, copied_confirmed: true }),
      });
      issued = null;
      await refresh();
    }, refresh, async () => { issued = null; await refresh(); });
  };

  const renderCreate = (): HTMLElement => {
    const card = element('section');
    card.className = 'open-platform-card';
    card.append(element('h2', '新建 V1 调用方'));
    const form = element('form');
    form.className = 'open-platform-form';
    const clientID = textInput(); clientID.autocomplete = 'off'; clientID.dataset.openPlatformCreate = 'client_id';
    const displayName = textInput(); displayName.dataset.openPlatformCreate = 'display_name';
    const ttl = textInput('1800', 'number'); ttl.min = '60'; ttl.max = '3600'; ttl.dataset.openPlatformCreate = 'token_ttl_seconds';
    const ips = textArea(); ips.dataset.openPlatformCreate = 'allowed_cidrs';
    const capabilities = exactV1Capabilities(catalog);
    form.append(
      field('调用方 ID', clientID, '固定 V1 OAuth 调用方标识。'),
      field('显示名称', displayName),
      field('Token TTL（秒）', ttl),
      field('来源 CIDR（可留空）', ips, '以逗号或换行分隔。'),
      field('Scope', checkList(KNOWN_SCOPES, ['read'], 'create-scope')),
      field('V1 能力', checkList(capabilities, capabilities.includes('platform.capabilities.read') ? ['platform.capabilities.read'] : [], 'create-capability')),
    );
    const message = element('p'); message.className = 'open-platform-status';
    form.append(message, button('创建并显示一次密钥', async () => {
      const scopes = checkedValues(form, 'create-scope');
      const granted = checkedValues(form, 'create-capability');
      const ttlSeconds = validTTL(ttl.value);
      if (!clientID.value.trim() || !displayName.value.trim() || ttlSeconds === undefined || scopes.length === 0 || granted.length === 0) {
        setStatus(message, '请填写调用方、TTL，并至少选择一项 scope 与能力。', true);
        return;
      }
      try {
        const result = await request<{ client: ClientSummary; secret: string }>('/api/admin/open-platform/clients', {
          method: 'POST',
          body: JSON.stringify({
            client_id: clientID.value.trim(), display_name: displayName.value.trim(), purpose: V1_PURPOSE,
            audiences: [V1_AUDIENCE], scopes, capabilities: granted, allowed_cidrs: cidrs(ips.value), token_ttl_seconds: ttlSeconds,
          }),
        });
        selectedID = result.client.client_id;
        showIssuedSecret(result);
      } catch {
        setStatus(message, '创建未完成。请检查 V1 授权字段并重试。', true);
      }
    }, 'primary'));
    card.append(form);
    return card;
  };

  const renderDetail = (client: ClientSummary | undefined): HTMLElement => {
    const card = element('section');
    card.className = 'open-platform-card';
    if (!client) {
      card.append(element('h2', '调用方详情'), element('p', '选择已有调用方，或创建新的 V1 调用方。'));
      return card;
    }
    card.dataset.openPlatformClient = client.client_id;
    card.append(element('h2', client.display_name));
    const summary = element('p', `${client.client_id} · ${client.enabled ? '已启用' : '待启用或已停用'} · OAuth 版本 ${client.auth_version}`);
    summary.className = 'open-platform-muted';
    card.append(summary);
    const form = element('form'); form.className = 'open-platform-form';
    const displayName = textInput(client.display_name); displayName.dataset.openPlatformEdit = 'display_name';
    const ttl = textInput(String(client.token_ttl_seconds), 'number'); ttl.min = '60'; ttl.max = '3600'; ttl.dataset.openPlatformEdit = 'token_ttl_seconds';
    const ips = textArea(client.allowed_cidrs.join('\n')); ips.dataset.openPlatformEdit = 'allowed_cidrs';
    const capabilityValues = exactV1Capabilities(catalog);
    const initialExpiresAt = client.expires_at;
    const initialExpiresLocal = dateTimeLocalValue(initialExpiresAt);
    const expires = textInput(initialExpiresLocal, 'datetime-local'); expires.step = '1'; expires.dataset.openPlatformEdit = 'expires_at';
    const ownerScope = textArea(client.owner_scope && Object.keys(client.owner_scope).length ? JSON.stringify(client.owner_scope, null, 2) : ''); ownerScope.dataset.openPlatformEdit = 'owner_scope';
    form.append(
      field('显示名称', displayName), field('Token TTL（秒）', ttl), field('来源 CIDR（可留空）', ips),
      field('Scope', checkList(KNOWN_SCOPES, client.scopes, 'edit-scope')),
      field('V1 能力', checkList(capabilityValues, client.capabilities, 'edit-capability')),
      field('Owner scope（可留空以清除）', ownerScope, '仅受服务端验证的资源范围。'),
      field('到期时间（可留空以清除）', expires),
    );
    const message = element('p'); message.className = 'open-platform-status';
    const actions = element('div'); actions.className = 'open-platform-actions';
    actions.append(button('保存授权', async () => {
      const scopes = checkedValues(form, 'edit-scope');
      const granted = checkedValues(form, 'edit-capability');
      const ttlSeconds = validTTL(ttl.value);
      if (!displayName.value.trim() || ttlSeconds === undefined || scopes.length === 0 || granted.length === 0) {
        setStatus(message, '请保留显示名称、TTL、scope 与能力。', true);
        return;
      }
      try {
        const scope = safeOwnerScope(ownerScope.value);
        await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}`, {
          method: 'PATCH',
          body: JSON.stringify({ display_name: displayName.value.trim(), audiences: [V1_AUDIENCE], scopes, capabilities: granted, allowed_cidrs: cidrs(ips.value), token_ttl_seconds: ttlSeconds, owner_scope: scope, expires_at: unchangedDateTimeLocal(expires.value, initialExpiresLocal) ? initialExpiresAt ?? null : (expires.value ? new Date(expires.value).toISOString() : null) }),
        });
        await refresh();
      } catch {
        setStatus(message, '保存未完成；现有授权没有在页面上更新。', true);
      }
    }, 'primary'));
    actions.append(button('轮换密钥', async () => {
      try {
        const result = await request<{ client: ClientSummary; secret: string }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}/rotate`, { method: 'POST', body: '{}' });
        showIssuedSecret(result);
      } catch { setStatus(message, '轮换未完成。', true); }
    }));
    if (client.enabled) {
      actions.append(button('停用调用方', async () => {
        try { await request<{ client: ClientSummary }>(`/api/admin/open-platform/clients/${encodeURIComponent(client.client_id)}/disable`, { method: 'POST', body: '{}' }); await refresh(); }
        catch { setStatus(message, '停用未完成。', true); }
      }, 'danger'));
    }
    form.append(message, actions);
    card.append(form);
    card.append(renderAudit(client.client_id));
    return card;
  };

  const renderAudit = (clientID: string): HTMLElement => {
    const section = element('section'); section.className = 'open-platform-catalog';
    section.append(element('h2', '最近审计'));
    const body = element('div', '正在读取审计…'); body.className = 'open-platform-muted'; section.append(body);
    void request<{ items: AuditEntry[] }>(`/api/admin/open-platform/clients/${encodeURIComponent(clientID)}/audit?limit=50`).then((result) => {
      const table = element('table'); table.className = 'open-platform-table';
      const head = element('thead'); const header = element('tr');
      for (const label of ['时间', '操作', '结果', '详情']) header.append(element('th', label));
      head.append(header); const rows = element('tbody');
      for (const entry of result.items || []) {
        const row = element('tr');
        const details = typeof entry.details === 'string' ? entry.details : JSON.stringify(entry.details ?? {});
        for (const value of [formatTime(entry.created_at), entry.action, entry.outcome, details]) row.append(element('td', value));
        rows.append(row);
      }
      if (!rows.children.length) { const row = element('tr'); const cell = element('td', '暂无审计记录'); cell.colSpan = 4; row.append(cell); rows.append(row); }
      table.append(head, rows); body.replaceWith(table);
    }).catch(() => { body.textContent = '审计暂不可读取。'; });
    return section;
  };

  const renderCatalog = (): HTMLElement => {
    const card = element('section'); card.className = 'open-platform-card open-platform-catalog';
    card.append(element('h2', 'V1 能力目录'));
    const table = element('table'); table.className = 'open-platform-table';
    const head = element('thead'); const header = element('tr');
    for (const label of ['Operation', 'REST', 'MCP', 'Capability', 'Scope']) header.append(element('th', label));
    head.append(header); const rows = element('tbody');
    for (const item of catalog) {
      const row = element('tr');
      for (const value of [item.operation_id, `${item.rest_method} ${item.rest_path}`, item.mcp_tool, item.capability, item.required_scope]) {
        const cell = element('td', value); if (value.includes('.') || value.startsWith('/')) cell.className = 'open-platform-code'; row.append(cell);
      }
      rows.append(row);
    }
    table.append(head, rows); card.append(table);
    return card;
  };

  const render = (): void => {
    root.replaceChildren();
    const header = element('header'); header.className = 'open-platform-header';
    const lead = element('div'); lead.append(element('h1', title), element('p', 'V1 OAuth 调用方、最小授权与审计。密钥不会在离开本页后保留。'));
    header.append(lead, button('刷新', refresh)); root.append(header);
    const layout = element('div'); layout.className = 'open-platform-layout';
    const list = element('section'); list.className = 'open-platform-card'; list.append(element('h2', '调用方'));
    const clientList = element('div'); clientList.className = 'open-platform-list';
    for (const client of clients) {
      const row = element('button'); row.type = 'button'; row.className = 'open-platform-client'; row.setAttribute('aria-current', String(client.client_id === selectedID));
      row.append(element('strong', client.display_name), element('small', `${client.client_id} · ${client.enabled ? '已启用' : '已停用'} · ${client.credential_hint || '未签发'}`));
      row.addEventListener('click', () => choose(client.client_id)); clientList.append(row);
    }
    if (!clients.length) clientList.append(element('p', '尚无 V1 调用方。'));
    list.append(clientList, renderCreate());
    const detailColumn = element('div'); detailColumn.className = 'open-platform-list';
    detailColumn.append(renderDetail(selectedClient), renderCatalog());
    layout.append(list, detailColumn); root.append(layout);
  };

  await refresh();
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => { void boot(); }, { once: true });
} else {
  void boot();
}
