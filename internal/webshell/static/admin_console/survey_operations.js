(function () {
  'use strict';
  const platformFetch = typeof window.fetch === 'function' ? window.fetch.bind(window) : function () { return Promise.reject(new Error('fetch unavailable')); };
  function text(value) { return value == null ? '' : String(value); }
  function csrfToken() { const match = document.cookie.match(/(?:^|;\s*)(?:aicrm_admin_csrf|aicrm_csrf)=([^;]+)/); return match ? decodeURIComponent(match[1]) : ''; }
  function requestKey() { return 'survey-host-' + (crypto.randomUUID ? crypto.randomUUID() : Date.now().toString(36)); }
  async function adminRequest(path, options) {
    const headers = new Headers((options && options.headers) || {}); headers.set('X-CSRF-Token', csrfToken()); headers.set('Idempotency-Key', requestKey());
    const response = await platformFetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers: headers }));
    if (!response.ok) { const error = new Error('HTTP ' + response.status); error.status = response.status; throw error; }
    return response.json();
  }
  function appendCell(row, value) { const cell = document.createElement('td'); cell.textContent = text(value || '—'); cell.style.cssText = 'padding:8px;border-bottom:1px solid #f2f3f5;vertical-align:top'; row.appendChild(cell); }
  function statusLabel(item) {
    if (item.status === 'queued') return '等待处理'; if (item.status === 'executed') return item.provider_result_received === true ? '已收到处理结果' : '已完成处理';
    if (item.status === 'outcome_unknown') return '处理结果待确认（不会自动重复发送）'; if (item.status === 'disabled') return '当时未启用外推配置';
    if (item.status === 'legacy_success') return '历史记录：已完成'; if (item.status === 'legacy_failed' || item.status === 'final_failed') return '未完成'; if (item.status === 'attempted') return '正在等待结果'; return '历史记录：' + text(item.status || '状态未知');
  }
  function attemptLabel(item) { const attempt = Number.isInteger(item.provider_attempt_number) ? item.provider_attempt_number : item.attempt_count; return Number.isInteger(attempt) ? '尝试 ' + attempt + ' 次' : '尝试次数未记录'; }
  function findExternalPushCard() {
    const heading = Array.from(document.querySelectorAll('h3')).find(function (node) { return node.textContent.trim() === '外部推送绑定'; });
    return heading && heading.parentElement && heading.parentElement.parentElement && heading.parentElement.parentElement.parentElement ? heading.parentElement.parentElement.parentElement : null;
  }
  function findLogCard() {
    const heading = Array.from(document.querySelectorAll('h3')).find(function (node) { return /问卷.*(?:外推|推送).*记录/.test(node.textContent); });
    return heading ? heading.parentElement : null;
  }
  function installQrFallback() {
    const page = document.body.dataset.page || ''; if (!['questionnaires', 'questionnaireDetail', 'questionnaireOps'].includes(page)) return;
    const pending = new WeakSet();
    function scan() { if (!document || !document.body) return; const box = document.getElementById('shareQrBox'); if (!box || pending.has(box)) return; pending.add(box); setTimeout(function () { if (!box.isConnected || box.childElementCount || text(box.textContent).trim()) return; const alert = document.createElement('div'); alert.setAttribute('role', 'alert'); alert.dataset.surveyQrFallback = 'true'; alert.style.cssText = 'padding:16px;color:#d93026;text-align:center;line-height:1.6'; alert.textContent = '二维码加载失败，请使用上方“复制”按钮复制链接。'; box.replaceChildren(alert); }, 1200); }
    scan(); new MutationObserver(scan).observe(document.body, { childList: true, subtree: true });
  }
  function makeMetadataForm(payload, operationsPath, refresh) {
    const push = payload.external_push || {}, metadata = push.metadata && typeof push.metadata === 'object' ? push.metadata : {};
    const form = document.createElement('form'); form.dataset.surveyPushMetadata = 'true'; form.style.cssText = 'display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px;margin:14px 0 0;padding:12px;border:1px solid #eef0f2;border-radius:6px';
    const title = document.createElement('strong'); title.textContent = '外推参数'; title.style.cssText = 'grid-column:1/-1;font-size:13px'; form.appendChild(title);
    [['推送类型', 'type'], ['有效期秒时间戳', 'expires_at_ts'], ['第几天', 'day'], ['频次', 'frequency'], ['备注', 'remark']].forEach(function (item) { const label = document.createElement('label'); label.textContent = item[0]; const input = document.createElement('input'); input.name = item[1]; input.value = metadata[item[1]] == null ? '' : String(metadata[item[1]]); input.style.cssText = 'display:block;width:100%;box-sizing:border-box;margin-top:4px;height:28px;border:1px solid #dee0e3;border-radius:4px'; label.appendChild(input); form.appendChild(label); });
    const params = document.createElement('div'); params.dataset.surveyPushParams = 'true'; params.style.cssText = 'grid-column:1/-1;display:grid;gap:6px'; form.appendChild(params);
    function paramRow(name, value) { const row = document.createElement('div'); row.style.cssText = 'display:grid;grid-template-columns:1fr 1fr auto;gap:6px'; const key = document.createElement('input'), val = document.createElement('input'), remove = document.createElement('button'); key.dataset.paramName = 'true'; key.placeholder = '参数名'; key.value = name || ''; val.dataset.paramValue = 'true'; val.placeholder = '参数值'; val.value = value || ''; remove.type = 'button'; remove.textContent = '删除'; remove.onclick = function () { row.remove(); }; row.append(key, val, remove); params.appendChild(row); }
    Object.entries(metadata.custom_params || {}).forEach(function (item) { paramRow(item[0], item[1]); }); if (!params.children.length) paramRow('', '');
    const add = document.createElement('button'); add.type = 'button'; add.textContent = '添加自定义参数'; add.onclick = function () { paramRow('', ''); }; form.appendChild(add);
    const save = document.createElement('button'); save.type = 'submit'; save.textContent = '保存外推参数'; save.style.cssText = 'width:max-content;height:28px;border:0;border-radius:4px;background:#3370ff;color:#fff;padding:0 10px'; form.appendChild(save);
    form.addEventListener('submit', async function (event) {
      event.preventDefault();
      try {
        const next = { custom_params: {} }; ['type', 'expires_at_ts', 'day', 'frequency', 'remark'].forEach(function (key) { const value = form.elements[key].value.trim(); if (value) { const numeric = /^(day|frequency|expires_at_ts)$/.test(key), parsed = numeric ? Number(value) : value; if (numeric && (!Number.isFinite(parsed) || parsed < 0)) throw new Error('invalid metadata field'); next[key] = parsed; } });
        params.querySelectorAll('div').forEach(function (row) { const name = row.querySelector('[data-param-name]').value.trim(), value = row.querySelector('[data-param-value]').value; if (name) next.custom_params[name] = value; });
        const reference = document.getElementById('opsConfigurationReference');
        const saved = await adminRequest(operationsPath + '/external-push', { method: 'PUT', headers: { 'Content-Type': 'application/json', Accept: 'application/json' }, body: JSON.stringify({ enabled: push.enabled === true, configuration_reference: reference ? reference.value.trim() : (push.configuration_reference || ''), metadata: next, configuration_version: payload.configuration_version }) });
        payload.configuration_version = saved.configuration_version; push.enabled = saved.external_push.enabled; push.configuration_reference = saved.external_push.configuration_reference; push.metadata = saved.external_push.metadata; save.textContent = '已保存';
      } catch (error) { save.textContent = error && (error.status === 409 || /\b409\b/.test(error.message || '')) ? '配置已更新，请重新打开后再保存' : '保存失败'; }
    });
    return form;
  }
  function renderLogs(card, current, global, scope, keyword, setScope) {
    card.replaceChildren(); card.dataset.surveyHostLogs = 'true';
    const header = document.createElement('div'); header.style.cssText = 'display:flex;justify-content:space-between;gap:8px;align-items:center'; const heading = document.createElement('h3'); heading.textContent = scope === 'global' ? '全部问卷外推记录' : '当前问卷外推记录'; heading.style.cssText = 'margin:0;font-size:15px'; const count = document.createElement('span'); count.style.cssText = 'font-size:12px;color:#8F959E'; header.append(heading, count); card.appendChild(header);
    const controls = document.createElement('div'); controls.style.cssText = 'display:flex;gap:8px;flex-wrap:wrap;margin-top:12px';
    [['当前问卷', 'current'], ['全部问卷', 'global']].forEach(function (item) { const button = document.createElement('button'); button.type = 'button'; button.textContent = item[0]; button.dataset.surveyLogScope = item[1]; button.style.cssText = 'height:28px;padding:0 10px;border:1px solid #DEE0E3;border-radius:6px;background:' + (scope === item[1] ? '#EFF4FF' : '#fff') + ';font-size:12px'; button.onclick = function () { setScope(item[1]); }; controls.appendChild(button); });
    const filter = document.createElement('input'); filter.placeholder = '测试记录 ID / 问卷 ID'; filter.value = keyword; filter.style.cssText = 'height:28px;min-width:180px;border:1px solid #DEE0E3;border-radius:6px;padding:0 8px;font-size:12px'; controls.appendChild(filter); card.appendChild(controls);
    const source = scope === 'global' ? global : current; const rows = source.filter(function (item) { return !filter.value.trim() || JSON.stringify(item).toLowerCase().includes(filter.value.trim().toLowerCase()); }); count.textContent = rows.length + ' 条';
    filter.oninput = function () { Array.from(body.querySelectorAll('tr')).forEach(function (row) { row.hidden = !row.dataset.surveySearch.includes(filter.value.trim().toLowerCase()); }); };
    if (!rows.length) { const empty = document.createElement('p'); empty.textContent = '暂无测试记录。'; card.appendChild(empty); return; }
    const table = document.createElement('table'); table.style.cssText = 'width:100%;border-collapse:collapse;margin-top:10px;font-size:12px'; const head = document.createElement('thead'), headRow = document.createElement('tr'); ['时间', '外推记录', '处理状态', '尝试情况', '备注'].forEach(function (label) { const cell = document.createElement('th'); cell.textContent = label; cell.style.cssText = 'text-align:left;padding:8px;border-bottom:1px solid #DEE0E3'; headRow.appendChild(cell); }); head.appendChild(headRow); const body = document.createElement('tbody'); rows.forEach(function (item) { const row = document.createElement('tr'); row.dataset.surveySearch = JSON.stringify(item).toLowerCase(); appendCell(row, item.occurred_at || item.updated_at || item.created_at); appendCell(row, item.source_pk || item.test_run_id || item.id); appendCell(row, statusLabel(item)); appendCell(row, attemptLabel(item)); appendCell(row, item.failure_category || (item.read_only_legacy ? '历史只读记录' : '—')); body.appendChild(row); }); table.append(head, body); card.appendChild(table);
  }
  async function mount() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    const questionnaireID = new URLSearchParams(location.search).get('id') || ''; if (!/^[1-9][0-9]*$/.test(questionnaireID)) return;
    const externalCard = findExternalPushCard(), logCard = findLogCard(); if (!externalCard || !logCard || externalCard.querySelector('[data-survey-push-metadata]')) return;
    const operationsPath = '/api/admin/questionnaires/' + questionnaireID + '/operations';
    try {
      const [payload, page] = await Promise.all([adminRequest(operationsPath, { method: 'GET', headers: { Accept: 'application/json' } }), adminRequest('/admin/questionnaires/external-push-logs?limit=100&offset=0', { method: 'GET', headers: { Accept: 'application/json' } })]);
      if (!payload || !Array.isArray(payload.items)) throw new Error('invalid operations');
      externalCard.appendChild(makeMetadataForm(payload, operationsPath));
      const logState = { current: payload.items, global: page && Array.isArray(page.items) ? page.items : [], scope: 'current' }; const redraw = function (nextScope) { logState.scope = nextScope; renderLogs(logCard, logState.current, logState.global, logState.scope, '', redraw); }; redraw(logState.scope);
      const originalTest = Array.from(externalCard.querySelectorAll('button')).find(function (button) { return button.textContent.includes('测试推送（仅本地记录）'); }); if (originalTest) originalTest.dataset.surveyHostTestPush = 'true';
      window.__surveyHostTestPush = async function (button) { button.disabled = true; button.textContent = '正在创建测试…'; try { const receipt = await adminRequest(operationsPath + '/external-push/test', { method: 'POST', headers: { Accept: 'application/json' } }); button.textContent = receipt && receipt.status === 'queued' ? '测试已创建，等待结果' : '测试已创建'; const refreshed = await adminRequest(operationsPath, { method: 'GET', headers: { Accept: 'application/json' } }); const refreshedPage = await adminRequest('/admin/questionnaires/external-push-logs?limit=100&offset=0', { method: 'GET', headers: { Accept: 'application/json' } }); logState.current = refreshed.items || []; logState.global = refreshedPage.items || []; redraw(logState.scope); } catch (error) { button.textContent = error && error.status === 403 ? '测试创建失败：无操作权限' : '创建测试失败'; } finally { button.disabled = false; } };
    } catch (_error) { const note = document.createElement('p'); note.setAttribute('role', 'alert'); note.textContent = '外推设置读取失败，请稍后重试。'; externalCard.appendChild(note); }
  }
  function installLegacyQuestionnaireOpsGuard() {
    if (document.body.dataset.page !== 'questionnaireOps') return;
    window.fetch = function (input, options) { const method = (options && options.method || (input && input.method) || 'GET').toUpperCase(); const url = typeof input === 'string' ? input : input && input.url || ''; if (method === 'GET' && /^\/admin\/questionnaires\/[1-9][0-9]*\/external-push-logs(?:\?|$)/.test(url)) { const body = JSON.stringify({items:[],total:0,limit:100,offset:0,has_more:false,local_only:true}); return Promise.resolve({ok:true,status:200,headers:new Headers({'Content-Type':'application/json'}),json:function () { return Promise.resolve(JSON.parse(body)); },text:function () { return Promise.resolve(body); },clone:function () { return this; }}); } return platformFetch(input, options); };
    document.addEventListener('click', function (event) { const button = event.target && event.target.closest && event.target.closest('button'); if (button && button.dataset.surveyHostTestPush === 'true') { event.preventDefault(); event.stopImmediatePropagation(); if (typeof window.__surveyHostTestPush === 'function') void window.__surveyHostTestPush(button); } }, true);
  }
  function start() {
    installQrFallback(); if (document.body.dataset.page !== 'questionnaireOps') return; const stage = document.getElementById('stage'); if (!stage) return;
    let scheduled = false; const ensureHost = function () { if (scheduled || (findExternalPushCard() && findExternalPushCard().querySelector('[data-survey-push-metadata]'))) return; scheduled = true; setTimeout(function () { scheduled = false; void mount(); }, 0); };
    new MutationObserver(ensureHost).observe(stage, { childList: true, subtree: true }); ensureHost(); setTimeout(ensureHost, 50);
  }
  installLegacyQuestionnaireOpsGuard();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true }); else start();
}());
